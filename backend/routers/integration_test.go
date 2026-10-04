package routers_test

// 全栈集成测试：启动 routers.Setup() 完整引擎（含全部中间件、CORS、InstallGuard、
// Auth、WebDAV、REST 路由），用 httptest 真实驱动 HTTP 请求，把已修复的 CODE_REVIEW
// P0 项锁成回归用例，CI 的 -race 能自动守回归。
//
// 与 controllers/*_test.go 的区别：那里用 newTestRouter() 把单个 handler 挂到空引擎、
// 并手动注入 ctx 用户，绕过了完整的认证/安装/权限中间件链；这里走的是生产完全一致
// 的请求路径。

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/controllers"
	"github.com/nebula-drive/nebula/internal/service"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/jwt"
	"github.com/nebula-drive/nebula/pkg/testutil"
	"github.com/nebula-drive/nebula/routers"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// testEnv 把一次集成测试所需的全部全局状态收拢在一起。
type testEnv struct {
	Engine    *gin.Engine
	DB        *gorm.DB
	UploadDir string
}

// bootstrapForTest 把引擎拉到"已安装、有默认策略"的生产等价状态。
//
// 关键点（曾反复踩坑）：
//   - 必须 conf.Save 把 installed 翻成 true，否则 InstallGuard 会把所有 /api 业务路由
//     挡成 503，根本进不到 handler。
//   - 必须 Seed 默认 Group(1/2) 与 Policy(1 local→UploadDir)，否则 WebDAV/上传找不到
//     存储策略直接 500。
//   - 必须 jwt.Init + 注入 service 的 SettingGetter/QuotaProvider，否则认证与配额走兜底。
func bootstrapForTest(t *testing.T) *testEnv {
	t.Helper()

	g := testutil.SetupDB(t)
	uploadDir := t.TempDir()

	// 标记已安装：写 conf.env + 翻转 installed。
	if err := conf.Save(&conf.Config{
		System: conf.SystemConfig{UploadPath: uploadDir, SiteName: "itest"},
	}); err != nil {
		t.Fatalf("conf.Save: %v", err)
	}

	// 默认用户组：1=default（开启 WebDAV、10GB 额度）；2=admin（无限额）。
	ensureGroup(g, models.Group{ID: 1, Name: "default", MaxStorage: 10 << 30, ShareEnabled: true, WebDAVEnabled: true})
	ensureGroup(g, models.Group{ID: 2, Name: "admin", MaxStorage: -1})
	// 默认本地存储策略，落盘到 uploadDir。
	ensurePolicy(g, models.Policy{
		ID: 1, Name: "本地存储", Type: "local",
		Config:    models.From(models.LocalPolicyConfig(uploadDir)),
		IsDefault: true,
	})

	// 品牌 / 上传安全设置（对齐生产 main.ensureBrandSettings）。
	brand := map[string]string{
		"brand.name":                        "NebulaDrive",
		"trash.retention_days":              "30",
		"file.max_versions":                 "10",
		"tls.mode":                          "off",
		"upload.allowed_extensions":         "jpg,jpeg,png,gif,webp,mp4,webm,mp3,wav,pdf,doc,docx,xls,xlsx,ppt,pptx,zip,rar,7z,tar,gz,txt,md,go,py,js,ts,json,yaml,yml,xml,csv",
		"upload.enable_magic_check":         "true",
		"security.password_min_length":      "8",
		"security.password_require_upper":   "true",
		"security.password_require_digit":   "true",
		"security.password_require_special": "false",
		"security.captcha_enabled":          "false",
		"security.allow_register":           "false",
	}
	for k, v := range brand {
		testutil.SeedSetting(k, v)
	}

	// 服务层注入（对齐 main.bootstrap）。
	service.SetSettingGetter(func(key string) (string, bool) {
		var s models.Setting
		if err := g.Where("`key` = ?", key).First(&s).Error; err != nil {
			return "", false
		}
		return s.Value, true
	})
	service.SetQuotaProvider(func(tx *gorm.DB, userID uint) (int64, string) {
		var u models.User
		if err := tx.Select("plan_id", "plan_expire_at", "group_id").First(&u, userID).Error; err != nil {
			return -1, "unknown"
		}
		if u.PlanID > 0 && u.PlanExpireAt != nil && u.PlanExpireAt.After(time.Now()) {
			var p models.Plan
			if tx.First(&p, u.PlanID).Error == nil {
				return p.MaxStorage, "plan:" + p.Name
			}
		}
		var grp models.Group
		if tx.First(&grp, u.GroupID).Error == nil {
			return grp.MaxStorage, "group:" + grp.Name
		}
		return -1, "fallback"
	})

	if err := jwt.Init(); err != nil {
		t.Fatalf("jwt.Init: %v", err)
	}
	controllers.SeedDefaultPlans()

	return &testEnv{Engine: routers.Setup(), DB: g, UploadDir: uploadDir}
}

func ensureGroup(g *gorm.DB, grp models.Group) {
	var existing models.Group
	if err := g.Where("id = ?", grp.ID).First(&existing).Error; err != nil {
		if err := g.Create(&grp).Error; err != nil {
			return
		}
	}
}

func ensurePolicy(g *gorm.DB, p models.Policy) {
	var existing models.Policy
	if err := g.Where("id = ?", p.ID).First(&existing).Error; err != nil {
		_ = g.Create(&p)
	}
}

// makeUser 创建带明文密码的持久化用户（Basic Auth 与二次确认密码都依赖它）。
func makeUser(t *testing.T, g *gorm.DB, name string, groupID uint, admin bool, password string) *models.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	u := &models.User{
		UserName: name, GroupID: groupID, IsAdmin: admin,
		Password: string(hash), Status: 0,
	}
	if err := g.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

// authCookie 为该用户签发一个有效的会话令牌，封装成 nebula_token cookie。
// 走的是与生产完全一致的 Auth 中间件路径：JWT 校验 + sessions 表有效性校验。
func authCookie(t *testing.T, u *models.User) *http.Cookie {
	t.Helper()
	sid := "sess-" + randHex(t, 10)
	if err := db.Get().Create(&models.Session{
		UserID:    u.ID,
		SessionID: sid,
		Token:     "tok-" + sid,
		ExpiresAt: time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	tok, err := jwt.Sign(u.ID, sid, time.Hour)
	if err != nil {
		t.Fatalf("jwt.Sign: %v", err)
	}
	return &http.Cookie{Name: "nebula_token", Value: tok, Path: "/"}
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	const hexc = "0123456789abcdef"
	out := make([]byte, 0, n*2)
	for _, c := range b {
		out = append(out, hexc[c>>4], hexc[c&0x0f])
	}
	return string(out)
}

// doReq 通用请求辅助：支持 JSON / 原始 body、cookie、自定义 header。
func doReq(t *testing.T, env *testEnv, method, path string, body []byte, contentType string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	env.Engine.ServeHTTP(w, req)
	return w
}

// doJSON 发送 JSON 请求（自动序列化 body）。
func doJSON(t *testing.T, env *testEnv, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	return doReq(t, env, method, path, raw, "application/json", cookies...)
}

// doReqWithHeader 发送带自定义 Header 的请求。
// 分享下载接受 query / Header 两种传密码方式（X-Share-Pwd、X-Share-Extract），
// Range 请求也要带 Header，两者都不能用只支持 Cookie 的 doReq 表达。
func doReqWithHeader(t *testing.T, env *testEnv, method, path string, _ []byte, _ string, hdr map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(nil))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	env.Engine.ServeHTTP(w, req)
	return w
}

// doDAV 发送带 Basic Auth 的 WebDAV 请求（WebDAV 用 HTTP Basic，非会话/JWT）。
// headers 为可选的可变参数，形如 [2]string{"Depth","1"}，用于覆盖默认请求头。
func doDAV(t *testing.T, env *testEnv, method, path, body, contentType, user, pass string, headers ...[2]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	w := httptest.NewRecorder()
	env.Engine.ServeHTTP(w, req)
	return w
}

// uploadFile 通过 /api/files/upload 上传一个文件（multipart）。
func uploadFile(t *testing.T, env *testEnv, cookie *http.Cookie, name, content string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	part.Write([]byte(content))
	mw.Close()
	return doReq(t, env, http.MethodPost, "/api/files/upload", buf.Bytes(), mw.FormDataContentType(), cookie)
}

// ---- 响应解析辅助 ----

type fileData struct {
	ID         uint   `json:"id"`
	OwnerID    uint   `json:"ownerId"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SourceName string `json:"sourceName"`
}

type uploadResp struct {
	Code int      `json:"code"`
	Data fileData `json:"data"`
}

func parseUpload(t *testing.T, w *httptest.ResponseRecorder) uploadResp {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("upload status = %d body=%s", w.Code, w.Body.String())
	}
	var r uploadResp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("unmarshal upload: %v body=%s", err, w.Body.String())
	}
	return r
}

func userStorage(t *testing.T, g *gorm.DB, userID uint) int64 {
	t.Helper()
	var u models.User
	if err := g.First(&u, userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return u.Storage
}

// findDiskFile 在 dir 下（递归）找到唯一一个文件，返回其完整路径。
func findDiskFile(t *testing.T, dir string) (string, bool) {
	t.Helper()
	var found string
	ok := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if ok {
			return filepath.SkipAll
		}
		found = p
		ok = true
		return nil
	})
	return found, ok
}

// ---------------------------------------------------------------------------
// 用例
// ---------------------------------------------------------------------------

// TestIntegration_HealthInstalled 连通性：完整引擎能起来，且已安装态正确。
func TestIntegration_HealthInstalled(t *testing.T) {
	env := bootstrapForTest(t)
	w := doReq(t, env, http.MethodGet, "/api/health", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("health status = %d", w.Code)
	}
	var r struct {
		Installed bool `json:"installed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.Installed {
		t.Fatal("expected installed=true after conf.Save")
	}
}

// TestIntegration_UnauthenticatedRejected 未登录：REST 与 WebDAV 都必须拒绝。
func TestIntegration_UnauthenticatedRejected(t *testing.T) {
	env := bootstrapForTest(t)

	w := doReq(t, env, http.MethodGet, "/api/files", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("REST unauth expected 401, got %d", w.Code)
	}

	// WebDAV 未带 Basic Auth → 401 + WWW-Authenticate
	w2 := doReq(t, env, "PROPFIND", "/dav/", nil, "")
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("WebDAV unauth expected 401, got %d", w2.Code)
	}
	if !strings.Contains(w2.Header().Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("missing WWW-Authenticate, headers=%v", w2.Header())
	}
}

// TestIntegration_WebDAVPathTraversal 回归 P0-1：路径穿越不能逃逸到磁盘 / 不能读服务器文件。
func TestIntegration_WebDAVPathTraversal(t *testing.T) {
	env := bootstrapForTest(t)
	u := makeUser(t, env.DB, "davtraverse", 1, false, "DavPass1")
	pass := "DavPass1"

	// 用穿越路径 PUT：/dav/../../../evil.txt
	// Handler 会 path.Clean 后落到用户根目录，作为名为 evil.txt 的 DB 记录与
	// 物理文件（dav-<ts>-evil.txt）落盘到 uploadDir —— 而非服务器的 /evil.txt。
	w := doDAV(t, env, "PUT", "/dav/../../../evil.txt", "traversal-body-content", "text/plain", u.UserName, pass)
	if w.Code != http.StatusCreated && w.Code != http.StatusNoContent {
		t.Fatalf("traversal PUT status = %d body=%s", w.Code, w.Body.String())
	}

	// 断言 1：穿越后文件确实落在用户根目录（parent_id 为空）。
	var cnt int64
	env.DB.Model(&models.File{}).Where("owner_id = ? AND name = ? AND parent_id IS NULL", u.ID, "evil.txt").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expected 1 root file named evil.txt, got %d", cnt)
	}

	// 断言 2：磁盘上不存在字面量 evil.txt —— 证明没有真实路径穿越写入。
	if _, err := os.Stat(filepath.Join(env.UploadDir, "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("LITERAL traversal write to %s must not exist", filepath.Join(env.UploadDir, "evil.txt"))
	}
	// 断言 3：内容被安全落盘在 uploadDir 内的某个 dav- 文件里。
	diskPath, ok := findDiskFile(t, env.UploadDir)
	if !ok {
		t.Fatal("expected a physical file under uploadDir")
	}
	data, err := os.ReadFile(diskPath)
	if err != nil || string(data) != "traversal-body-content" {
		t.Fatalf("physical content mismatch: err=%v data=%q", err, string(data))
	}

	// 断言 4：试图 GET 服务器文件（穿越到 /etc/passwd）必须 404，绝不返回服务器内容。
	w2 := doDAV(t, env, http.MethodGet, "/dav/../../../../etc/passwd", "", "", u.UserName, pass)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("GET traversal to /etc/passwd expected 404, got %d body=%s", w2.Code, w2.Body.String())
	}

	// 断言 5：根目录 PROPFIND（Depth:1）能列到 evil.txt（确认它确实在树里）。
	w3 := doDAV(t, env, "PROPFIND", "/dav/", "", "", u.UserName, pass, [2]string{"Depth", "1"})
	if w3.Code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND status = %d", w3.Code)
	}
	if !strings.Contains(w3.Body.String(), "evil.txt") {
		t.Fatalf("PROPFIND missing evil.txt: %s", w3.Body.String())
	}
}

// TestIntegration_WebDAVPutOverwrite 回归 P0-2：PUT 覆盖必须真实写入新数据，而不是伪成功丢数据。
func TestIntegration_WebDAVPutOverwrite(t *testing.T) {
	env := bootstrapForTest(t)
	u := makeUser(t, env.DB, "davoverwrite", 1, false, "DavPass2")
	pass := "DavPass2"

	// 首次 PUT → 201
	w1 := doDAV(t, env, "PUT", "/dav/note.txt", "v1-content", "text/plain", u.UserName, pass)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first PUT expected 201, got %d body=%s", w1.Code, w1.Body.String())
	}
	// 覆盖 PUT 同名文件 → 204
	w2 := doDAV(t, env, "PUT", "/dav/note.txt", "v2-content-longer", "text/plain", u.UserName, pass)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("overwrite PUT expected 204, got %d body=%s", w2.Code, w2.Body.String())
	}

	// GET 回来，内容必须是新数据 v2。
	w3 := doDAV(t, env, http.MethodGet, "/dav/note.txt", "", "", u.UserName, pass)
	if w3.Code != http.StatusOK {
		t.Fatalf("GET note.txt status = %d", w3.Code)
	}
	if w3.Body.String() != "v2-content-longer" {
		t.Fatalf("overwrite lost data: got %q", w3.Body.String())
	}

	// DB 记录 size 必须反映新内容长度。
	var f models.File
	env.DB.Where("owner_id = ? AND name = ?", u.ID, "note.txt").First(&f)
	if f.Size != int64(len("v2-content-longer")) {
		t.Fatalf("stored size = %d, want %d", f.Size, len("v2-content-longer"))
	}
}

// TestIntegration_SSRFRejected 回归 P0-6：出站地址必须拒绝内网 / 环回 / 链路本地 / 非 http(s)。
func TestIntegration_SSRFRejected(t *testing.T) {
	// 这些校验不依赖网络，仅验证"地址判定"这一关卡死内网。
	blocked := []string{
		"http://127.0.0.1/",
		"http://127.0.0.1:8080/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://[::1]/",
		"file:///etc/passwd",
		"gopher://127.0.0.1:6379/",
	}
	for _, url := range blocked {
		if _, err := service.ValidateOutboundURL(url); err == nil {
			t.Fatalf("expected SSRF block for %q, got none", url)
		}
	}

	// 公网 IP 字面量（1.1.1.1 不需要 DNS）应被放行，证明不是"一刀切拒绝所有"。
	if _, err := service.ValidateOutboundURL("http://1.1.1.1/"); err != nil {
		t.Fatalf("public IP literal should be allowed: %v", err)
	}

	// FetchBytes 端到端：内网地址在校验阶段即被拒，不会真正拨号。
	if _, err := service.FetchBytes("http://127.0.0.1:1/", 1024); err == nil {
		t.Fatal("FetchBytes must reject loopback address")
	}
}

// TestIntegration_PermissionMatrix 权限矩阵：用户 B 不能碰用户 A 的文件；越权一律 403；未登录 401。
func TestIntegration_PermissionMatrix(t *testing.T) {
	env := bootstrapForTest(t)
	admin := makeUser(t, env.DB, "padmin", 2, true, "AdminPwd1")
	userA := makeUser(t, env.DB, "pusera", 1, false, "UserAPwd1")
	userB := makeUser(t, env.DB, "puserb", 1, false, "UserBPwd1")
	cookieA := authCookie(t, userA)
	cookieB := authCookie(t, userB)
	cookieAdmin := authCookie(t, admin)

	// A 上传私密文件
	w := uploadFile(t, env, cookieA, "secret.txt", "top-secret-data")
	up := parseUpload(t, w)
	idA := up.Data.ID

	// B 下载 A 的文件 → 403（ownOrAdmin）
	w2 := doReq(t, env, http.MethodGet, "/api/files/"+itoa(idA)+"/download", nil, "", cookieB)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("B download A's file expected 403, got %d body=%s", w2.Code, w2.Body.String())
	}

	// B 删除 A 的文件（带二次确认密码）→ 403
	req := httptest.NewRequest(http.MethodDelete, "/api/files/"+itoa(idA), nil)
	req.AddCookie(cookieB)
	req.Header.Set("X-Confirm-Password", "UserBPwd1")
	w3b := httptest.NewRecorder()
	env.Engine.ServeHTTP(w3b, req)
	if w3b.Code != http.StatusForbidden {
		t.Fatalf("B delete A's file expected 403, got %d body=%s", w3b.Code, w3b.Body.String())
	}

	// B 列自己根目录，绝不能看到 A 的 secret.txt / idA
	w4 := doJSON(t, env, http.MethodGet, "/api/files?parent=", nil, cookieB)
	if w4.Code != http.StatusOK {
		t.Fatalf("B list status = %d", w4.Code)
	}
	var list struct {
		Data []fileData `json:"data"`
	}
	json.Unmarshal(w4.Body.Bytes(), &list)
	for _, f := range list.Data {
		if f.ID == idA || f.Name == "secret.txt" {
			t.Fatalf("B should not see A's file: %+v", f)
		}
	}

	// B 访问管理员接口 → 403
	w5 := doReq(t, env, http.MethodGet, "/api/admin/users", nil, "", cookieB)
	if w5.Code != http.StatusForbidden {
		t.Fatalf("B admin endpoint expected 403, got %d", w5.Code)
	}

	// 未登录访问管理员接口 → 401
	w6 := doReq(t, env, http.MethodGet, "/api/admin/users", nil, "")
	if w6.Code != http.StatusUnauthorized {
		t.Fatalf("unauth admin endpoint expected 401, got %d", w6.Code)
	}

	// 管理员访问 → 200（对照，证明链路本身没问题）
	w7 := doReq(t, env, http.MethodGet, "/api/admin/users", nil, "", cookieAdmin)
	if w7.Code != http.StatusOK {
		t.Fatalf("admin list users expected 200, got %d body=%s", w7.Code, w7.Body.String())
	}
}

// TestIntegration_FileLifecycle 文件生命周期：上传 → 复制 → 软删(回收站) → 彻底删除(清理物理+配额)。
func TestIntegration_FileLifecycle(t *testing.T) {
	env := bootstrapForTest(t)
	u := makeUser(t, env.DB, "lifecycle", 1, false, "LifePass1")
	cookie := authCookie(t, u)

	// 1) 上传 a.txt（11 字节）
	w := uploadFile(t, env, cookie, "a.txt", "hello-world")
	up := parseUpload(t, w)
	idA := up.Data.ID
	srcA := up.Data.SourceName
	if up.Data.Size != 11 {
		t.Fatalf("upload size = %d, want 11", up.Data.Size)
	}

	// 引用计数应为 1，且物理文件落盘。
	if refs := fileObjectRefs(t, env, srcA); refs != 1 {
		t.Fatalf("after upload refs = %d, want 1", refs)
	}
	if _, err := os.Stat(filepath.Join(env.UploadDir, srcA)); err != nil {
		t.Fatalf("physical file missing: %v", err)
	}
	storAfterUpload := userStorage(t, env.DB, u.ID)
	if storAfterUpload != 11 {
		t.Fatalf("storage after upload = %d, want 11", storAfterUpload)
	}

	// 2) 复制到 b.txt（独立物理副本）
	w2 := doJSON(t, env, http.MethodPost, "/api/files/batch-copy",
		map[string]any{"fileIds": []uint{idA}, "targetParentId": 0}, cookie)
	if w2.Code != http.StatusOK {
		t.Fatalf("batch-copy status = %d body=%s", w2.Code, w2.Body.String())
	}
	var fc int64
	env.DB.Model(&models.File{}).Where("owner_id = ? AND is_dir = ?", u.ID, false).Count(&fc)
	if fc != 2 {
		t.Fatalf("expected 2 files after copy, got %d", fc)
	}
	storAfterCopy := userStorage(t, env.DB, u.ID)
	if storAfterCopy != 22 {
		t.Fatalf("storage after copy = %d, want 22", storAfterCopy)
	}

	// 3) 软删除 a.txt（进回收站），需要二次确认密码
	req := httptest.NewRequest(http.MethodDelete, "/api/files/"+itoa(idA), nil)
	req.AddCookie(cookie)
	req.Header.Set("X-Confirm-Password", "LifePass1")
	w3 := httptest.NewRecorder()
	env.Engine.ServeHTTP(w3, req)
	if w3.Code != http.StatusOK {
		t.Fatalf("soft delete status = %d body=%s", w3.Code, w3.Body.String())
	}
	// 正常列表看不到，回收站能看到。
	if inNormal := fileVisible(t, env, cookie, idA); inNormal {
		t.Fatal("soft-deleted file should not appear in normal list")
	}
	if inTrash := fileVisibleInTrash(t, env, cookie, idA); !inTrash {
		t.Fatal("soft-deleted file should appear in trash list")
	}
	// 软删不释放配额 / 不删物理。
	if s := userStorage(t, env.DB, u.ID); s != 22 {
		t.Fatalf("storage after soft delete = %d, want 22 (not released yet)", s)
	}
	if _, err := os.Stat(filepath.Join(env.UploadDir, srcA)); err != nil {
		t.Fatalf("physical file should still exist after soft delete: %v", err)
	}

	// 4) 彻底删除（清理）：释放配额 + 删物理（refs→0）
	w4 := doReq(t, env, http.MethodPost, "/api/files/"+itoa(idA)+"/purge", nil, "", cookie)
	if w4.Code != http.StatusOK {
		t.Fatalf("purge status = %d body=%s", w4.Code, w4.Body.String())
	}
	if s := userStorage(t, env.DB, u.ID); s != 11 {
		t.Fatalf("storage after purge = %d, want 11 (released 11)", s)
	}
	// 回收站也不再能看到。
	if inTrash := fileVisibleInTrash(t, env, cookie, idA); inTrash {
		t.Fatal("purged file should not appear in trash")
	}
	// 物理文件被删除（a.txt 是独立引用，refs 1→0）。
	if _, err := os.Stat(filepath.Join(env.UploadDir, srcA)); !os.IsNotExist(err) {
		t.Fatalf("physical file should be deleted after purge: %v", err)
	}
	// 副本 b.txt 仍完好。
	if fc2 := countFiles(t, env, u.ID); fc2 != 1 {
		t.Fatalf("expected 1 remaining file (copy), got %d", fc2)
	}
}

// ---- 小工具 ----

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

func fileObjectRefs(t *testing.T, env *testEnv, src string) int64 {
	t.Helper()
	var obj models.FileObject
	if err := env.DB.Where("source_name = ?", src).First(&obj).Error; err != nil {
		return 0
	}
	return obj.Refs
}

func countFiles(t *testing.T, env *testEnv, userID uint) int {
	t.Helper()
	var c int64
	env.DB.Model(&models.File{}).Where("owner_id = ? AND is_dir = ? AND deleted_at IS NULL", userID, false).Count(&c)
	return int(c)
}

func fileVisible(t *testing.T, env *testEnv, cookie *http.Cookie, id uint) bool {
	t.Helper()
	w := doJSON(t, env, http.MethodGet, "/api/files?parent=", nil, cookie)
	var list struct {
		Data []fileData `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	for _, f := range list.Data {
		if f.ID == id {
			return true
		}
	}
	return false
}

func fileVisibleInTrash(t *testing.T, env *testEnv, cookie *http.Cookie, id uint) bool {
	t.Helper()
	w := doJSON(t, env, http.MethodGet, "/api/files?trash=1", nil, cookie)
	var list struct {
		Data []fileData `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	for _, f := range list.Data {
		if f.ID == id {
			return true
		}
	}
	return false
}

// TestIntegration_TaskRetryAndTrashEndpoints 锁死侧边栏三个入口所依赖的后端能力。
//
// 背景：侧边栏「分享列表 / 离线下载 / 回收站」三个入口的路由从未在前端注册，
// 本用例守着它们真正要用的那几个接口，避免再次出现「点了没反应」却无人察觉：
//   - POST /api/tasks/:id/retry（本轮新增；此前前端按钮打的是 404）
//   - GET  /api/files?trash=1
//   - POST /api/files/:id/restore
//   - POST /api/files/:id/purge
func TestIntegration_TaskRetryAndTrashEndpoints(t *testing.T) {
	env := bootstrapForTest(t)
	pass := "Str0ng-Pass-123!"
	owner := makeUser(t, env.DB, "retryer", 1, false, pass)
	other := makeUser(t, env.DB, "intruder", 1, false, pass)
	ownerCookie := authCookie(t, owner)
	otherCookie := authCookie(t, other)

	// ---- 1. 未认证访问全部被拒 ----
	for _, p := range []string{"/api/tasks", "/api/files?trash=1"} {
		if w := doReq(t, env, http.MethodGet, p, nil, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: got %d, want 401", p, w.Code)
		}
	}

	// ---- 2. ListTasks 只回自己的任务（后端按 owner_id 过滤） ----
	mkTask := func(ownerID uint, url string, status int) models.Task {
		tk := models.Task{OwnerID: ownerID, Type: "http", URL: url, Status: status}
		if err := env.DB.Create(&tk).Error; err != nil {
			t.Fatalf("create task: %v", err)
		}
		return tk
	}
	mine := mkTask(owner.ID, "https://example.com/a.bin", 3)        // 失败态，可重试
	theirs := mkTask(other.ID, "https://example.com/secret.bin", 2) // 别人的，不应出现在我的列表

	w := doJSON(t, env, http.MethodGet, "/api/tasks", nil, ownerCookie)
	if w.Code != http.StatusOK {
		t.Fatalf("list tasks: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Data []struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	seenMine, seenTheirs := false, false
	for _, r := range list.Data {
		if r.ID == mine.ID {
			seenMine = true
		}
		if r.ID == theirs.ID {
			seenTheirs = true
		}
	}
	if !seenMine {
		t.Fatalf("own task missing from list: %s", w.Body.String())
	}
	if seenTheirs {
		t.Fatal("list leaked another user's task — owner_id filter broken")
	}

	// ---- 3. Retry：他人任务不可重试（403） ----
	if w := doJSON(t, env, http.MethodPost, "/api/tasks/"+itoa(theirs.ID)+"/retry", nil, ownerCookie); w.Code != http.StatusForbidden {
		t.Fatalf("retry other's task: got %d, want 403", w.Code)
	}

	// ---- 4. Retry：进行中的任务不该被重试（400，避免同一 URL 重复下载） ----
	running := mkTask(owner.ID, "https://example.com/running.bin", 1)
	if w := doJSON(t, env, http.MethodPost, "/api/tasks/"+itoa(running.ID)+"/retry", nil, ownerCookie); w.Code != http.StatusBadRequest {
		t.Fatalf("retry running task: got %d, want 400", w.Code)
	}

	// ---- 5. Retry：失败态任务被重置为进行中、进度与错误清空 ----
	env.DB.Model(&models.Task{}).Where("id = ?", mine.ID).
		Updates(map[string]any{"progress": 42, "error": "boom"})
	if w := doJSON(t, env, http.MethodPost, "/api/tasks/"+itoa(mine.ID)+"/retry", nil, ownerCookie); w.Code != http.StatusOK {
		t.Fatalf("retry failed task: got %d %s", w.Code, w.Body.String())
	}
	var after models.Task
	env.DB.First(&after, mine.ID)
	if after.Status != 1 {
		t.Fatalf("after retry status = %d, want 1 (running)", after.Status)
	}
	if after.Progress != 0 || after.Error != "" {
		t.Fatalf("after retry progress=%d error=%q, want 0 and empty", after.Progress, after.Error)
	}

	// ---- 6. 回收站：软删 -> 出现在 trash -> 他人不可见 -> 还原 ----
	up := parseUpload(t, uploadFile(t, env, ownerCookie, "trash-me.txt", "bye"))
	upID := up.Data.ID

	// 删除接口要求 X-Confirm-Password（middleware.RequireConfirm），先确认护栏生效
	if w := doJSON(t, env, http.MethodDelete, "/api/files/"+itoa(upID), nil, ownerCookie); w.Code == http.StatusOK {
		t.Fatal("delete without X-Confirm-Password must be rejected")
	}

	// 直接改库置软删态，绕开需要确认头的删除接口
	env.DB.Model(&models.File{}).Where("id = ?", upID).
		Updates(map[string]any{"deleted_at": time.Now()})

	if !fileVisibleInTrash(t, env, ownerCookie, upID) {
		t.Fatal("soft-deleted file must show up in trash listing")
	}
	if fileVisibleInTrash(t, env, otherCookie, upID) {
		t.Fatal("trash listing leaked another user's file")
	}
	if fileVisible(t, env, ownerCookie, upID) {
		t.Fatal("soft-deleted file must NOT appear in normal file listing")
	}

	// 他人不能还原
	if w := doJSON(t, env, http.MethodPost, "/api/files/"+itoa(upID)+"/restore", nil, otherCookie); w.Code != http.StatusForbidden {
		t.Fatalf("restore other's file: got %d, want 403", w.Code)
	}

	// 还原
	if w := doJSON(t, env, http.MethodPost, "/api/files/"+itoa(upID)+"/restore", nil, ownerCookie); w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
	if fileVisibleInTrash(t, env, ownerCookie, upID) {
		t.Fatal("file still in trash after restore")
	}

	// ---- 7. Purge：他人文件不可彻底删除 ----
	if w := doJSON(t, env, http.MethodPost, "/api/files/"+itoa(upID)+"/purge", nil, otherCookie); w.Code != http.StatusForbidden {
		t.Fatalf("purge other's file: got %d, want 403", w.Code)
	}
}

// TestIntegration_ShareDownloadAndPreview 覆盖公开分享的下载/预览链路。
//
// 这组用例守的是三件容易被改坏的事：
//  1. 分享接收方通常没登录，所以这两个端点必须真的不需要 Bearer ——
//     挂了 Auth 就等于分享功能完全不可用；
//  2. fileId 是客户端传的，不校验祖先链就是越权读取任意文件的后门；
//  3. 预览必须 inline 且不计下载次数，下载必须 attachment 且计数。
func TestIntegration_ShareDownloadAndPreview(t *testing.T) {
	env := bootstrapForTest(t)
	pass := "Str0ng-Pass-123!"
	owner := makeUser(t, env.DB, "sharer", 1, false, pass)
	other := makeUser(t, env.DB, "bystander", 1, false, pass)
	ownerCookie := authCookie(t, owner)

	const body = "share download payload 中文内容"
	up := parseUpload(t, uploadFile(t, env, ownerCookie, "pub.txt", body))
	fileID := up.Data.ID

	// 建一个他人文件用于越权断言
	otherUp := parseUpload(t, uploadFile(t, env, authCookie(t, other), "secret.txt", "not yours"))
	secretID := otherUp.Data.ID

	mkShare := func(fileID, ownerID uint, pwd, extract string) models.Share {
		s := models.Share{FileID: fileID, OwnerID: ownerID, Password: pwd, ExtractCode: extract}
		if err := env.DB.Create(&s).Error; err != nil {
			t.Fatalf("create share: %v", err)
		}
		return s
	}
	shareCount := func(sid uint) int {
		var s models.Share
		env.DB.First(&s, sid)
		return s.Downloads
	}

	// ---- 1. 无密码的公开分享：未认证也能下载，且内容一致 ----
	pub := mkShare(fileID, owner.ID, "", "")
	w := doReq(t, env, http.MethodGet, "/api/shares/"+itoa(pub.ID)+"/download", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("public share download: %d %s", w.Code, w.Body.String())
	}
	if w.Body.String() != body {
		t.Fatalf("downloaded body = %q, want %q", w.Body.String(), body)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("download Content-Disposition = %q, want attachment prefix", got)
	}
	if got := shareCount(pub.ID); got != 1 {
		t.Fatalf("downloads after one download = %d, want 1", got)
	}

	// 第二次下载计数必须变成 2。这条专门守 UpdateColumn 绕过钩子后
	// 内存结构体不更新的老坑：用 s.Views+1 算出来会永远停在 1。
	w = doReq(t, env, http.MethodGet, "/api/shares/"+itoa(pub.ID)+"/download", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("second download: %d", w.Code)
	}
	if got := shareCount(pub.ID); got != 2 {
		t.Fatalf("downloads after two downloads = %d, want 2 (counter must actually increment)", got)
	}

	// ---- 2. 密码分享：错密码 401、对密码 200 ----
	locked := mkShare(fileID, owner.ID, "s3cret", "")
	if w := doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(locked.ID)+"/download?password=wrong", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d, want 401", w.Code)
	}
	w = doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(locked.ID)+"/download?password=s3cret", nil, "")
	if w.Code != http.StatusOK || w.Body.String() != body {
		t.Fatalf("correct password: %d body=%q", w.Code, w.Body.String())
	}

	// Header 形式也必须认：<video>/<audio> 之类用 query，
	// 但 fetch 走 header，两条路都得通。
	w = doReq(t, env, http.MethodGet, "/api/shares/"+itoa(locked.ID)+"/download", nil, "")
	w = doReqWithHeader(t, env, http.MethodGet, "/api/shares/"+itoa(locked.ID)+"/download", nil, "",
		map[string]string{"X-Share-Pwd": "s3cret"})
	if w.Code != http.StatusOK {
		t.Fatalf("password via header: got %d, want 200", w.Code)
	}

	// ---- 3. 提取码 ----
	extracted := mkShare(fileID, owner.ID, "", "AB12")
	if w := doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(extracted.ID)+"/download?extract=ZZZZ", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong extract: got %d, want 401", w.Code)
	}
	w = doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(extracted.ID)+"/download?extract=AB12", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("correct extract: got %d, want 200", w.Code)
	}

	// 密码与提取码同时存在时，缺一个都必须被拒
	both := mkShare(fileID, owner.ID, "pwd123", "CD34")
	if w := doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(both.ID)+"/download?password=pwd123", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing extract code: got %d, want 401", w.Code)
	}
	if w := doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(both.ID)+"/download?extract=CD34", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing password: got %d, want 401", w.Code)
	}

	// ---- 4. 越权：拿他人文件 ID 构造 fileId 必须 404 ----
	// 这是本组用例最要紧的一条：fileId 完全由客户端提供，
	// 不校验祖先链的话，任何人拿一个有效分享 ID 就能拖走别人的文件。
	if w := doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(pub.ID)+"/download?fileId="+itoa(secretID), nil, ""); w.Code != http.StatusNotFound {
		t.Fatalf("cross-user fileId: got %d, want 404", w.Code)
	}

	// ---- 5. 越权：分享目录外的同账号文件也不能通过 fileId 拿到 ----
	// 同属一个 owner 但不在分享子树内。只比 owner_id 是不够的。
	sibling := parseUpload(t, uploadFile(t, env, ownerCookie, "sibling.txt", "outside"))
	if w := doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(pub.ID)+"/download?fileId="+itoa(sibling.Data.ID), nil, ""); w.Code != http.StatusNotFound {
		t.Fatalf("sibling fileId: got %d, want 404 (must stay inside the share subtree)", w.Code)
	}

	// ---- 6. 预览：inline、不计下载次数、不自增 views ----
	before := shareCount(pub.ID)
	viewsBefore := func() int {
		var s models.Share
		env.DB.First(&s, pub.ID)
		return s.Views
	}()
	w = doReq(t, env, http.MethodGet, "/api/shares/"+itoa(pub.ID)+"/preview", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Fatalf("preview Content-Disposition = %q, want inline prefix", got)
	}
	if got := shareCount(pub.ID); got != before {
		t.Fatalf("preview changed downloads: %d -> %d", before, got)
	}
	if got := func() int {
		var s models.Share
		env.DB.First(&s, pub.ID)
		return s.Views
	}(); got != viewsBefore {
		t.Fatalf("preview changed views: %d -> %d (preview is not a page view)", viewsBefore, got)
	}

	// ---- 7. Range 支持（视频拖进度条依赖它） ----
	w = doReqWithHeader(t, env, http.MethodGet, "/api/shares/"+itoa(pub.ID)+"/download", nil, "",
		map[string]string{"Range": "bytes=0-3"})
	if w.Code != http.StatusPartialContent {
		t.Fatalf("range request: got %d, want 206", w.Code)
	}
	if w.Body.String() != body[:4] {
		t.Fatalf("range body = %q, want %q", w.Body.String(), body[:4])
	}

	// ---- 8. 目录分享不可直接下载 ----
	dir := models.File{OwnerID: owner.ID, Name: "folder", IsDir: true}
	if err := env.DB.Create(&dir).Error; err != nil {
		t.Fatalf("create dir: %v", err)
	}
	dirShare := mkShare(dir.ID, owner.ID, "", "")
	if w := doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(dirShare.ID)+"/download", nil, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("download dir share root: got %d, want 400", w.Code)
	}

	// ---- 9. 分享根被删后下载必须 404，而不是 panic 或返回空 ----
	env.DB.Model(&models.File{}).Where("id = ?", fileID).Delete(&models.File{})
	if w := doReq(t, env, http.MethodGet,
		"/api/shares/"+itoa(pub.ID)+"/download", nil, ""); w.Code != http.StatusNotFound {
		t.Fatalf("download share whose root is deleted: got %d, want 404", w.Code)
	}
}

// TestIntegration_ShareDetailViewsIncrements 把 views 字段的顺序 bug 锁死。
//
// 原实现是 UpdateColumn("views", s.Views+1) 之后直接读 s.Views 组 meta，
// 而 UpdateColumn 绕过钩子不会更新内存里的结构体 —— 于是第一次访问显示 0、
// 第二次仍显示 1，永远慢一拍。只查数据库是查不出这个问题的，
// 必须断言响应体里的 viewTimes。
func TestIntegration_ShareDetailViewsIncrements(t *testing.T) {
	env := bootstrapForTest(t)
	pass := "Str0ng-Pass-123!"
	owner := makeUser(t, env.DB, "counter", 1, false, pass)
	up := parseUpload(t, uploadFile(t, env, authCookie(t, owner), "v.txt", "x"))
	s := models.Share{FileID: up.Data.ID, OwnerID: owner.ID}
	if err := env.DB.Create(&s).Error; err != nil {
		t.Fatalf("create share: %v", err)
	}

	type shareDetail struct {
		Code int `json:"code"`
		Data struct {
			Meta struct {
				ViewTimes     int `json:"viewTimes"`
				DownloadTimes int `json:"downloadTimes"`
			} `json:"meta"`
		} `json:"data"`
	}
	for i := 1; i <= 3; i++ {
		w := doReq(t, env, http.MethodGet, "/api/shares/"+itoa(s.ID), nil, "")
		if w.Code != http.StatusOK {
			t.Fatalf("share detail #%d: %d %s", i, w.Code, w.Body.String())
		}
		var d shareDetail
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatalf("unmarshal share detail: %v", err)
		}
		if d.Data.Meta.ViewTimes != i {
			t.Fatalf("viewTimes on visit #%d = %d, want %d (response must reflect the increment)", i, d.Data.Meta.ViewTimes, i)
		}
	}
}
