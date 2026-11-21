package webdav

// WebDAV 包的回归测试。该包此前零测试覆盖，而它承载了 CODE_REVIEW 里
// 多个 P0 修复（LOCK 真实化、路径处理、方法白名单）。这里锁定那些
// 不依赖文件系统的纯逻辑与配置驱动的判定，防止修复被后续改动悄悄回退。

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestDavTime(t *testing.T) {
	// RFC1123Z 且强制 UTC —— WebDAV 客户端对时间格式很挑剔。
	loc, _ := time.LoadLocation("Asia/Shanghai")
	tm := time.Date(2026, 11, 21, 9, 30, 0, 0, loc)
	got := davTime(tm)
	want := "Sat, 21 Nov 2026 01:30:00 +0000"
	if got != want {
		t.Errorf("davTime = %q, want %q", got, want)
	}
}

func TestEtag(t *testing.T) {
	updated := time.Unix(1700000000, 0)
	f := &models.File{ID: 42, Size: 1234, UpdatedAt: updated, SourceName: "obj-abc"}
	h := md5.Sum([]byte(fmt.Sprintf("%d-%d-%d-%s", 42, 1234, updated.Unix(), "obj-abc")))
	want := `"` + hex.EncodeToString(h[:]) + `"`
	if got := etag(f); got != want {
		t.Errorf("etag = %q, want %q", got, want)
	}
	// ETag 必须是强引号包裹的十六进制，且对内容变化敏感。
	if !strings.HasPrefix(etag(f), `"`) || !strings.HasSuffix(etag(f), `"`) {
		t.Errorf("etag 未被引号包裹: %q", etag(f))
	}
	f2 := *f
	f2.Size = 9999
	if etag(&f2) == etag(f) {
		t.Error("size 变化后 ETag 应改变")
	}
}

func TestXmlEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain`, `plain`},
		{`a&b`, `a&amp;b`},
		{`<tag>`, `&lt;tag&gt;`},
		{`"quoted"`, `&quot;quoted&quot;`},
		{`&<>"`, `&amp;&lt;&gt;&quot;`},
	}
	for _, c := range cases {
		if got := xmlEscape(c.in); got != c.want {
			t.Errorf("xmlEscape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMimeTypeFromExt(t *testing.T) {
	cases := map[string]string{
		".zip":  "application/x-archive",
		".tar":  "application/x-archive",
		".txt":  "text/plain",
		".pdf":  "application/pdf",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".mp4":  "video/mp4",
		".mp3":  "audio/mpeg",
		".xyz":  "application/octet-stream",
		"":      "application/octet-stream",
	}
	for ext, want := range cases {
		if got := mimeTypeFromExt(ext); got != want {
			t.Errorf("mimeTypeFromExt(%q) = %q, want %q", ext, got, want)
		}
	}
}

func TestHrefOf(t *testing.T) {
	// 文件名含空格与中文时必须做百分号编码，且始终以 / 开头。
	// 注意 url.PathEscape 不编码 & （它在 path 段里是合法字符）。
	if got := hrefOf(models.File{Name: "报告 & 附表.docx"}); got != "/%E6%8A%A5%E5%91%8A%20&%20%E9%99%84%E8%A1%A8.docx" {
		t.Errorf("hrefOf = %q", got)
	}
	if got := hrefOf(models.File{Name: "simple.txt"}); got != "/simple.txt" {
		t.Errorf("hrefOf simple = %q", got)
	}
	if !strings.HasPrefix(hrefOf(models.File{Name: "a/b"}), "/") {
		t.Error("hrefOf 必须以 / 开头")
	}
}

func TestParentOf(t *testing.T) {
	if got := parentOf(nil); got != nil {
		t.Errorf("parentOf(nil) = %v, want nil", got)
	}
	pid := uint(7)
	file := &models.File{ID: 10, IsDir: false, ParentID: &pid}
	if got := parentOf(file); got == nil || *got != pid {
		t.Fatalf("文件应返回其 ParentID, got %v", got)
	}
	dir := &models.File{ID: 10, IsDir: true, ParentID: &pid}
	got := parentOf(dir)
	if got == nil || *got != dir.ID {
		t.Fatalf("目录应返回自身 ID 作为父, got %v", got)
	}
}

// TestMethodAllowed 验证方法白名单：默认集合、OPTIONS 永远允许、
// 自定义配置生效以及大小写不敏感。
func TestMethodAllowed(t *testing.T) {
	testutil.SetupDB(t)

	// 默认的 12 个方法
	for _, m := range defaultWebDAVMethods {
		if !methodAllowed(m) {
			t.Errorf("默认方法 %s 应被允许", m)
		}
		if !methodAllowed(strings.ToLower(m)) {
			t.Errorf("方法判定应大小写不敏感: %s", m)
		}
	}
	if !methodAllowed("OPTIONS") {
		t.Error("OPTIONS 应永远允许")
	}
	if methodAllowed("TRACE") {
		t.Error("TRACE 不在默认白名单，应拒绝")
	}

	// 自定义配置：只允许 GET，其它方法一律拒绝
	testutil.SeedSetting("webdav.methods", `["GET"]`)
	if !methodAllowed("GET") {
		t.Error("自定义白名单里 GET 应允许")
	}
	if methodAllowed("PUT") {
		t.Error("自定义白名单不含 PUT，应拒绝")
	}
	if !methodAllowed("OPTIONS") {
		t.Error("OPTIONS 不受白名单约束")
	}
}

func TestWebdavPathPrefix(t *testing.T) {
	testutil.SetupDB(t)
	if got := webdavPathPrefix(); got != "/dav" {
		t.Errorf("默认前缀 = %q, want /dav", got)
	}
	testutil.SeedSetting("webdav.path", "/webdav")
	if got := webdavPathPrefix(); got != "/webdav" {
		t.Errorf("自定义前缀 = %q, want /webdav", got)
	}
}

func TestStripDAVPrefix(t *testing.T) {
	testutil.SetupDB(t) // webdavPathPrefix 读 db
	cases := []struct{ in, want string }{
		{"/dav/foo/bar", "/foo/bar"},
		{"/dav", "/"},
		{"", "/"},
		{"foo", "/foo"},
		{"http://example.com/dav/a.txt", "/a.txt"},
		{"http://example.com", "/"},
	}
	for _, c := range cases {
		if got := stripDAVPrefix(c.in); got != c.want {
			t.Errorf("stripDAVPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDavCheckLock 验证锁的核心语义：他人持锁时拒绝写入(423)、
// 自己持锁放行、过期锁被清理。直接操作 davLocks 全局 map 以隔离 hooks。
func TestDavCheckLock(t *testing.T) {
	// 清空全局锁表，测试结束恢复，避免相互干扰。
	davLocksMu.Lock()
	old := davLocks
	davLocks = map[string]*davLockEntry{}
	davLocksMu.Unlock()
	t.Cleanup(func() {
		davLocksMu.Lock()
		davLocks = old
		davLocksMu.Unlock()
	})

	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		return c, w
	}
	me := &models.User{ID: 1}
	other := &models.User{ID: 2}

	// 他人持有未过期锁 -> 拒绝
	davLocks["tok1"] = &davLockEntry{Token: "tok1", Path: "/a.txt", OwnerID: other.ID, Expires: time.Now().Add(time.Hour)}
	c, w := newCtx()
	if davCheckLock(c, me, "/a.txt") {
		t.Error("他人持锁时应拒绝写入")
	}
	if w.Code != 423 {
		t.Errorf("状态码 = %d, want 423", w.Code)
	}

	// 自己持锁 -> 放行
	c, _ = newCtx()
	if !davCheckLock(c, other, "/a.txt") {
		t.Error("自己持锁应放行")
	}

	// 不同路径不受影响
	c, _ = newCtx()
	if !davCheckLock(c, me, "/b.txt") {
		t.Error("其它路径不应被锁")
	}

	// 过期锁应被清理且不再阻挡
	davLocks["tok2"] = &davLockEntry{Token: "tok2", Path: "/c.txt", OwnerID: other.ID, Expires: time.Now().Add(-time.Hour)}
	c, _ = newCtx()
	if !davCheckLock(c, me, "/c.txt") {
		t.Error("过期锁不应阻挡写入")
	}
	davLocksMu.Lock()
	_, stillThere := davLocks["tok2"]
	davLocksMu.Unlock()
	if stillThere {
		t.Error("过期锁应被清理出 map")
	}
}

// TestResponseFromFile 验证 PROPFIND 响应结构：目录有 ResourceType，
// 文件有 ContentLength/ETag，两者都有 DisplayName 和 LastModified。
func TestResponseFromFile(t *testing.T) {
	updated := time.Now()
	dir := models.File{ID: 1, Name: "docs", IsDir: true, UpdatedAt: updated}
	r := respFromFile(dir)
	if r.Propstat.Prop.ResourceType == nil || r.Propstat.Prop.ResourceType.Collection == nil {
		t.Error("目录响应应含 Collection ResourceType")
	}
	if r.Propstat.Prop.GetETag != "" {
		t.Error("目录不应有 ETag")
	}
	if r.Propstat.Prop.DisplayName != "docs" {
		t.Errorf("DisplayName = %q", r.Propstat.Prop.DisplayName)
	}

	file := models.File{ID: 2, Name: "a.txt", Size: 10, MimeType: "text/plain", UpdatedAt: updated}
	r2 := respFromFile(file)
	if r2.Propstat.Prop.ResourceType != nil {
		t.Error("文件不应有 ResourceType")
	}
	if r2.Propstat.Prop.GetContentLength != 10 {
		t.Errorf("ContentLength = %d", r2.Propstat.Prop.GetContentLength)
	}
	if r2.Propstat.Prop.GetContentType != "text/plain" {
		t.Errorf("ContentType = %q", r2.Propstat.Prop.GetContentType)
	}
	if r2.Propstat.Prop.GetETag == "" {
		t.Error("文件应有 ETag")
	}
}
