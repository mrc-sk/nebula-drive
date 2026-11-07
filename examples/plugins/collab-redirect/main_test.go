package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// 这些测试刻意使用 float64 数字：ctx 在宿主与插件之间走了一趟 JSON，
// userId/fileId 到达插件时一定是 float64。用 int 写测试会掩盖类型 bug。

func openCtx(fileName, mimeType string) map[string]any {
	return map[string]any{
		"userId":   float64(7),
		"userName": "张三",
		"fileId":   float64(42),
		"fileName": fileName,
		"mimeType": mimeType,
		"ip":       "203.0.113.9",
	}
}

const openXMLDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// target 取出 ctxPatch.url；不存在就立刻失败。
func target(t *testing.T, res fireResult) string {
	t.Helper()
	raw, ok := res.CtxPatch["url"].(string)
	if !ok || raw == "" {
		t.Fatalf("期望 ctxPatch.url 为非空字符串，实际 ctxPatch=%#v", res.CtxPatch)
	}
	return raw
}

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("返回的 url 无法解析: %v（raw=%q）", err, raw)
	}
	return u
}

func mustTargetURL(t *testing.T, res fireResult) *url.URL {
	t.Helper()
	return parseURL(t, target(t, res))
}

// requireNoURL 是本插件最重要的一条断言：不命中时 ctxPatch 里不能有 url。
//
// 注意不能只判断 res.CtxPatch["url"] != ""：map 里根本没有这个键时，
// 索引会返回零值，那种写法会**永远通过**，等于没测。
func requireNoURL(t *testing.T, res fireResult) {
	t.Helper()
	if res.CtxPatch != nil {
		if v, ok := res.CtxPatch["url"]; ok {
			t.Fatalf("不该跳转时却写入了 ctxPatch.url=%v", v)
		}
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("序列化 fireResult 失败: %v", err)
	}
	if strings.Contains(string(b), `"url"`) {
		t.Fatalf("不该跳转时序列化结果仍含 url 键: %s", b)
	}
}

func TestExtensionMatchAndMiss(t *testing.T) {
	p := newPlugin(Config{
		EditorURL: "https://office.example.com/edit?fileId={{fileId}}",
	})

	t.Run("命中扩展名", func(t *testing.T) {
		for _, name := range []string{"报告.docx", "REPORT.DOCX", "sheet.xlsx", "deck.ppt"} {
			res := p.collabOpen(openCtx(name, "application/octet-stream"))
			if got := mustTargetURL(t, res).Query().Get("fileId"); got != "42" {
				t.Errorf("%s: fileId=%q，期望 42", name, got)
			}
		}
	})

	t.Run("未命中扩展名不写 url", func(t *testing.T) {
		for _, name := range []string{"notes.md", "readme.txt", "photo.png", "无扩展名"} {
			res := p.collabOpen(openCtx(name, "text/plain"))
			requireNoURL(t, res)
		}
	})
}

func TestMimePrefixMatch(t *testing.T) {
	p := newPlugin(Config{
		EditorURL: "https://office.example.com/edit?fileId={{fileId}}",
	})

	// 扩展名 .bin 不在默认列表里，只能靠 MIME 命中。
	res := p.collabOpen(openCtx("scan.bin", openXMLDocx))
	target(t, res)

	// MIME 大小写不敏感（RFC 2045 的 type/subtype 本身就不区分大小写）。
	res = p.collabOpen(openCtx("scan.bin", "APPLICATION/VND.OASIS.OPENDOCUMENT.TEXT"))
	target(t, res)

	// 不匹配的 MIME 且扩展名也不匹配 → 不写 url。
	res = p.collabOpen(openCtx("scan.bin", "application/pdf"))
	requireNoURL(t, res)
}

func TestPlaceholderReplacement(t *testing.T) {
	p := newPlugin(Config{
		EditorURL: "https://office.example.com/edit" +
			"?fileId={{fileId}}&name={{fileName}}&user={{userName}}" +
			"&uid={{userId}}&mime={{mimeType}}&ip={{ip}}",
	})
	ctx := openCtx("报告.docx", openXMLDocx)
	q := mustTargetURL(t, p.collabOpen(ctx)).Query()

	want := map[string]string{
		"fileId": "42",
		"name":   "报告.docx",
		"user":   "张三",
		"uid":    "7",
		"mime":   openXMLDocx,
		"ip":     "203.0.113.9",
	}
	for key, w := range want {
		if got := q.Get(key); got != w {
			t.Errorf("参数 %s = %q，期望 %q", key, got, w)
		}
	}
}

// TestEscaping 是本插件安全性的核心用例：
// 文件名里的 & 会拆参数、# 会截断查询串、空格与中文必须编码。
// 断言方式是用 url.Parse 解回参数值，等于模拟外部编辑器真实收到的值。
func TestEscaping(t *testing.T) {
	name := "报告 & 附表 #1 100%.docx"
	p := newPlugin(Config{
		EditorURL: "https://office.example.com/edit?fileId={{fileId}}&name={{fileName}}",
	})

	u := mustTargetURL(t, p.collabOpen(openCtx(name, "application/octet-stream")))
	q := u.Query()

	if got := q.Get("name"); got != name {
		t.Fatalf("name = %q，期望原始文件名 %q", got, name)
	}
	if got := q.Get("fileId"); got != "42" {
		t.Fatalf("fileId = %q，期望 42", got)
	}
	// 若 & 未被转义，Query() 会多出参数（如 " 附表 #1 100%.docx"）。
	if len(q) != 2 {
		t.Fatalf("查询参数个数 = %d，期望 2；说明文件名破坏了查询串: %v", len(q), q)
	}
	// # 未被转义时浏览器会把后半段当 fragment，服务端根本收不到。
	if u.Fragment != "" {
		t.Fatalf("fragment = %q，期望为空（# 应被转义为 %%23）", u.Fragment)
	}
}

func TestUnknownPlaceholderPreserved(t *testing.T) {
	p := newPlugin(Config{
		EditorURL: "https://office.example.com/e?f={{fileId}}&x={{foo}}&y={{bar}}",
	})

	u := mustTargetURL(t, p.collabOpen(openCtx("a.docx", openXMLDocx)))
	q := u.Query()
	if got := q.Get("f"); got != "42" {
		t.Errorf("fileId 应正常替换，实际 %q", got)
	}
	if got := q.Get("x"); got != "{{foo}}" {
		t.Errorf("未知占位符应原样保留，x = %q", got)
	}
	if got := q.Get("y"); got != "{{bar}}" {
		t.Errorf("未知占位符应原样保留，y = %q", got)
	}

	if len(p.unknown) != 2 || p.unknown[0] != "foo" || p.unknown[1] != "bar" {
		t.Errorf("unknown = %v，期望 [foo bar]（供 init 阶段告警）", p.unknown)
	}
}

func TestEmptyEditorURLIsInert(t *testing.T) {
	p := newPlugin(Config{})
	requireNoURL(t, p.collabOpen(openCtx("报告.docx", openXMLDocx)))

	// 显式带其它配置、但 editorURL 为空，同样不能生效。
	p = newPlugin(Config{
		Extensions:      []string{"docx"},
		AppendUserQuery: true,
	})
	requireNoURL(t, p.collabOpen(openCtx("报告.docx", openXMLDocx)))

	if out := p.describe(); !strings.Contains(out, "不生效") {
		t.Fatalf("CLI 输出应说明插件不生效，实际: %s", out)
	}
}

func TestUnsafeOrInvalidURLIsRejected(t *testing.T) {
	cases := []string{
		"javascript:alert(1)://{{fileId}}",
		"data:text/html,<script>alert(1)</script>",
		"ftp://office.example.com/edit?f={{fileId}}",
		"//office.example.com/edit?f={{fileId}}", // 协议相对：没有 scheme
		"https:///no-host/edit?f={{fileId}}",     // 有 scheme 但没有 host
	}
	for _, tmpl := range cases {
		p := newPlugin(Config{EditorURL: tmpl})
		// 文件本身是命中的 —— 拒绝必须来自 URL 校验，而不是没匹配上。
		res := p.collabOpen(openCtx("a.docx", openXMLDocx))
		requireNoURL(t, res)
	}
}

func TestHTTPIsAllowed(t *testing.T) {
	p := newPlugin(Config{EditorURL: "http://office.example.com/edit?f={{fileId}}"})
	target(t, p.collabOpen(openCtx("a.docx", openXMLDocx)))
}

func TestAppendUserQuery(t *testing.T) {
	t.Run("无已有查询串", func(t *testing.T) {
		p := newPlugin(Config{
			EditorURL:       "https://office.example.com/edit",
			AppendUserQuery: true,
		})
		res := p.collabOpen(openCtx("a.docx", openXMLDocx))
		raw := target(t, res)
		if !strings.HasPrefix(raw, "https://office.example.com/edit?ndUser=") {
			t.Fatalf("拼接位置不对: %s", raw)
		}
		q := parseURL(t, raw).Query()
		if got := q.Get("ndUser"); got != "张三" {
			t.Errorf("ndUser = %q", got)
		}
		if got := q.Get("ndIp"); got != "203.0.113.9" {
			t.Errorf("ndIp = %q", got)
		}
	})

	t.Run("已有查询串用 & 拼接", func(t *testing.T) {
		p := newPlugin(Config{
			EditorURL:       "https://office.example.com/edit?fileId={{fileId}}",
			AppendUserQuery: true,
		})
		res := p.collabOpen(openCtx("a.docx", openXMLDocx))
		raw := target(t, res)
		if !strings.Contains(raw, "fileId=42&ndUser=") {
			t.Fatalf("已有查询串时应使用 & 拼接: %s", raw)
		}
		q := parseURL(t, raw).Query()
		if got := q.Get("fileId"); got != "42" {
			t.Errorf("fileId = %q", got)
		}
		if got := q.Get("ndUser"); got != "张三" {
			t.Errorf("ndUser = %q", got)
		}
		if got := q.Get("ndIp"); got != "203.0.113.9" {
			t.Errorf("ndIp = %q", got)
		}
	})

	t.Run("身份信息里的特殊字符也要转义", func(t *testing.T) {
		p := newPlugin(Config{
			EditorURL:       "https://office.example.com/edit",
			AppendUserQuery: true,
		})
		ctx := openCtx("a.docx", openXMLDocx)
		ctx["userName"] = "李 & 四"
		ctx["ip"] = "2001:db8::1"
		q := mustTargetURL(t, p.collabOpen(ctx)).Query()
		if got := q.Get("ndUser"); got != "李 & 四" {
			t.Errorf("ndUser = %q，期望原值", got)
		}
		if got := q.Get("ndIp"); got != "2001:db8::1" {
			t.Errorf("ndIp = %q，期望原值", got)
		}
	})

	t.Run("fragment 之前的参数才有效", func(t *testing.T) {
		p := newPlugin(Config{
			EditorURL:       "https://office.example.com/edit?fileId={{fileId}}#section",
			AppendUserQuery: true,
		})
		u := mustTargetURL(t, p.collabOpen(openCtx("a.docx", openXMLDocx)))
		if u.Fragment != "section" {
			t.Fatalf("fragment = %q，期望 section", u.Fragment)
		}
		if got := u.Query().Get("ndUser"); got != "张三" {
			t.Fatalf("ndUser 被拼到了 fragment 里: %q", got)
		}
	})

	t.Run("关闭时不追加", func(t *testing.T) {
		p := newPlugin(Config{
			EditorURL: "https://office.example.com/edit?fileId={{fileId}}",
		})
		raw := target(t, p.collabOpen(openCtx("a.docx", openXMLDocx)))
		if strings.Contains(raw, "ndUser") || strings.Contains(raw, "ndIp") {
			t.Fatalf("appendUserQuery=false 时不应追加参数: %s", raw)
		}
	})
}

func TestCLIOutput(t *testing.T) {
	p := newPlugin(Config{
		EditorURL:       "https://office.example.com/edit?fileId={{fileId}}&name={{fileName}}",
		AppendUserQuery: true,
	})

	res := p.fire(hookCLI, map[string]any{"command": cliCommand})
	out, ok := res.CtxPatch["cliOutput"].(string)
	if !ok || out == "" {
		t.Fatalf("onCLI 未返回 cliOutput: %#v", res.CtxPatch)
	}
	for _, want := range []string{
		"生效配置",
		"渲染示例",
		"fileId=42",
		// 示例文件名「报告 & 附表.docx」的查询串转义结果
		"name=%E6%8A%A5%E5%91%8A+%26+%E9%99%84%E8%A1%A8.docx",
		"ndUser=",
		"appendUserQuery",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("CLI 输出缺少 %q:\n%s", want, out)
		}
	}

	// 别的命令不该抢输出（宿主会把 onCLI 派发给所有插件）。
	res = p.fire(hookCLI, map[string]any{"command": "other-plugin"})
	if res.CtxPatch != nil {
		t.Fatalf("命令名不匹配时不应写 cliOutput: %#v", res.CtxPatch)
	}
}

func TestInitProtocolAndConfig(t *testing.T) {
	t.Run("协议不匹配必须失败", func(t *testing.T) {
		res, pl := initPlugin(initParams{HostProtocol: 2})
		if res.OK {
			t.Fatal("hostProtocol=2 时不应返回 ok")
		}
		if res.Error == "" {
			t.Fatal("失败时必须给出 error")
		}
		if pl != nil {
			t.Fatal("失败时不应返回可用的插件实例")
		}
	})

	t.Run("正常初始化的钩子与默认值", func(t *testing.T) {
		res, pl := initPlugin(initParams{
			HostProtocol: 1,
			Config:       json.RawMessage(`{"editorURL":"https://office.example.com/e?f={{fileId}}&x={{foo}}"}`),
		})
		if !res.OK || pl == nil {
			t.Fatalf("初始化失败: %+v", res)
		}
		if res.Name != pluginName || res.Version != version {
			t.Errorf("name/version = %s/%s", res.Name, res.Version)
		}
		if got := strings.Join(res.Hooks, ","); got != "onCollabOpen,onCLI" {
			t.Errorf("hooks = %s", got)
		}
		if !pl.exts["docx"] || !pl.exts["ppt"] {
			t.Errorf("默认扩展名未生效: %v", pl.exts)
		}
		if len(pl.mimePrefixes) != 2 {
			t.Errorf("默认 MIME 前缀未生效: %v", pl.mimePrefixes)
		}
		if len(pl.unknown) != 1 || pl.unknown[0] != "foo" {
			t.Errorf("未知占位符未记录: %v", pl.unknown)
		}
	})

	t.Run("空扩展名列表是显式关闭", func(t *testing.T) {
		res, pl := initPlugin(initParams{
			HostProtocol: 1,
			Config:       json.RawMessage(`{"editorURL":"https://office.example.com/e","extensions":[]}`),
		})
		if !res.OK || pl == nil {
			t.Fatalf("初始化失败: %+v", res)
		}
		if len(pl.exts) != 0 {
			t.Errorf("显式空 extensions 应关闭扩展名匹配: %v", pl.exts)
		}
	})

	t.Run("配置非法时退回默认且不生效", func(t *testing.T) {
		res, pl := initPlugin(initParams{
			HostProtocol: 1,
			Config:       json.RawMessage(`{"editorURL":`),
		})
		if !res.OK {
			t.Fatalf("配置笔误不应导致插件不可用: %+v", res)
		}
		if pl == nil || pl.cfg.EditorURL != "" {
			t.Fatal("配置解析失败后应退回默认（editorURL 为空）")
		}
	})
}
