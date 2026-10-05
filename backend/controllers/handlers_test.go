package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/host"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// mustUnmarshal 解析 JSON 响应体，失败直接终止测试。
func mustUnmarshal(t *testing.T, data []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("解析响应失败: %v\n响应体: %s", err, data)
	}
}

// makeUser 创建并返回一个已持久化的测试用户
func makeUser(t *testing.T, name string, admin bool) *models.User {
	t.Helper()
	u := &models.User{UserName: name, GroupID: 1, IsAdmin: admin}
	if err := db.Get().Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func userRouter(u *models.User) *gin.Engine {
	r := newTestRouter()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
	return r
}

// ---- auth.go ----

func TestLogout(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "logout", false)
	db.Get().Create(&models.Session{UserID: u.ID, Token: "tok", SessionID: "sid"})
	r := userRouter(u)
	r.POST("/logout", Logout)
	w := doJSON(r, http.MethodPost, "/logout", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var n int64
	db.Get().Model(&models.Session{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatalf("sessions not cleared: %d", n)
	}
}

func TestMeSuggest2FA(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "meuser", false)
	r := userRouter(u)
	r.GET("/me", Me)
	w := doJSON(r, http.MethodGet, "/me", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !contains(w.Body.String(), "suggest2FAHint") {
		t.Fatalf("expected suggest2FAHint, body=%s", w.Body.String())
	}
	// 第二次不应再提示
	w2 := doJSON(r, http.MethodGet, "/me", nil)
	if contains(w2.Body.String(), "suggest2FAHint") {
		t.Fatalf("should not suggest again, body=%s", w2.Body.String())
	}
}

func TestUpdateProfile(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "profuser", false)
	r := userRouter(u)
	r.PUT("/profile", UpdateProfile)
	w := doJSON(r, http.MethodPut, "/profile", map[string]any{
		"preferLang": "en-US", "themeMode": "dark", "nickName": "Nick", "avatar": "a.png",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got models.User
	db.Get().First(&got, u.ID)
	if got.NickName != "Nick" || got.PreferLang != "en-US" {
		t.Fatalf("profile not updated: %+v", got)
	}
}

func TestUpdateProfileEmpty(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "profempty", false)
	r := userRouter(u)
	r.PUT("/profile", UpdateProfile)
	// 空更新（无字段）→ 仍 200
	w := doJSON(r, http.MethodPut, "/profile", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestUpdateProfileBadBody(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "profbad", false)
	r := userRouter(u)
	r.PUT("/profile", UpdateProfile)
	req := httptest.NewRequest(http.MethodPut, "/profile", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestVerifyCaptchaNoSecret(t *testing.T) {
	testutil.SetupDB(t)
	// 未配置 hcaptcha secret → 直接放行
	if !verifyCaptcha("") {
		t.Fatal("expected true when no secret configured")
	}
}

func TestVerifyCaptchaEmptyToken(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SeedSetting("security.hcaptcha_secret", "0x123")
	if verifyCaptcha("") {
		t.Fatal("expected false for empty token with secret configured")
	}
}

// ---- admin.go ----

func TestListUsersPagination(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin1", true)
	for i := 0; i < 3; i++ {
		makeUser(t, "u"+string(rune('a'+i)), false)
	}
	r := userRouter(admin)
	r.GET("/users", ListUsers)
	w := doJSON(r, http.MethodGet, "/users?page=1&size=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !contains(w.Body.String(), "total") {
		t.Fatalf("bad body: %s", w.Body.String())
	}
}

func TestListUsersBadPaging(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin2", true)
	r := userRouter(admin)
	r.GET("/users", ListUsers)
	// page/size 非法 → 回退默认
	w := doJSON(r, http.MethodGet, "/users?page=-1&size=999", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCreateUser(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin3", true)
	r := userRouter(admin)
	r.POST("/users", CreateUser)
	w := doJSON(r, http.MethodPost, "/users", map[string]any{
		"userName": "newbie", "password": "Secret123", "email": "n@e.com", "isAdmin": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var u models.User
	db.Get().Where("user_name = ?", "newbie").First(&u)
	if u.ID == 0 || !u.IsAdmin {
		t.Fatalf("user not created correctly: %+v", u)
	}
}

func TestCreateUserBadBody(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin4", true)
	r := userRouter(admin)
	r.POST("/users", CreateUser)
	w := doJSON(r, http.MethodPost, "/users", map[string]any{"userName": "x"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestUpdateUser(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin5", true)
	target := makeUser(t, "target1", false)
	r := userRouter(admin)
	r.PUT("/users/:id", UpdateUser)
	status := 1
	w := doJSON(r, http.MethodPut, "/users/"+itoa(target.ID), map[string]any{"status": status, "nickName": "Renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got models.User
	db.Get().First(&got, target.ID)
	if got.Status != 1 || got.NickName != "Renamed" {
		t.Fatalf("not updated: %+v", got)
	}
}

func TestUpdateUserNoChanges(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin6", true)
	target := makeUser(t, "target2", false)
	r := userRouter(admin)
	r.PUT("/users/:id", UpdateUser)
	w := doJSON(r, http.MethodPut, "/users/"+itoa(target.ID), map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestDeleteUser(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin7", true)
	target := makeUser(t, "target3", false)
	r := userRouter(admin)
	r.DELETE("/users/:id", DeleteUser)
	w := doJSON(r, http.MethodDelete, "/users/"+itoa(target.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestDeleteUserSelf(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin8", true)
	r := userRouter(admin)
	r.DELETE("/users/:id", DeleteUser)
	w := doJSON(r, http.MethodDelete, "/users/"+itoa(admin.ID), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGroups(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin9", true)
	r := userRouter(admin)
	r.GET("/groups", ListGroups)
	r.POST("/groups", CreateGroup)
	r.PUT("/groups/:id", UpdateGroup)

	// list empty
	w := doJSON(r, http.MethodGet, "/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	// create
	w2 := doJSON(r, http.MethodPost, "/groups", map[string]any{"name": "g1", "maxStorage": 1024, "shareEnabled": true})
	if w2.Code != http.StatusOK {
		t.Fatalf("create status = %d", w2.Code)
	}
	var g models.Group
	db.Get().Where("name = ?", "g1").First(&g)
	if g.ID == 0 {
		t.Fatal("group not created")
	}
	// update
	w3 := doJSON(r, http.MethodPut, "/groups/"+itoa(g.ID), map[string]any{"name": "g1u", "maxStorage": 2048})
	if w3.Code != http.StatusOK {
		t.Fatalf("update status = %d", w3.Code)
	}
}

func TestCreateGroupBadBody(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin10", true)
	r := userRouter(admin)
	r.POST("/groups", CreateGroup)
	w := doJSON(r, http.MethodPost, "/groups", map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

// 旧 TestPlugins 验证的是"POST /plugins/:id/toggle 翻转数据库布尔值"——
// 那个开关从未接线，属于已废弃的行为（详见 docs/插件系统完全指南.md）。
// 下面改为验证现行语义：协议关卡 + 列表返回真实运行状态。

func TestPluginListShowsRuntimeState(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin11", true)
	db.Get().Create(&models.Plugin{Name: "p1", Title: "Plugin1", Enabled: true})
	r := userRouter(admin)
	r.GET("/plugins", ListPlugins)
	r.GET("/plugins/hooks", ListPluginHooks)

	w := doJSON(r, http.MethodGet, "/plugins", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var resp struct {
		Code int          `json:"code"`
		Data []PluginView `json:"data"`
	}
	mustUnmarshal(t, w.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("列表长度 = %d, want 1", len(resp.Data))
	}
	v := resp.Data[0]
	// 关键回归点：enabled=true 但进程没跑时，必须暴露矛盾，
	// 否则管理员会看到"已启用"却毫无效果
	if v.Enabled && v.Status == host.StatusStopped && v.RuntimeError == "" {
		t.Fatal("enabled=true 但进程未运行时必须给出提示，当前 RuntimeError 为空")
	}
	// manifest 不存在（没装目录）必须标为无效
	if v.ManifestValid {
		t.Fatal("目录不存在时 ManifestValid 应为 false")
	}
	if v.ManifestError == "" {
		t.Fatal("清单无效时应给出原因")
	}
}

func TestPluginHooksReturnsRealCounts(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin12", true)
	r := userRouter(admin)
	r.GET("/plugins/hooks", ListPluginHooks)

	w := doJSON(r, http.MethodGet, "/plugins/hooks", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
			Doc   string `json:"doc"`
			Mode  string `json:"mode"`
			Wired bool   `json:"wired"`
		} `json:"data"`
	}
	mustUnmarshal(t, w.Body.Bytes(), &resp)
	// 必须是全部 10 个钩子（此前前端只列 8 个，漏了 collab 两个）
	if len(resp.Data) != 10 {
		t.Fatalf("钩子数 = %d, want 10", len(resp.Data))
	}
	byName := map[string]int{}
	for _, h := range resp.Data {
		byName[h.Name] = h.Count
		if h.Doc == "" {
			t.Errorf("钩子 %s 缺描述", h.Name)
		}
		// 预留未接入的钩子必须标出来，不能让管理员以为可用
		if !h.Wired && h.Count > 0 {
			t.Errorf("未接入的钩子 %s 不应有 handler", h.Name)
		}
	}
	// 无插件时全部为 0 —— 这正是此前前端用 Math.random() 伪造的东西
	if byName["onAntiLeech"] != 0 {
		t.Fatalf("onAntiLeech count = %d, want 0", byName["onAntiLeech"])
	}
}

func TestPluginAgreementGate(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin13", true)
	r := userRouter(admin)
	r.GET("/plugins/agreement", GetPluginAgreement)
	r.POST("/plugins/agreement", AcceptPluginAgreement)
	r.POST("/plugins/:name/enable", EnablePlugin)
	r.POST("/plugins/:name/disable", DisablePlugin)
	r.POST("/plugins/install", InstallPlugin)
	r.POST("/plugins/:name/uninstall", UninstallPlugin)

	// 初始：未同意
	w := doJSON(r, http.MethodGet, "/plugins/agreement", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("agreement status = %d", w.Code)
	}
	var ag struct {
		Code int `json:"code"`
		Data struct {
			Accepted bool `json:"accepted"`
			Version  int  `json:"version"`
		} `json:"data"`
	}
	mustUnmarshal(t, w.Body.Bytes(), &ag)
	if ag.Data.Accepted {
		t.Fatal("初始不应为已同意")
	}
	if ag.Data.Version != plugin.AgreementVersion {
		t.Fatalf("协议版本 = %d, want %d", ag.Data.Version, plugin.AgreementVersion)
	}

	// 未同意时，后端必须拒绝安装/启用 —— 不能只靠前端弹窗
	db.Get().Create(&models.Plugin{Name: "p1", Title: "P1"})
	for _, path := range []string{"/plugins/p1/enable", "/plugins/p1/uninstall"} {
		w := doJSON(r, http.MethodPost, path, nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("未同意时 %s 应 403，实际 %d", path, w.Code)
			continue
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("needAgreement")) {
			t.Errorf("%s 的错误响应应带 needAgreement 标记", path)
		}
	}
	w = doJSON(r, http.MethodPost, "/plugins/install", map[string]any{
		"source": "local", "path": t.TempDir(),
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("未同意时安装应 403，实际 %d", w.Code)
	}

	// 提交错误版本号应被拒（防止"同意"没读过的内容）
	w = doJSON(r, http.MethodPost, "/plugins/agreement", map[string]any{
		"version": plugin.AgreementVersion + 999, "accept": true,
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("版本不匹配应 409，实际 %d", w.Code)
	}
	// accept=false 应被拒
	w = doJSON(r, http.MethodPost, "/plugins/agreement", map[string]any{
		"version": plugin.AgreementVersion, "accept": false,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("accept=false 应 400，实际 %d", w.Code)
	}

	// 正确同意
	w = doJSON(r, http.MethodPost, "/plugins/agreement", map[string]any{
		"version": plugin.AgreementVersion, "accept": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("同意应成功，实际 %d: %s", w.Code, w.Body.String())
	}
	w = doJSON(r, http.MethodGet, "/plugins/agreement", nil)
	mustUnmarshal(t, w.Body.Bytes(), &ag)
	if !ag.Data.Accepted {
		t.Fatal("同意后应为已同意")
	}

	// 同意后启用不再被协议关卡拦住。
	// 测试环境没有初始化插件宿主，所以会停在 503（宿主未就绪）而非 400；
	// 关键是**不能再是 403** —— 那说明协议关卡还在拦。
	w = doJSON(r, http.MethodPost, "/plugins/p1/enable", nil)
	if w.Code == http.StatusForbidden {
		t.Fatal("同意后启用不应再被协议关卡拦为 403")
	}
	if w.Code != http.StatusServiceUnavailable && w.Code != http.StatusBadRequest {
		t.Fatalf("同意后启用应走到宿主/清单检查，"+
			"期望 503（宿主未就绪）或 400（清单无效），实际 %d: %s",
			w.Code, w.Body.String())
	}
}

func TestDisablePluginNotFound(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "admin14", true)
	r := userRouter(admin)
	r.POST("/plugins/:name/disable", DisablePlugin)
	w := doJSON(r, http.MethodPost, "/plugins/9999/disable", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

// ---- pat.go ----

func TestPATCRUD(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "patuser", false)
	r := userRouter(u)
	r.POST("/pat", CreatePAT)
	r.GET("/pat", ListPATs)
	r.DELETE("/pat/:id", DeletePAT)

	// create
	w := doJSON(r, http.MethodPost, "/pat", map[string]any{"name": "my token", "scope": "profile"})
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "nd_pat_") {
		t.Fatalf("token not returned: %s", w.Body.String())
	}
	// list
	w2 := doJSON(r, http.MethodGet, "/pat", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("list status = %d", w2.Code)
	}
	var pats []models.PersonalAccessToken
	db.Get().Where("user_id = ?", u.ID).Find(&pats)
	if len(pats) != 1 {
		t.Fatalf("expected 1 pat, got %d", len(pats))
	}
	// delete
	w3 := doJSON(r, http.MethodDelete, "/pat/"+itoa(pats[0].ID), nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w3.Code)
	}
}

func TestCreatePATBadBody(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "patbad", false)
	r := userRouter(u)
	r.POST("/pat", CreatePAT)
	w := doJSON(r, http.MethodPost, "/pat", map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestDeletePATNotFound(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "patnf", false)
	r := userRouter(u)
	r.DELETE("/pat/:id", DeletePAT)
	w := doJSON(r, http.MethodDelete, "/pat/9999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

// ---- oauth.go ----

func TestOAuthAppCRUD(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "oauthuser", false)
	r := userRouter(u)
	r.POST("/oauth/apps", CreateOAuthApp)
	r.GET("/oauth/apps", ListOAuthApps)
	r.DELETE("/oauth/apps/:id", DeleteOAuthApp)

	w := doJSON(r, http.MethodPost, "/oauth/apps", map[string]any{
		"name": "app1", "redirectUris": []string{"https://example.com/cb"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "clientSecret") {
		t.Fatalf("secret not returned: %s", w.Body.String())
	}
	w2 := doJSON(r, http.MethodGet, "/oauth/apps", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("list status = %d", w2.Code)
	}
	var app models.OAuthApp
	db.Get().Where("user_id = ?", u.ID).First(&app)
	w3 := doJSON(r, http.MethodDelete, "/oauth/apps/"+itoa(app.ID), nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w3.Code)
	}
}

func TestCreateOAuthAppBadBody(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "oauthbad", false)
	r := userRouter(u)
	r.POST("/oauth/apps", CreateOAuthApp)
	w := doJSON(r, http.MethodPost, "/oauth/apps", map[string]any{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestDeleteOAuthAppNotFound(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "oauthnf", false)
	r := userRouter(u)
	r.DELETE("/oauth/apps/:id", DeleteOAuthApp)
	w := doJSON(r, http.MethodDelete, "/oauth/apps/9999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestOAuthAuthorize(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "authz", false)
	app := models.OAuthApp{
		ClientID: "cid", ClientSecret: "csec", Name: "A",
		RedirectURIs: `["https://cb.example.com/cb"]`, UserID: u.ID,
	}
	db.Get().Create(&app)
	r := newTestRouter()
	r.GET("/authorize", OAuthAuthorize)

	// invalid client_id
	w := doJSON(r, http.MethodGet, "/authorize?client_id=nope", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	// success
	w2 := doJSON(r, http.MethodGet, "/authorize?client_id=cid&redirect_uri=https://cb.example.com/cb&response_type=code", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w2.Code, w2.Body.String())
	}
	// redirect mismatch
	w3 := doJSON(r, http.MethodGet, "/authorize?client_id=cid&redirect_uri=https://evil.com/cb&response_type=code", nil)
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("mismatch status = %d", w3.Code)
	}
	// unsupported response_type
	w4 := doJSON(r, http.MethodGet, "/authorize?client_id=cid&response_type=token", nil)
	if w4.Code != http.StatusBadRequest {
		t.Fatalf("response_type status = %d", w4.Code)
	}
}

func TestOAuthAuthorizeConfirmDeny(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "confirm", false)
	app := models.OAuthApp{ClientID: "cid2", ClientSecret: "csec", Name: "A", UserID: u.ID}
	db.Get().Create(&app)
	r := userRouter(u)
	r.POST("/confirm", OAuthAuthorizeConfirm)
	w := doJSON(r, http.MethodPost, "/confirm", map[string]any{
		"client_id": "cid2", "redirect_uri": "https://cb.com/cb", "deny": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !contains(w.Body.String(), "access_denied") {
		t.Fatalf("expected access_denied: %s", w.Body.String())
	}
}

func TestOAuthAuthorizeConfirmInvalidClient(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "confirm2", false)
	r := userRouter(u)
	r.POST("/confirm", OAuthAuthorizeConfirm)
	w := doJSON(r, http.MethodPost, "/confirm", map[string]any{"client_id": "nope"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestOAuthAuthorizeConfirmBadBody(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "confirm3", false)
	r := userRouter(u)
	r.POST("/confirm", OAuthAuthorizeConfirm)
	req := httptest.NewRequest(http.MethodPost, "/confirm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestOAuthAuthorizeConfirmApprove(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "confirm4", false)
	app := models.OAuthApp{ClientID: "cid3", ClientSecret: "csec", Name: "A", UserID: u.ID}
	db.Get().Create(&app)
	r := userRouter(u)
	r.POST("/confirm", OAuthAuthorizeConfirm)
	w := doJSON(r, http.MethodPost, "/confirm", map[string]any{
		"client_id": "cid3", "redirect_uri": "https://cb.com/cb",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "code") {
		t.Fatalf("expected code: %s", w.Body.String())
	}
}

func TestOAuthToken(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "token", false)
	app := models.OAuthApp{ClientID: "cid4", ClientSecret: "csec4", Name: "A", UserID: u.ID}
	db.Get().Create(&app)
	oc := models.OAuthCode{
		Code: "code1", AppID: app.ID, UserID: u.ID, Scope: "profile",
		RedirectURI: "https://cb.com/cb", ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	db.Get().Create(&oc)
	r := newTestRouter()
	r.POST("/token", OAuthToken)

	// unsupported grant type
	w := doJSON(r, http.MethodPost, "/token", map[string]any{"grant_type": "foo"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	// invalid client
	w2 := doJSON(r, http.MethodPost, "/token", map[string]any{
		"grant_type": "authorization_code", "client_id": "x", "client_secret": "y",
	})
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w2.Code)
	}
	// success
	w3 := doJSON(r, http.MethodPost, "/token", map[string]any{
		"grant_type": "authorization_code", "client_id": "cid4", "client_secret": "csec4",
		"code": "code1", "redirect_uri": "https://cb.com/cb",
	})
	if w3.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w3.Code, w3.Body.String())
	}
	if !contains(w3.Body.String(), "access_token") {
		t.Fatalf("no access_token: %s", w3.Body.String())
	}
	// code reuse
	w4 := doJSON(r, http.MethodPost, "/token", map[string]any{
		"grant_type": "authorization_code", "client_id": "cid4", "client_secret": "csec4",
		"code": "code1", "redirect_uri": "https://cb.com/cb",
	})
	if w4.Code != http.StatusBadRequest {
		t.Fatalf("reuse status = %d", w4.Code)
	}
}

func TestOAuthTokenCodeNotFound(t *testing.T) {
	testutil.SetupDB(t)
	app := models.OAuthApp{ClientID: "cid5", ClientSecret: "csec5", Name: "A"}
	db.Get().Create(&app)
	r := newTestRouter()
	r.POST("/token", OAuthToken)
	w := doJSON(r, http.MethodPost, "/token", map[string]any{
		"grant_type": "authorization_code", "client_id": "cid5", "client_secret": "csec5",
		"code": "noexist",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestOAuthTokenExpired(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "exp", false)
	app := models.OAuthApp{ClientID: "cid6", ClientSecret: "csec6", Name: "A", UserID: u.ID}
	db.Get().Create(&app)
	oc := models.OAuthCode{
		Code: "codeexp", AppID: app.ID, UserID: u.ID, Scope: "profile",
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	db.Get().Create(&oc)
	r := newTestRouter()
	r.POST("/token", OAuthToken)
	w := doJSON(r, http.MethodPost, "/token", map[string]any{
		"grant_type": "authorization_code", "client_id": "cid6", "client_secret": "csec6",
		"code": "codeexp",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestOAuthUserInfo(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "uinfo", false)
	r := userRouter(u)
	r.GET("/me", OAuthUserInfo)
	w := doJSON(r, http.MethodGet, "/me", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestOAuthUserInfoNoUser(t *testing.T) {
	testutil.SetupDB(t)
	r := newTestRouter()
	r.GET("/me", OAuthUserInfo)
	w := doJSON(r, http.MethodGet, "/me", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestBuildRedirect(t *testing.T) {
	cases := []struct {
		base, code, state, errCode, stateErr string
		want                                 string
	}{
		{"", "c", "s", "", "", ""},
		{"https://cb.com/cb", "c", "s", "", "", "https://cb.com/cb?code=c&state=s"},
		{"https://cb.com/cb?x=1", "c", "", "", "", "https://cb.com/cb?x=1&code=c"},
		{"https://cb.com/cb", "", "", "access_denied", "", "https://cb.com/cb?error=access_denied"},
	}
	for _, tc := range cases {
		got := buildRedirect(tc.base, tc.code, tc.state, tc.errCode, tc.stateErr)
		if got != tc.want {
			t.Fatalf("buildRedirect(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// ---- task.go ----

func TestListTasks(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "taskuser", false)
	db.Get().Create(&models.Task{OwnerID: u.ID, Type: "http", URL: "http://x", Status: 2})
	r := userRouter(u)
	r.GET("/tasks", ListTasks)
	w := doJSON(r, http.MethodGet, "/tasks", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListTasksAdmin(t *testing.T) {
	testutil.SetupDB(t)
	admin := makeUser(t, "taskadmin", true)
	other := makeUser(t, "other", false)
	db.Get().Create(&models.Task{OwnerID: other.ID, Type: "http", URL: "http://y", Status: 1})
	r := userRouter(admin)
	r.GET("/tasks", ListTasks)
	w := doJSON(r, http.MethodGet, "/tasks", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCancelTask(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "canceltask", false)
	tk := models.Task{OwnerID: u.ID, Type: "http", URL: "http://z", Status: 1}
	db.Get().Create(&tk)
	r := userRouter(u)
	r.POST("/tasks/:id/cancel", CancelTask)
	w := doJSON(r, http.MethodPost, "/tasks/"+itoa(tk.ID)+"/cancel", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got models.Task
	db.Get().First(&got, tk.ID)
	if got.Status != 3 {
		t.Fatalf("status not 3: %d", got.Status)
	}
}

func TestMimeTypeByExtExtra(t *testing.T) {
	if got := mimeTypeByExt(".mp3"); got != "audio/mpeg" {
		t.Fatalf("mp3 = %q", got)
	}
	if got := mimeTypeByExt(".tar"); got != "application/x-archive" {
		t.Fatalf("tar = %q", got)
	}
}

func TestListNotificationsBadPaging(t *testing.T) {
	testutil.SetupDB(t)
	u := makeUser(t, "notifpage", false)
	r := userRouter(u)
	r.GET("/notifications", ListNotifications)
	w := doJSON(r, http.MethodGet, "/notifications?page=0&size=0", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

// ---- helpers ----

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func itoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
