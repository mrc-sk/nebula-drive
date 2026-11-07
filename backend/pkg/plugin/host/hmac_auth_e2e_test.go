package host_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/host"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
)

// 这组测试跑通 HMAC 鉴权的**完整链路**：
//
//	host.Manager 下发密钥配置 → 拉起 hmac-auth 子进程 → 派发 onApiAuth
//	→ 插件校验签名 → 回写 principalUserId → 宿主拿到调用者身份
//
// # 为什么在测试里**再实现一遍**签名计算
//
// 下面的 signRequest 是"客户端参考实现"，与插件里的 canonicalString
// **刻意重复**。这不是冗余：插件的规范串一旦改动（多一个字段、换个分隔符），
// 参考实现不会跟着变，测试立刻红 —— 而这正是真实客户端会遇到的情况。
// 若从插件里导出函数来复用，这种破坏就永远测不出来。
//
// （插件不 import 宿主包、宿主测试也不 import 插件包，跨 module 本就无法互相引用。）

const (
	hmacSecret       = "e2e-shared-secret"
	hmacPrincipalUID = 42
	hmacSigHeader    = "X-Nebula-Signature"
	hmacTsHeader     = "X-Nebula-Timestamp"
)

// signRequest 客户端侧签名：METHOD\nPATH\nQUERY\nTIMESTAMP\nBODY_SHA256_HEX
func signRequest(secret, method, path, query, body string, ts int64) map[string]any {
	sum := sha256.Sum256([]byte(body))
	bodyHash := hex.EncodeToString(sum[:])

	canonical := method + "\n" + path + "\n" + query + "\n" +
		strconv.FormatInt(ts, 10) + "\n" + bodyHash

	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(canonical))

	return map[string]any{
		"method":           method,
		"path":             path,
		"query":            query,
		"ip":               "203.0.113.9",
		"ua":               "e2e-client",
		"hasAuthorization": true,
		"bodyHash":         "sha256:" + bodyHash,
		"bodyTruncated":    false,
		"headers": map[string]string{
			hmacSigHeader: hex.EncodeToString(m.Sum(nil)),
			hmacTsHeader:  strconv.FormatInt(ts, 10),
		},
	}
}

func loadHMACPlugin(t *testing.T) *host.Manager {
	t.Helper()
	const src = "../../../../examples/plugins/hmac-auth"
	if _, err := os.Stat(filepath.Join(src, plugin.ManifestFileName)); err != nil {
		t.Skipf("示例插件源码不存在: %v", err)
	}

	staged := t.TempDir()
	copyDirFiles(t, src, staged)
	entry := "hmac-auth"
	if runtime.GOOS == "windows" {
		entry += ".exe"
	}
	buildInto(t, staged, entry)

	ins := install.New(t.TempDir())
	res, err := ins.InstallFromDir(staged, "test")
	if err != nil {
		t.Fatalf("安装 hmac-auth 失败: %v", err)
	}
	man, err := install.ReadManifest(res.Dir)
	if err != nil {
		t.Fatalf("读取清单失败: %v", err)
	}

	config := fmt.Sprintf(
		`{"sharedSecret":%q,"principalUserId":%d,"signatureHeader":%q,"timestampHeader":%q,`+
			`"algorithm":"sha256","toleranceSeconds":300,"exemptPaths":["/api/v1/health"]}`,
		hmacSecret, hmacPrincipalUID, hmacSigHeader, hmacTsHeader)

	m := host.New(host.Config{
		PluginsDir:   ins.PluginsDir,
		DataDir:      t.TempDir(),
		HostVersion:  "test",
		StartTimeout: 30 * time.Second,
		FireTimeout:  10 * time.Second,
		Log:          t.Logf,
		ConfigFor:    func(string) json.RawMessage { return json.RawMessage(config) },
	})
	if err := m.Load(host.LoadSpec{Name: man.Name, Dir: res.Dir, Manifest: man}); err != nil {
		m.StopAll()
		t.Fatalf("加载 hmac-auth 失败: %v", err)
	}
	return m
}

// TestHMACAuthPlugin_EndToEnd 有效签名 → 插件回写 principalUserId。
func TestHMACAuthPlugin_EndToEnd(t *testing.T) {
	m := loadHMACPlugin(t)
	defer m.StopAll()

	ctx := signRequest(hmacSecret, "GET", "/api/v1/files", "a=1", "", time.Now().Unix())

	for _, e := range m.DispatchSync(plugin.HookApiAuth, ctx) {
		if plugin.IsBlockedMessage(e.Error()) {
			t.Fatalf("有效签名被判为拦截: %v（插件日志：%+v）", e, m.Logs("hmac-auth", 30))
		}
	}

	got, ok := asUint(ctx["principalUserId"])
	if !ok {
		t.Fatalf("插件未回写 principalUserId，宿主无法认定调用者；ctx=%v 日志：%+v",
			ctx, m.Logs("hmac-auth", 30))
	}
	if got != hmacPrincipalUID {
		t.Fatalf("principalUserId = %d, want %d", got, hmacPrincipalUID)
	}
	if fmt.Sprint(ctx["hmacAuth"]) != "granted" {
		t.Errorf("hmacAuth = %v, want granted", ctx["hmacAuth"])
	}
}

// TestHMACAuthPlugin_RejectsTamperedRequest 篡改路径 → 必须返回拦截错误。
//
// 宿主据此返回 403。这里验证的是"错误文本确实含 blocked" ——
// 不含的话宿主会把拒绝当成普通错误而放行（判定见 plugin.IsBlockedError）。
func TestHMACAuthPlugin_RejectsTamperedRequest(t *testing.T) {
	m := loadHMACPlugin(t)
	defer m.StopAll()

	ctx := signRequest(hmacSecret, "GET", "/api/v1/files", "", "", time.Now().Unix())
	// 签名用原路径算好，之后把路径改掉 —— 经典重放/越权构造
	ctx["path"] = "/api/v1/admin/users"

	blocked := false
	for _, e := range m.DispatchSync(plugin.HookApiAuth, ctx) {
		if plugin.IsBlockedMessage(e.Error()) {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("篡改路径未被拦截；ctx=%v 日志：%+v", ctx, m.Logs("hmac-auth", 30))
	}
	if ctx["principalUserId"] != nil {
		t.Errorf("被拒的请求不应带上身份: %v", ctx["principalUserId"])
	}
}

// TestHMACAuthPlugin_ExemptPathSkipsVerification 免签路径不带签名也应放行，
// 但**不得**因此认定调用者身份。
func TestHMACAuthPlugin_ExemptPathSkipsVerification(t *testing.T) {
	m := loadHMACPlugin(t)
	defer m.StopAll()

	ctx := map[string]any{
		"method": "GET", "path": "/api/v1/health", "query": "",
		"bodyHash": "sha256:" + "", "bodyTruncated": false,
		"headers": map[string]string{},
	}
	for _, e := range m.DispatchSync(plugin.HookApiAuth, ctx) {
		if plugin.IsBlockedMessage(e.Error()) {
			t.Fatalf("免签路径被拦截: %v", e)
		}
	}
	if ctx["principalUserId"] != nil {
		t.Errorf("免签路径不该认定身份（它只是跳过校验）: %v", ctx["principalUserId"])
	}
}

// TestHMACAuthPlugin_MissingSignatureIsBlocked 不带签名头 → 拦截。
//
// 若这里返回"没意见"，任何人都能靠不带签名头绕过这层保护。
func TestHMACAuthPlugin_MissingSignatureIsBlocked(t *testing.T) {
	m := loadHMACPlugin(t)
	defer m.StopAll()

	ctx := map[string]any{
		"method": "GET", "path": "/api/v1/files", "query": "",
		"bodyHash": "sha256:" + "", "bodyTruncated": false,
		"headers": map[string]string{},
	}
	blocked := false
	for _, e := range m.DispatchSync(plugin.HookApiAuth, ctx) {
		if plugin.IsBlockedMessage(e.Error()) {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("缺签名头时必须拦截（否则不带签名即可绕过）")
	}
}

// asUint 兼容 int/uint/float64：进程外插件的回写经 JSON 往返后是 float64。
func asUint(v any) (uint64, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 && n == float64(uint64(n)) {
			return uint64(n), true
		}
	case int:
		if n > 0 {
			return uint64(n), true
		}
	case uint:
		if n > 0 {
			return uint64(n), true
		}
	case int64:
		if n > 0 {
			return uint64(n), true
		}
	}
	return 0, false
}
