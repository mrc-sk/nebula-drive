package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// 这组测试覆盖 onApiAuth 的接通（本轮实现）：
// 开放 API 的内置鉴权（PAT / OAuth2）失败后，交给插件扩展鉴权。
//
// 三个关键语义必须钉住：
//  1. **放行**要能生效 —— 插件在 ctx 里写 principalUserId；
//  2. **拒绝**要能生效 —— 插件返回含 blocked 的 error → 403；
//  3. **fail closed** —— 插件既不放行也不拒绝时保持 401，
//     内置鉴权失败不能因为"挂了插件"就变成放行。
//
// 另外两条容易被忽略、但一旦错了很难查的：
//   - 内置鉴权成功时**不该**触发钩子（热路径零成本 + 内置凭据优先）；
//   - 中间件"顺手看一眼 body"之后，body 必须仍能被后续 handler 完整读到。

// registerAPIAuth 注册一个 onApiAuth 处理器，测试结束自动清理。
func registerAPIAuth(t *testing.T, fn plugin.HandlerFunc) {
	t.Helper()
	plugin.Register(plugin.HookApiAuth, fn)
	t.Cleanup(func() { plugin.UnregisterAll(plugin.HookApiAuth) })
}

func apiAuthRouter(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APIAuth())
	r.Any("/*any", handler)
	return r
}

// TestAPIAuthPluginGrants 插件放行：写入 principalUserId，请求应通过，
// 且 CurrentUser 是插件认定的那个用户。
func TestAPIAuthPluginGrants(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "plugin-auth-user"}
	db.Get().Create(u)

	registerAPIAuth(t, func(ctx map[string]any) (map[string]any, error) {
		ctx[pluginPrincipalKey] = float64(u.ID) // 进程外插件回写的是 float64
		return nil, nil
	})

	r := apiAuthRouter(func(c *gin.Context) {
		cur := CurrentUser(c)
		if cur == nil {
			c.String(http.StatusInternalServerError, "no user")
			return
		}
		c.String(http.StatusOK, "user=%d", cur.ID)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("插件放行后应 200，实际 %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "user="+itoa(u.ID)) {
		t.Errorf("CurrentUser 不是插件认定的用户: %s", w.Body.String())
	}
}

// TestAPIAuthPluginBlocks 插件拒绝：返回含 blocked 的 error → 403。
//
// 用 403 而不是 401：这是"扩展鉴权明确说不"，不是"没带凭据"。
func TestAPIAuthPluginBlocks(t *testing.T) {
	testutil.SetupDB(t)

	registerAPIAuth(t, func(ctx map[string]any) (map[string]any, error) {
		return nil, errors.New("blocked by signature mismatch")
	})

	r := apiAuthRouter(func(c *gin.Context) { c.String(http.StatusOK, "should not reach") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	req.Header.Set("Authorization", "Bearer bad")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("插件拒绝应 403，实际 %d body=%s", w.Code, w.Body.String())
	}
}

// TestAPIAuthPluginNoOpinion 插件既不放行也不拒绝 → 保持 401。
//
// 这是 fail closed 的核心：装了插件不能把"鉴权失败"变成"放行"。
func TestAPIAuthPluginNoOpinion(t *testing.T) {
	testutil.SetupDB(t)

	called := false
	registerAPIAuth(t, func(ctx map[string]any) (map[string]any, error) {
		called = true
		return nil, nil // 什么也不写
	})

	r := apiAuthRouter(func(c *gin.Context) { c.String(http.StatusOK, "should not reach") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	req.Header.Set("Authorization", "Bearer bad")
	r.ServeHTTP(w, req)

	if !called {
		t.Fatal("内置鉴权失败后应触发 onApiAuth")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("插件没表态时应保持 401，实际 %d", w.Code)
	}
}

// TestAPIAuthPluginUnknownPrincipal 插件声明了一个不存在的用户 → 401。
//
// 必须 fail closed，且报错要与"没带令牌"区分开：
// 否则表现为"插件明明放行了却还是 invalid token"，排查方向会跑偏。
func TestAPIAuthPluginUnknownPrincipal(t *testing.T) {
	testutil.SetupDB(t)

	registerAPIAuth(t, func(ctx map[string]any) (map[string]any, error) {
		ctx[pluginPrincipalKey] = float64(999999)
		return nil, nil
	})

	r := apiAuthRouter(func(c *gin.Context) { c.String(http.StatusOK, "should not reach") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	req.Header.Set("Authorization", "Bearer bad")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未知用户应 401，实际 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unknown principal") {
		t.Errorf("报错应指明是插件给了未知身份，实际 = %s", w.Body.String())
	}
}

// TestAPIAuthValidTokenHotPathFree 内置鉴权成功路径的**热路径成本**约束。
//
// v2 把成功路径改成了"插件可否决有效令牌"（apiAuthHook → FireIf）——
// 这与本地最初的"内置成功即终局"语义不同，取舍后保留 v2 语义：
// 它防得住被盗 PAT 继续滥用，是更强的安全层。
//
// 但本地关切的热路径成本仍然成立，只是保证方式变了：
// FireIf 在**无订阅者时短路**，所以没装任何 onApiAuth 插件的部署
// （绝大多数）在成功路径上零 IPC 成本。本测试守住这一点：
// 不注册任何钩子时，有效 PAT 必须 200。
//
// 插件否决有效令牌的行为由 v2 上游测试覆盖；本地插件（hmac-auth）
// 走的是失败路径的 pluginAPIAuth，语义互不干扰。
func TestAPIAuthValidTokenHotPathFree(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "pat-hotpath"}
	db.Get().Create(u)
	db.Get().Create(&models.PersonalAccessToken{Token: "valid-pat", UserID: u.ID, Prefix: "valid-pat"})

	// 关键：不注册任何 onApiAuth 钩子 —— 模拟"没装扩展鉴权插件"的常见部署
	plugin.UnregisterAll(plugin.HookApiAuth)

	r := apiAuthRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	req.Header.Set("Authorization", "Bearer valid-pat")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("无插件时有效 PAT 应 200（热路径不得有无谓失败），实际 %d", w.Code)
	}
	// HasSubscribers 是 FireIf 短路的依据，这里直接验证它是 false ——
	// 若它误报 true，成功路径就会为一次空派发买单。
	if plugin.HasSubscribers(plugin.HookApiAuth) {
		t.Error("无注册时 HasSubscribers 应为 false（热路径短路的前提）")
	}
}

// TestAPIAuthPluginWithoutAuthorizationHeader 不带 Authorization 的请求
// 也必须能走到扩展鉴权。
//
// 这条是回归测试，对应一个真实踩过的坑：原先 APIAuth 在
// `tok == ""` 时直接 401 早退，而 HMAC 这类方案**压根不用 Authorization 头**
// （签名放在自定义头里）—— 于是扩展鉴权永远收不到这类请求。
// 症状极具迷惑性：插件配好了、进程在跑、钩子计数也对，
// 但插件日志里一片空白，客户端只拿到一个看起来完全正常的 401。
func TestAPIAuthPluginWithoutAuthorizationHeader(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "hmac-user"}
	db.Get().Create(u)

	reached := false
	registerAPIAuth(t, func(ctx map[string]any) (map[string]any, error) {
		reached = true
		if ctx["hasAuthorization"] != false {
			t.Errorf("hasAuthorization = %v, want false", ctx["hasAuthorization"])
		}
		ctx[pluginPrincipalKey] = float64(u.ID)
		return nil, nil
	})

	r := apiAuthRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	// 关键：**完全不设 Authorization**
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	req.Header.Set("X-Nebula-Signature", "sig")
	r.ServeHTTP(w, req)

	if !reached {
		t.Fatal("不带 Authorization 的请求没有走到扩展鉴权（HMAC 类方案会被完全挡在门外）")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("扩展鉴权放行后应 200，实际 %d body=%s", w.Code, w.Body.String())
	}
}

// TestAPIAuthNoTokenStillMissingToken 没有插件时，缺 token 的报错
// 仍是 "missing token"（与"凭据不对"区分开）。
func TestAPIAuthNoTokenStillMissingToken(t *testing.T) {
	testutil.SetupDB(t)

	r := apiAuthRouter(func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/files", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "missing token") {
		t.Errorf("应提示 missing token，实际 = %s", w.Body.String())
	}

	// 带了但无效 → 提示 invalid token
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	req2.Header.Set("Authorization", "Bearer nope")
	r.ServeHTTP(w2, req2)
	if !strings.Contains(w2.Body.String(), "invalid token") {
		t.Errorf("应提示 invalid token，实际 = %s", w2.Body.String())
	}
}

// TestAPIAuthPluginContextContract 插件收到的 ctx 契约。
//
// 字段名是宿主与插件两个 module 之间的约定，改名不会有编译错误 ——
// 插件只会静默读不到，表现为"签名永远校验不过"。
func TestAPIAuthPluginContextContract(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "ctx-user"}
	db.Get().Create(u)

	const body = `{"hello":"world"}`
	var got map[string]any
	registerAPIAuth(t, func(ctx map[string]any) (map[string]any, error) {
		got = make(map[string]any, len(ctx))
		for k, v := range ctx {
			got[k] = v
		}
		ctx[pluginPrincipalKey] = float64(u.ID)
		return nil, nil
	})

	r := apiAuthRouter(func(c *gin.Context) {
		// 中间件看过 body 之后，后续 handler 必须仍能读到完整内容
		b, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.String(http.StatusInternalServerError, "body read failed: %v", err)
			return
		}
		c.String(http.StatusOK, string(b))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files?x=1&y=2", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer bad")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nebula-Signature", "abc123")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("放行后应 200，实际 %d body=%s", w.Code, w.Body.String())
	}
	// body 完整性
	if w.Body.String() != body {
		t.Fatalf("请求体被中间件读坏了: got=%q want=%q", w.Body.String(), body)
	}

	if got == nil {
		t.Fatal("onApiAuth 未被触发")
	}
	if got["method"] != http.MethodPost {
		t.Errorf("method = %v", got["method"])
	}
	if got["path"] != "/api/v1/files" {
		t.Errorf("path = %v", got["path"])
	}
	if got["query"] != "x=1&y=2" {
		t.Errorf("query = %v", got["query"])
	}
	if got["hasAuthorization"] != true {
		t.Errorf("hasAuthorization = %v, want true", got["hasAuthorization"])
	}
	wantHash := "sha256:" + hex.EncodeToString(func() []byte { s := sha256.Sum256([]byte(body)); return s[:] }())
	if got["bodyHash"] != wantHash {
		t.Errorf("bodyHash = %v, want %s", got["bodyHash"], wantHash)
	}
	if got["bodyTruncated"] != false {
		t.Errorf("bodyTruncated = %v, want false", got["bodyTruncated"])
	}

	headers, ok := got["headers"].(map[string]string)
	if !ok {
		t.Fatalf("headers 类型不对: %T", got["headers"])
	}
	// 插件的自定义头必须能看到（HMAC 签名正是靠它）
	if headers["X-Nebula-Signature"] != "abc123" {
		t.Errorf("插件的自定义头不可见: %v", headers)
	}
	// Authorization 刻意不下发：内置凭据留在宿主
	for k := range headers {
		if strings.EqualFold(k, "Authorization") {
			t.Error("Authorization 不应下发给插件（内置凭据留在宿主）")
		}
	}
}

// TestPositiveUintAcceptsPluginNumberTypes 数字类型兼容性。
//
// 进程内 handler 写的是 int/uint，进程外插件回写经 JSON 往返后是 float64。
// 只认一种的后果是"进程外插件放行无效"，而且不报任何错。
func TestPositiveUintAcceptsPluginNumberTypes(t *testing.T) {
	ok := []any{7, uint(7), int64(7), uint64(7), float64(7), 1 << 40}
	for _, v := range ok {
		if _, good := positiveUint(v); !good {
			t.Errorf("positiveUint(%#v) 应被接受", v)
		}
	}
	if id, good := positiveUint(float64(7)); !good || id != 7 {
		t.Errorf("float64(7) → (%d,%v), want (7,true)", id, good)
	}

	// 2^53 的界限只针对 **float64** 路径：JSON 无法精确表示更大的整数，
	// 拿到的值本身已经不可信（可能已被四舍五入成另一个用户）。
	// int 路径没有这个歧义 —— 它来自进程内 handler，是精确的 Go 整数，
	// 所以 1<<40 在上面的 ok 列表里是被接受的。
	bad := []any{nil, 0, -1, "7", float64(1.5), float64(0), float64(1 << 60)}
	for _, v := range bad {
		if _, good := positiveUint(v); good {
			t.Errorf("positiveUint(%#v) 应返回 false", v)
		}
	}
}

func itoa(v uint) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
