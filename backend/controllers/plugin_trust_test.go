package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// 管理端信任库 API 的测试。
//
// 这组接口的价值在于把"建立信任"从"手工往数据目录放一个 JSON 文件"
// 变成可操作的动作，所以测试重点也在那些**只有真接口才会遇到**的问题：
// 私钥被误贴进来、信任库坏了要不要覆盖、策略改动对后续安装的影响。

func trustRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxUserKey, &models.User{ID: 1, UserName: "admin", IsAdmin: true})
		c.Next()
	})
	r.GET("/plugins/trust", GetPluginTrust)
	r.POST("/plugins/trust", AddPluginTrustKey)
	r.DELETE("/plugins/trust/:keyId", RemovePluginTrustKey)
	r.PUT("/plugins/trust/mode", SetPluginTrustMode)
	return r
}

func doTrust(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestTrustAddPublicKeyAndList 加公钥 → 列表能看到 → 库文件真的落盘。
func TestTrustAddPublicKeyAndList(t *testing.T) {
	testutil.SetupDB(t)
	r := trustRouter()

	key, err := pbclus.GenerateSigningKey("示例发布者")
	if err != nil {
		t.Fatal(err)
	}

	w := doTrust(t, r, http.MethodPost, "/plugins/trust", map[string]any{
		"publicKey": key.PublicKey, "name": "示例发布者",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("加公钥应成功: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			KeyID   string `json:"keyId"`
			Count   int    `json:"count"`
			Warning string `json:"warning"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.KeyID != key.KeyID {
		t.Errorf("keyId = %q, want %q", resp.Data.KeyID, key.KeyID)
	}
	// 给了公钥就不该有"只登记 keyId"的警告
	if resp.Data.Warning != "" {
		t.Errorf("给了公钥不该有安全警告，实际: %s", resp.Data.Warning)
	}

	// 库文件真的写到了数据目录
	path := install.TrustStorePath(conf.DataDir())
	ring, err := install.LoadTrustStore(path)
	if err != nil {
		t.Fatalf("信任库应可读: %v", err)
	}
	if n, ok := ring.Lookup(key.KeyID); !ok || n != "示例发布者" {
		t.Errorf("信任库里应有这条记录: %q %v", n, ok)
	}

	// 列表接口
	w = doTrust(t, r, http.MethodGet, "/plugins/trust", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表应成功: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), key.KeyID) {
		t.Errorf("列表应包含 keyId，实际: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"mode":"warn"`) {
		t.Errorf("默认策略应是 warn，实际: %s", w.Body.String())
	}
}

// TestTrustRejectsPrivateKey 私钥绝不能被贴进这个接口。
//
// 发布者的密钥文件里**同时含私钥与公钥**，而"把密钥文件发给我"
// 是极自然的说法。接口必须挡住它，并且要说清"你发的是私钥"。
func TestTrustRejectsPrivateKey(t *testing.T) {
	testutil.SetupDB(t)
	r := trustRouter()

	key, err := pbclus.GenerateSigningKey("某人")
	if err != nil {
		t.Fatal(err)
	}
	// 私钥是 64 字节 base64，长度与公钥不同
	w := doTrust(t, r, http.MethodPost, "/plugins/trust", map[string]any{
		"publicKey": key.PrivateKey, "name": "某人",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("贴私钥必须被拒绝，实际 %d: %s", w.Code, w.Body.String())
	}
	msg := w.Body.String()
	if !strings.Contains(msg, "私钥") {
		t.Errorf("错误信息必须指出这是私钥，实际: %s", msg)
	}
	if !strings.Contains(msg, "pub.json") {
		t.Errorf("错误信息应指出该用公钥文件，实际: %s", msg)
	}
	// 拒绝之后信任库不该被创建/修改
	ring, _ := install.LoadTrustStore(install.TrustStorePath(conf.DataDir()))
	if len(ring.Keys) != 0 {
		t.Errorf("被拒绝的请求不该写入信任库: %+v", ring.Keys)
	}
}

// TestTrustKeyIDOnlyWarns 只给 keyId 是允许的，但必须提示安全性较弱。
func TestTrustKeyIDOnlyWarns(t *testing.T) {
	testutil.SetupDB(t)
	r := trustRouter()
	key, err := pbclus.GenerateSigningKey("无公钥")
	if err != nil {
		t.Fatal(err)
	}
	w := doTrust(t, r, http.MethodPost, "/plugins/trust", map[string]any{
		"keyId": key.KeyID, "name": "无公钥",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("只给 keyId 应被接受: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "警告") && !strings.Contains(w.Body.String(), "warning") {
		t.Errorf("应给出安全提示，实际: %s", w.Body.String())
	}
	// 信任库里的记录不该含公钥
	ring, _ := install.LoadTrustStore(install.TrustStorePath(conf.DataDir()))
	if len(ring.Keys) != 1 || ring.Keys[0].PublicKey != "" {
		t.Errorf("只给 keyId 时不该凭空造出公钥: %+v", ring.Keys)
	}
}

// TestTrustDuplicateNeedsForce 重复添加要 409，带 force 才覆盖。
func TestTrustDuplicateNeedsForce(t *testing.T) {
	testutil.SetupDB(t)
	r := trustRouter()
	key, _ := pbclus.GenerateSigningKey("发布者")

	add := func(force bool, name string) *httptest.ResponseRecorder {
		return doTrust(t, r, http.MethodPost, "/plugins/trust", map[string]any{
			"publicKey": key.PublicKey, "name": name, "force": force,
		})
	}
	if w := add(false, "旧名字"); w.Code != http.StatusOK {
		t.Fatalf("首次添加应成功: %d", w.Code)
	}
	w := add(false, "新名字")
	if w.Code != http.StatusConflict {
		t.Fatalf("重复添加应 409，实际 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "force") {
		t.Errorf("应提示用 force，实际: %s", w.Body.String())
	}
	if w := add(true, "新名字"); w.Code != http.StatusOK {
		t.Fatalf("带 force 应成功: %d %s", w.Code, w.Body.String())
	}
	ring, _ := install.LoadTrustStore(install.TrustStorePath(conf.DataDir()))
	if len(ring.Keys) != 1 {
		t.Errorf("force 覆盖不该留下两条记录: %+v", ring.Keys)
	}
	if ring.Keys[0].Name != "新名字" {
		t.Errorf("应更新名字，实际 %q", ring.Keys[0].Name)
	}
}

// TestTrustRemove 移除信任。
func TestTrustRemove(t *testing.T) {
	testutil.SetupDB(t)
	r := trustRouter()
	key, _ := pbclus.GenerateSigningKey("要撤销的")

	doTrust(t, r, http.MethodPost, "/plugins/trust", map[string]any{
		"publicKey": key.PublicKey, "name": "要撤销的",
	})
	w := doTrust(t, r, http.MethodDelete, "/plugins/trust/"+key.KeyID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("移除应成功: %d %s", w.Code, w.Body.String())
	}
	// 响应要说明"已安装的插件不受影响"——否则管理员会以为撤销会自动回滚
	if !strings.Contains(w.Body.String(), "已安装") {
		t.Errorf("响应应说明对已安装插件的影响，实际: %s", w.Body.String())
	}
	// 再移除一次 → 404
	if w := doTrust(t, r, http.MethodDelete, "/plugins/trust/"+key.KeyID, nil); w.Code != http.StatusNotFound {
		t.Errorf("重复移除应 404，实际 %d", w.Code)
	}
}

// TestTrustBadFileIsNotSilentlyOverwritten 信任库文件损坏时不许静默重建。
//
// 静默重建会把管理员已经建立的信任关系一次抹掉，而且没有任何提示。
func TestTrustBadFileIsNotSilentlyOverwritten(t *testing.T) {
	testutil.SetupDB(t)
	r := trustRouter()
	key, _ := pbclus.GenerateSigningKey("x")

	path := install.TrustStorePath(conf.DataDir())
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	broken := []byte(`{"keys":[{"keyId":"tooshort","name":"bad"}]}`)
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}

	w := doTrust(t, r, http.MethodPost, "/plugins/trust", map[string]any{
		"publicKey": key.PublicKey, "name": "x",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("信任库损坏时应 409 而不是覆盖，实际 %d: %s", w.Code, w.Body.String())
	}
	// 文件必须保持原样（没被覆盖）
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, broken) {
		t.Error("损坏的信任库文件不该被覆盖")
	}
	// 列表接口要如实报错，而不是显示成"空库"
	w = doTrust(t, r, http.MethodGet, "/plugins/trust", nil)
	if !strings.Contains(w.Body.String(), "error") {
		t.Errorf("列表应如实报出信任库错误，实际: %s", w.Body.String())
	}
}

// TestTrustModeChange 策略切换与 require 的二次确认。
func TestTrustModeChange(t *testing.T) {
	testutil.SetupDB(t)
	// 建立一份已安装系统的配置：`SetPluginSignatureMode` 在配置未加载时
	// 会明确报错，而不是凭空造一份默认配置写盘（那会把已有配置覆盖掉）。
	// 生产环境里管理员能调到这个接口，说明系统已安装、配置必然已加载。
	if err := conf.Save(&conf.Config{
		System: conf.SystemConfig{SiteName: "trust-test", UploadPath: t.TempDir()},
	}); err != nil {
		t.Fatalf("conf.Save: %v", err)
	}
	r := trustRouter()

	// 非法值
	if w := doTrust(t, r, http.MethodPut, "/plugins/trust/mode", map[string]any{"mode": "Require!"}); w.Code != http.StatusBadRequest {
		t.Errorf("非法策略应 400，实际 %d", w.Code)
	}
	// off
	w := doTrust(t, r, http.MethodPut, "/plugins/trust/mode", map[string]any{"mode": "off"})
	if w.Code != http.StatusOK {
		t.Fatalf("切到 off 应成功: %d %s", w.Code, w.Body.String())
	}
	if conf.PluginSignatureMode() != "off" {
		t.Errorf("配置里应是 off，实际 %q", conf.PluginSignatureMode())
	}
	// 现场确认：安装器读到的策略也跟着变了（否则界面与实际会不一致）
	ins := newInstaller()
	if ins.SignatureMode != "off" {
		t.Errorf("新建的安装器应读到 off，实际 %q", ins.SignatureMode)
	}

	// 有存量无签名插件时，切 require 要先确认
	db.Get().Create(&models.Plugin{Name: "legacy", Title: "旧插件", Signature: ""})
	w = doTrust(t, r, http.MethodPut, "/plugins/trust/mode", map[string]any{"mode": "require"})
	if w.Code != http.StatusConflict {
		t.Fatalf("有存量无签名插件时切 require 应要求确认，实际 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "needConfirm") {
		t.Errorf("应带 needConfirm 标记，实际: %s", w.Body.String())
	}
	// 确认后再调
	w = doTrust(t, r, http.MethodPut, "/plugins/trust/mode", map[string]any{"mode": "require", "confirm": true})
	if w.Code != http.StatusOK {
		t.Fatalf("带 confirm 应成功（早期版本漏读这个字段，导致 require 永远设不上）: %d %s",
			w.Code, w.Body.String())
	}
	if conf.PluginSignatureMode() != "require" {
		t.Errorf("配置里应是 require，实际 %q", conf.PluginSignatureMode())
	}
}

// TestInstalledPluginKeepsSignatureSnapshot 安装时把签名状态冻结在库里。
//
// 包文件在安装后不保留，所以"这个插件当时有没有签名、是谁签的"
// 只能靠安装那一刻记下来。这条守住"记下来"这件事。
func TestInstalledPluginKeepsSignatureSnapshot(t *testing.T) {
	testutil.SetupDB(t)

	// 造一个签名包并安装（走真实安装器，不mock）
	key, err := pbclus.GenerateSigningKey("快照发布者")
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writePluginSrc(t, src, "snapdemo")
	pkg := filepath.Join(t.TempDir(), "snapdemo.pbclus")
	if _, err := pbclus.PackDir(pkg, src, pbclus.PackOptions{
		Universal: true, Tool: "test", SignKey: key,
	}); err != nil {
		t.Fatal(err)
	}

	ring := pbclus.NewEmptyKeyRing()
	ring.Add(pbclus.TrustedKey{KeyID: key.KeyID, Name: "快照发布者", PublicKey: key.PublicKey})
	ins := install.New(pluginsRootDir())
	ins.TrustStore = ring
	ins.Platform = pbclus.HostPlatform()
	res, err := ins.InstallFromPackage(pkg, "test")
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}
	if res.Signature == nil || !res.Signature.Trusted {
		t.Fatalf("受信任签名的包应报 Trusted: %+v", res.Signature)
	}

	// 模拟管理端的落库（与 InstallPlugin 里的逻辑一致）
	p := models.Plugin{Name: res.Name, Title: res.Manifest.Title, Enabled: false}
	raw, _ := json.Marshal(res.Signature)
	p.Signature = string(raw)
	p.Trusted = res.Signature.Trusted
	if err := db.Get().Create(&p).Error; err != nil {
		t.Fatal(err)
	}

	// 列表接口应能把快照解析出来（而不是让前端自己 JSON.parse）
	u := makeUser(t, "trustlist", true)
	lr := userRouter(u)
	lr.GET("/plugins", ListPlugins)
	w := doJSON(lr, http.MethodGet, "/plugins", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表失败: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"signatureInfo"`) {
		t.Fatalf("列表应带 signatureInfo 字段: %s", body)
	}
	if !strings.Contains(body, key.KeyID) {
		t.Errorf("列表里应能看到签名者 keyId: %s", body)
	}
	if !strings.Contains(body, `"trusted":true`) {
		t.Errorf("列表里应能看到受信任标记: %s", body)
	}
}

// writePluginSrc 造一个最小插件源码目录。
func writePluginSrc(t *testing.T, dir, name string) {
	t.Helper()
	manifest := `{
  "name": "` + name + `", "title": "快照测试", "version": "1.0.0",
  "author": "t", "description": "d", "entry": "` + name + `",
  "protocolVersion": 1, "hooks": ["onAntiLeech"]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("payload"), 0o750); err != nil {
		t.Fatal(err)
	}
}
