package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// 这组测试锁死一个**真实的越权缺口**：
//
// onAntiLeech 原先只在登录下载（controllers/file.go 的 Download）触发，
// 公开分享的 /api/shares/:id/download 与 /api/shares/:id/preview
// **完全不触发**。也就是说：装了防盗链插件的站点，分享链接依然可以被
// 任意第三方站点盗刷 —— 而分享恰恰是最需要防盗链的场景。
//
// 分两层断言：
//  1. 两条分享路径都必须触发钩子（否则缺口依旧）；
//  2. 钩子返回 blocked 时必须 403（否则触发也没用）；
//  3. 没插件 / 插件放行时分享必须照常可用（防"修安全把功能修坏"）。

const shareAntiLeechReferer = "https://evil.example.net/hotlink"

// setupShareAntiLeechTest 准备可真实流式输出的分享：本地策略 + 用户 + 文件 + 分享。
//
// 之所以把存储也搭起来（而不是只测 403 分支）：403 是在 handlerForFile
// **之前**返回的，光测 403 无法证明"放行时文件真的能取到"。
// 必须能真正读到字节，才能证明这道闸没有把正常分享也挡死。
func setupShareAntiLeechTest(t *testing.T) (*models.User, *models.File, *models.Share) {
	t.Helper()
	testutil.SetupDB(t)

	upDir := t.TempDir()
	if err := db.Get().Create(&models.Policy{
		ID: 1, Name: "local", Type: "local",
		Config:    models.From(models.LocalPolicyConfig(upDir)),
		IsDefault: true,
	}).Error; err != nil {
		t.Fatalf("创建策略失败: %v", err)
	}

	u := &models.User{UserName: "sharer", GroupID: 1}
	if err := db.Get().Create(u).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	const body = "hello share"
	const obj = "obj-share-antileech"
	if err := os.WriteFile(filepath.Join(upDir, obj), []byte(body), 0o644); err != nil {
		t.Fatalf("写入存储对象失败: %v", err)
	}

	f := &models.File{
		OwnerID: u.ID, Name: "share.txt", Extension: "txt",
		PolicyID: 1, SourceName: obj, Size: int64(len(body)),
	}
	if err := db.Get().Create(f).Error; err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}

	s := &models.Share{FileID: f.ID, OwnerID: u.ID}
	if err := db.Get().Create(s).Error; err != nil {
		t.Fatalf("创建分享失败: %v", err)
	}
	return u, f, s
}

// shareRouter 挂上分享下载与预览两条公开路由（与 routers/router.go 一致）。
func shareRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/shares/:id/download", DownloadShare)
	r.GET("/shares/:id/preview", PreviewShare)
	return r
}

func doShareRequest(t *testing.T, r *gin.Engine, path, referer string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestShareAntiLeechBlocksDownloadAndPreview 核心用例：
// 两条分享路径都过闸，且插件说 block 就是 403。
func TestShareAntiLeechBlocksDownloadAndPreview(t *testing.T) {
	_, f, s := setupShareAntiLeechTest(t)

	var seen []map[string]any
	plugin.Register(plugin.HookAntiLeech, func(ctx map[string]any) (map[string]any, error) {
		cp := make(map[string]any, len(ctx))
		for k, v := range ctx {
			cp[k] = v
		}
		seen = append(seen, cp)
		return nil, errors.New("blocked by test anti-leech")
	})
	defer plugin.UnregisterAll(plugin.HookAntiLeech)

	r := shareRouter()
	id := strconv.Itoa(int(s.ID))

	// 下载：必须 403
	if w := doShareRequest(t, r, "/shares/"+id+"/download", shareAntiLeechReferer); w.Code != http.StatusForbidden {
		t.Fatalf("分享**下载**未被防盗链拦截: status=%d body=%s", w.Code, w.Body.String())
	}
	// 预览：同样必须 403。
	//
	// 这条是本次修复里最容易漏的一半：preview 用 inline，
	// 但它返回的是**完整文件内容**，只拦 download 等于留了一条 exfil 通道。
	if w := doShareRequest(t, r, "/shares/"+id+"/preview", shareAntiLeechReferer); w.Code != http.StatusForbidden {
		t.Fatalf("分享**预览**未被防盗链拦截: status=%d body=%s", w.Code, w.Body.String())
	}

	if len(seen) != 2 {
		t.Fatalf("钩子触发次数 = %d, want 2（下载与预览各一次）", len(seen))
	}

	// ctx 契约：字段名与语义是给插件作者的公开约定，错了插件就静默不生效。
	for i, wantKind := range []string{"download", "preview"} {
		got := seen[i]
		if got["shareKind"] != wantKind {
			t.Errorf("第 %d 次触发 shareKind = %v, want %q", i+1, got["shareKind"], wantKind)
		}
		// 分享访问者是匿名的：userId 必须是 0，且**键存在**。
		// 键不存在时插件里的 numOf(ctx["userId"]) 会拿到 nil，
		// 与"存在且为 0"是两种不同语义 —— 契约要求后者。
		if v, ok := got["userId"]; !ok || fmt.Sprint(v) != "0" {
			t.Errorf("第 %d 次触发 userId = %v (存在=%v), want 0 且键存在", i+1, v, ok)
		}
		if fmt.Sprint(got["shareId"]) != strconv.Itoa(int(s.ID)) {
			t.Errorf("第 %d 次触发 shareId = %v, want %d", i+1, got["shareId"], s.ID)
		}
		if fmt.Sprint(got["fileId"]) != strconv.Itoa(int(f.ID)) {
			t.Errorf("第 %d 次触发 fileId = %v, want %d", i+1, got["fileId"], f.ID)
		}
		if got["fileName"] != f.Name {
			t.Errorf("第 %d 次触发 fileName = %v, want %q", i+1, got["fileName"], f.Name)
		}
		if got["referer"] != shareAntiLeechReferer {
			t.Errorf("第 %d 次触发 referer = %v, want %q", i+1, got["referer"], shareAntiLeechReferer)
		}
	}
}

// TestShareAntiLeechAllowsByDefault 防"修安全把功能修坏"：
// 没有插件（或插件放行）时，分享下载与预览必须照常返回真实内容。
func TestShareAntiLeechAllowsByDefault(t *testing.T) {
	_, _, s := setupShareAntiLeechTest(t)
	r := shareRouter()
	id := strconv.Itoa(int(s.ID))

	// 没有注册任何 onAntiLeech handler
	w := doShareRequest(t, r, "/shares/"+id+"/download", "")
	if w.Code != http.StatusOK {
		t.Fatalf("无插件时分享下载应 200，实际 %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "hello share" {
		t.Fatalf("分享下载内容 = %q, want %q", got, "hello share")
	}

	w2 := doShareRequest(t, r, "/shares/"+id+"/preview", "")
	if w2.Code != http.StatusOK {
		t.Fatalf("无插件时分享预览应 200，实际 %d body=%s", w2.Code, w2.Body.String())
	}
}

// TestShareAntiLeechNonBlockingErrorAllows 插件返回**非** blocked 错误时不得拦截。
//
// 这条守的是判定语义：只有含 "blocked" 的 error 才算拦截。
// 插件因为内部原因（比如审计写盘失败）返回普通 error 时，
// 不该把用户的下载拦下来。
func TestShareAntiLeechNonBlockingErrorAllows(t *testing.T) {
	_, _, s := setupShareAntiLeechTest(t)

	plugin.Register(plugin.HookAntiLeech, func(ctx map[string]any) (map[string]any, error) {
		return nil, errors.New("audit log write failed") // 不含 blocked
	})
	defer plugin.UnregisterAll(plugin.HookAntiLeech)

	w := doShareRequest(t, shareRouter(), "/shares/"+strconv.Itoa(int(s.ID))+"/download", "")
	if w.Code != http.StatusOK {
		t.Fatalf("非 blocked 错误不应拦截，实际 status=%d body=%s", w.Code, w.Body.String())
	}
}
