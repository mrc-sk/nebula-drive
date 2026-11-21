package controllers

// CDN 能力的回归测试。重点是安全边界：签名正确、篡改拒绝、
// 过期拒绝、未配置时 fail closed、回源不再二次跳转。

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

const testCDNSecret = "0123456789abcdef-this-is-a-test-signing-secret"

func setupCDN(t *testing.T, enabled bool, domain, secret string) {
	t.Helper()
	testutil.SetupDB(t)
	if enabled {
		testutil.SeedSetting("cdn.enabled", "true")
	} else {
		testutil.SeedSetting("cdn.enabled", "false")
	}
	testutil.SeedSetting("cdn.domain", domain)
	testutil.SeedSetting("cdn.secret", secret)
}

func TestCdnSecret(t *testing.T) {
	testutil.SetupDB(t)
	// 太短的密钥视为未配置
	testutil.SeedSetting("cdn.secret", "short")
	if got := cdnSecret(); got != "" {
		t.Errorf("短密钥应被拒绝, got %q", got)
	}
	testutil.SeedSetting("cdn.secret", testCDNSecret)
	if got := cdnSecret(); got != testCDNSecret {
		t.Errorf("密钥读取错误: %q", got)
	}
}

func TestCdnEnabled(t *testing.T) {
	cases := []struct {
		name    string
		enabled string
		domain  string
		secret  string
		want    bool
	}{
		{"全部合法", "true", "https://cdn.example.com", testCDNSecret, true},
		{"开关关闭", "false", "https://cdn.example.com", testCDNSecret, false},
		{"无域名", "true", "", testCDNSecret, false},
		{"无密钥", "true", "https://cdn.example.com", "", false},
		{"密钥太短", "true", "https://cdn.example.com", "123", false},
		{"非法scheme", "true", "javascript:alert(1)", testCDNSecret, false},
		{"无host", "true", "https://", testCDNSecret, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.SetupDB(t)
			testutil.SeedSetting("cdn.enabled", c.enabled)
			testutil.SeedSetting("cdn.domain", c.domain)
			testutil.SeedSetting("cdn.secret", c.secret)
			got, _ := cdnEnabled()
			if got != c.want {
				t.Errorf("cdnEnabled = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCdnSignAndURL(t *testing.T) {
	// 同一输入签名稳定；改任一字段签名都变。
	sig1 := cdnSign(testCDNSecret, 42, 1000, "inline")
	sig2 := cdnSign(testCDNSecret, 42, 1000, "inline")
	if sig1 != sig2 {
		t.Error("签名应稳定")
	}
	if len(sig1) != 64 {
		t.Errorf("签名应为 64 位十六进制, got %d", len(sig1))
	}
	if cdnSign(testCDNSecret, 43, 1000, "inline") == sig1 {
		t.Error("fileID 变化应改变签名")
	}
	if cdnSign(testCDNSecret, 42, 1001, "inline") == sig1 {
		t.Error("exp 变化应改变签名")
	}
	if cdnSign(testCDNSecret, 42, 1000, "attachment") == sig1 {
		t.Error("disp 变化应改变签名")
	}
	if cdnSign("other-secret-0123456789abcdef", 42, 1000, "inline") == sig1 {
		t.Error("密钥变化应改变签名")
	}

	url := cdnURL("https://cdn.example.com/", testCDNSecret, 42, "attachment")
	if !strings.HasPrefix(url, "https://cdn.example.com/cdn/files/42?") {
		t.Errorf("URL 格式错误: %s", url)
	}
	if !strings.Contains(url, "disp=attachment") || !strings.Contains(url, "sig=") || !strings.Contains(url, "exp=") {
		t.Errorf("URL 缺参数: %s", url)
	}
}

func TestMaybeCDNRedirect(t *testing.T) {
	setupCDN(t, true, "https://cdn.example.com", testCDNSecret)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	got := maybeCDNRedirect(c, testFile(42), "attachment")
	if !got {
		t.Fatal("CDN 启用时应跳转")
	}
	// gin.CreateTestContext 下 c.Status 只更新 writer.Status，不触发底层 recorder 的
	// WriteHeader，所以要断言 c.Writer.Status() 而非 w.Code。
	if c.Writer.Status() != http.StatusFound {
		t.Errorf("状态码 = %d, want 302", c.Writer.Status())
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("302 应 no-store, got %q", w.Header().Get("Cache-Control"))
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://cdn.example.com/cdn/files/42?") {
		t.Errorf("Location = %q", loc)
	}

	// 回源请求不能再跳（防循环）
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Set("cdn_origin", true)
	if maybeCDNRedirect(c2, testFile(42), "attachment") {
		t.Error("回源请求不应再跳 CDN")
	}
}

func TestCDNOriginFailClosed(t *testing.T) {
	// 未启用 CDN 时回源端点必须 404，不能成为公开文件通道。
	setupCDN(t, false, "https://cdn.example.com", testCDNSecret)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	CDNOrigin(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("未启用 CDN 时回源应 404, got %d", w.Code)
	}

	// 启用后：过期签名拒绝
	setupCDN(t, true, "https://cdn.example.com", testCDNSecret)
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Params = gin.Params{{Key: "id", Value: "42"}}
	c2.Request = httptest.NewRequest("GET", "/cdn/files/42?exp=1000&disp=inline&sig=bad", nil)
	CDNOrigin(c2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("过期链接应 403, got %d", w2.Code)
	}

	// 未来时间太远也拒绝
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Params = gin.Params{{Key: "id", Value: "42"}}
	future := time.Now().Add(time.Hour).Unix()
	c3.Request = httptest.NewRequest("GET", "/cdn/files/42?exp="+strconv.FormatInt(future, 10)+"&disp=inline&sig=bad", nil)
	CDNOrigin(c3)
	if w3.Code != http.StatusForbidden {
		t.Errorf("远期 exp 应 403, got %d", w3.Code)
	}

	// 篡改签名拒绝（exp 在有效窗内）
	w4 := httptest.NewRecorder()
	c4, _ := gin.CreateTestContext(w4)
	c4.Params = gin.Params{{Key: "id", Value: "42"}}
	now := time.Now().Add(time.Minute).Unix()
	badSig := cdnSign("wrong-secret-0123456789", 42, now, "inline")
	c4.Request = httptest.NewRequest("GET", "/cdn/files/42?exp="+strconv.FormatInt(now, 10)+"&disp=inline&sig="+badSig, nil)
	CDNOrigin(c4)
	if w4.Code != http.StatusForbidden {
		t.Errorf("错误签名应 403, got %d", w4.Code)
	}
}

// testFile 只取签名需要的 ID，不必构造完整模型。
func testFile(id uint) *models.File {
	return &models.File{ID: id}
}
