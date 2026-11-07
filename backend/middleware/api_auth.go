package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/internal/cryptox"
	"github.com/nebula-drive/nebula/internal/hooks"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// findAccessToken 按令牌查找 OAuth2 access token，查不到返回 nil。
//
// 令牌在库中以单向哈希存储，因此查询必须对输入先哈希。
// 第二段的明文查询是**迁移兜底**：若存量数据尚未被迁移脚本转换，
// 宁可临时放过一条明文令牌，也不能让迁移失败导致所有 API 调用全部失效。
// 迁移完成后（models.MigrateSensitiveFields）这段自然不会再命中。
func findAccessToken(tok string) *models.AccessToken {
	var at models.AccessToken
	if err := db.Get().Where("token = ?", cryptox.TokenHash(tok)).First(&at).Error; err == nil {
		return &at
	}
	if err := db.Get().Where("token = ?", tok).First(&at).Error; err == nil {
		return &at
	}
	return nil
}

// findPAT 按令牌查找 Personal Access Token，查不到返回 nil。
// 迁移兜底逻辑同 findAccessToken。
func findPAT(tok string) *models.PersonalAccessToken {
	var pat models.PersonalAccessToken
	if err := db.Get().Where("token = ?", cryptox.TokenHash(tok)).First(&pat).Error; err == nil {
		return &pat
	}
	if err := db.Get().Where("token = ?", tok).First(&pat).Error; err == nil {
		return &pat
	}
	return nil
}

// APIAuth 开放 API 鉴权：从 Authorization: Bearer xxx 提取 token，
// 先查 AccessToken 表（OAuth2），再查 PersonalAccessToken 表（PAT），
// 验证有效期后设置当前用户到 context。
//
// 两种内置方式都失败后交给 onApiAuth 钩子做扩展鉴权（见 pluginAPIAuth）。
func APIAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := extractBearerToken(c)

		// 1. OAuth2 access token
		if tok != "" {
			if at := findAccessToken(tok); at != nil {
				if at.ExpiresAt.Before(time.Now()) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "token expired"})
					return
				}
				var u models.User
				if err := db.Get().First(&u, at.UserID).Error; err != nil {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "user not found"})
					return
				}
				c.Set(CtxUserKey, &u)
				c.Set("api_scope", at.Scope)
				c.Set("api_token_type", "oauth")
				// 成功路径校验（v2 语义）：内置凭据有效后插件仍可否决或补充 claims
				if !apiAuthHook(c, &u, at.Scope, "oauth") {
					return
				}
				c.Next()
				return
			}
		}

		// 2. Personal Access Token
		if tok != "" {
			if pat := findPAT(tok); pat != nil {
				if pat.ExpiresAt != nil && pat.ExpiresAt.Before(time.Now()) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "token expired"})
					return
				}
				var u models.User
				if err := db.Get().First(&u, pat.UserID).Error; err != nil {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "user not found"})
					return
				}
				db.Get().Model(pat).Update("last_used_at", time.Now())
				c.Set(CtxUserKey, &u)
				c.Set("api_scope", pat.Scope)
				c.Set("api_token_type", "pat")
				// 成功路径校验（v2 语义），同上
				if !apiAuthHook(c, &u, pat.Scope, "pat") {
					return
				}
				c.Next()
				return
			}
		}

		// 3. 扩展鉴权（onApiAuth 钩子）
		//
		// 刻意只在**内置方式都失败之后**才触发：
		//   - 热路径零成本 —— PAT/OAuth 正常的请求不必为一次 IPC 往返买单；
		//   - 内置凭据始终优先，插件无法否决一个已经有效的令牌。
		//
		// ⚠ **"根本没带 Authorization"也要走到这里。**
		// HMAC 这类方案压根不用 Authorization 头（签名放在自定义头里），
		// 若像原来那样在缺 token 时就早退，扩展鉴权永远收不到这类请求 ——
		// 症状是"插件配好了、日志里却一片空白"，而且 401 看起来完全正常，
		// 极难排查。本轮的端到端冒烟正是这样把它抓出来的。
		switch u, decision := pluginAPIAuth(c); decision {
		case apiAuthGranted:
			c.Set(CtxUserKey, u)
			c.Set("api_scope", "plugin")
			c.Set("api_token_type", "plugin")
			c.Next()
			return
		case apiAuthDenied:
			// 插件明确拒绝，响应已由 pluginAPIAuth 写好
			return
		}

		// 兜底 401：区分"没带凭据"与"凭据不对"，
		// 报错指向的问题完全不同，合并成一句话会让调用方无从下手。
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "missing token"})
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "invalid token"})
	}
}

// pluginPrincipalKey 插件通过 onApiAuth 声明「这个请求是谁」的 ctx 字段名。
//
// 刻意不叫 userId：这里的语义是「扩展鉴权认定出的调用者」，
// 与其它钩子里「已登录用户」不是一回事，共用字段名迟早会被误读。
const pluginPrincipalKey = "principalUserId"

// maxBodyHashBytes 参与 bodyHash 的请求体上限。
//
// 本中间件在**鉴权失败**时才跑到扩展鉴权，而失败路径是攻击者可控的：
// 不限量地读 body 等于给了一个内存放大面。超限时不读全文，
// 只标记 bodyTruncated，由插件自己决定要不要放行。
const maxBodyHashBytes = 64 << 10

// apiAuthDecision 扩展鉴权的判定结果。
type apiAuthDecision int

const (
	// apiAuthNoOpinion 插件没给身份：交回调用方按内置逻辑返回 401。
	apiAuthNoOpinion apiAuthDecision = iota
	// apiAuthGranted 插件认定了调用者身份。
	apiAuthGranted
	// apiAuthDenied 插件明确拒绝（或给了无效身份），响应已写好。
	apiAuthDenied
)

// pluginAPIAuth 在内置鉴权失败后询问插件。
func pluginAPIAuth(c *gin.Context) (*models.User, apiAuthDecision) {
	ctx := map[string]any{
		"method":           c.Request.Method,
		"path":             c.Request.URL.Path,
		"query":            c.Request.URL.RawQuery,
		"ip":               c.ClientIP(),
		"ua":               c.Request.UserAgent(),
		"headers":          headersExcept(c, "Authorization"),
		"hasAuthorization": c.GetHeader("Authorization") != "",
		"contentType":      c.GetHeader("Content-Type"),
		"contentLength":    c.Request.ContentLength,
	}
	// bodyHash 只在能读完整个 body 时给出：半截数据的哈希没有任何意义，
	// 给出去只会让插件拿错误的哈希做校验（然后表现为"签名永远不对"）。
	if sum, truncated := bodySHA256(c, maxBodyHashBytes); sum != "" {
		ctx["bodyHash"] = sum
		ctx["bodyTruncated"] = false
	} else {
		ctx["bodyHash"] = ""
		ctx["bodyTruncated"] = truncated
	}

	for _, e := range plugin.Fire(plugin.HookApiAuth, ctx) {
		if plugin.IsBlockedError(e) {
			c.AbortWithStatusJSON(http.StatusForbidden,
				gin.H{"code": 403, "message": "blocked by onApiAuth plugin"})
			return nil, apiAuthDenied
		}
	}

	id, ok := positiveUint(ctx[pluginPrincipalKey])
	if !ok {
		return nil, apiAuthNoOpinion
	}
	var u models.User
	if err := db.Get().First(&u, id).Error; err != nil {
		// 插件声明了一个不存在的用户。不能放过（fail closed），
		// 但必须给出与"没有令牌"不同的提示 —— 否则表现为
		// "插件明明放行了却还是 invalid token"，排查方向会跑偏。
		c.AbortWithStatusJSON(http.StatusUnauthorized,
			gin.H{"code": 401, "message": "onApiAuth plugin returned unknown principal"})
		return nil, apiAuthDenied
	}
	return &u, apiAuthGranted
}

// headersExcept 返回请求头（键为规范形式），排除指定项。
//
// 刻意不把 Authorization 交给插件：内置凭据留在宿主这边。
// 插件要用自定义凭据（例如 HMAC 签名）应当用自己的头 ——
// 这既减少了凭据扩散面，也逼出一个明确的约定，而不是让插件去猜
// "Authorization 里的东西算不算已经验证过"。
func headersExcept(c *gin.Context, skip ...string) map[string]string {
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[http.CanonicalHeaderKey(s)] = true
	}
	out := make(map[string]string, len(c.Request.Header))
	for k, v := range c.Request.Header {
		if skipSet[http.CanonicalHeaderKey(k)] {
			continue
		}
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// bodySHA256 计算请求体的 sha256，并**把已读走的字节放回请求流**。
//
// 放回这件事必须做对：本中间件只是"顺手看一眼"，后续 handler
// （例如 /api/v1/files/upload）仍要读到完整 body。用 MultiReader
// 先给缓冲、再接剩余流，两条路径（读完 / 截断）都能保证字节不丢。
func bodySHA256(c *gin.Context, limit int64) (sum string, truncated bool) {
	if c.Request.Body == nil {
		return "", false
	}
	orig := c.Request.Body
	buf, err := io.ReadAll(io.LimitReader(orig, limit+1))

	// 无论成功与否都先把读走的字节放回去
	c.Request.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(buf), orig), orig}

	if err != nil {
		return "", true // 读失败 → 无法提供可信摘要
	}
	if int64(len(buf)) > limit {
		return "", true
	}
	h := sha256.Sum256(buf)
	return "sha256:" + hex.EncodeToString(h[:]), false
}

// positiveUint 从 ctx 值里取出一个正整数用户 ID。
//
// 必须兼容多种数字类型：进程内 handler 写进去的是 Go 的 int/uint，
// 而进程外插件的回写经 JSON 往返后是 float64。
// 只认其中一种的后果是"进程外插件放行无效"，而且**不报任何错**。
func positiveUint(v any) (uint, bool) {
	switch n := v.(type) {
	case float64:
		// JSON 只保证 2^53 内的整数精确；超出范围的值不可信
		if n > 0 && n < 1<<53 && n == float64(uint(n)) {
			return uint(n), true
		}
	case int:
		if n > 0 {
			return uint(n), true
		}
	case int64:
		if n > 0 {
			return uint(n), true
		}
	case uint:
		if n > 0 {
			return n, true
		}
	case uint64:
		if n > 0 {
			return uint(n), true
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i > 0 {
			return uint(i), true
		}
	}
	return 0, false
}

// extractBearerToken 从 Authorization 头提取 Bearer token
func extractBearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// apiAuthHook 触发 v2 的 onApiAuth（原 v1 占位钩子，现在真接线）。
// PAT / OAuth token 校验通过后，插件还能做 scope 之外的细粒度授权。
func apiAuthHook(c *gin.Context, u *models.User, scope, tokenType string) bool {
	allowed, reason, ctx := hooks.DecideMutate(plugin.HookApiAuth, map[string]any{
		"tokenType": tokenType, "userId": u.ID, "userName": u.UserName,
		"scope": scope, "ip": c.ClientIP(), "claims": map[string]any{},
	})
	if !allowed {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 403, "message": defaultMsg(reason, "API 访问被插件拒绝"),
		})
		return false
	}
	if claims, ok := ctx["claims"].(map[string]any); ok && len(claims) > 0 {
		c.Set("api_claims", claims)
	}
	return true
}
