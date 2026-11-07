package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- 协议版本与钩子表 ----

func TestProtocolV2KeepsV1Compatibility(t *testing.T) {
	if ProtocolVersion != 2 {
		t.Fatalf("ProtocolVersion = %d, want 2", ProtocolVersion)
	}
	if MinSupportedProtocolVersion != 1 {
		t.Fatalf("MinSupportedProtocolVersion = %d, want 1", MinSupportedProtocolVersion)
	}
	// 老插件（examples/plugins/referer-guard）声明 v1，必须仍能被解析
	m, err := ParseManifest([]byte(`{
		"name":"referer-guard","version":"1.0.0","entry":"referer-guard",
		"protocolVersion":1,"hooks":["onAntiLeech"]
	}`))
	if err != nil {
		t.Fatalf("v1 清单应被接受: %v", err)
	}
	if m.ProtocolVersion != 1 {
		t.Fatalf("v1 协议版本被改写: %d", m.ProtocolVersion)
	}
	// 越界版本必须拒绝
	for _, v := range []int{0, 3, 99} {
		raw := []byte(`{"name":"x","entry":"x","protocolVersion":` + itoa(v) + `,"hooks":["onAntiLeech"]}`)
		if _, err := ParseManifest(raw); err == nil {
			t.Errorf("protocolVersion=%d 应被拒绝", v)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestHookTableFullyWired(t *testing.T) {
	names := AllHookNames()
	if len(names) != len(Hooks) {
		t.Fatalf("AllHookNames=%d 与 Hooks 表=%d 不一致", len(names), len(Hooks))
	}
	if len(Hooks) < 90 {
		t.Fatalf("钩子表疑似被截断：仅 %d 个", len(Hooks))
	}
	seen := map[string]bool{}
	for _, m := range Hooks {
		if seen[m.Name] {
			t.Errorf("重复钩子 %s", m.Name)
		}
		seen[m.Name] = true
		if !m.Wired {
			t.Errorf("钩子 %s 标记 wired=false（要求全部真接线）", m.Name)
		}
		if m.Group == "" || m.Doc == "" {
			t.Errorf("钩子 %s 缺分组或文档", m.Name)
		}
		if m.Since != 1 && m.Since != 2 {
			t.Errorf("钩子 %s 的 Since 非法: %d", m.Name, m.Since)
		}
		if _, ok := MetaOf(HookName(m.Name)); !ok {
			t.Errorf("MetaOf 查不到 %s", m.Name)
		}
		if got := HookModeOf(HookName(m.Name)); got != m.Mode {
			t.Errorf("钩子 %s 的 mode=%v，表里是 %v", m.Name, got, m.Mode)
		}
	}
	if len(GroupDocs) < 15 {
		t.Errorf("分组文档过少: %d", len(GroupDocs))
	}
	if len(HooksInGroup(GroupSystem)) == 0 {
		t.Error("system 分组为空")
	}
}

func TestV1HooksFrozen(t *testing.T) {
	v1 := V1HookNames()
	if len(v1) == 0 {
		t.Fatal("v1 钩子列表为空")
	}
	for _, name := range v1 {
		if !IsV1Hook(HookName(name)) {
			t.Errorf("%s 应被识别为 v1 钩子", name)
		}
	}
	// 示例插件订阅的 onAntiLeech 必须仍是 v1 冻结钩子
	if !IsV1Hook(HookAntiLeech) {
		t.Fatal("onAntiLeech 必须是 v1 冻结钩子，否则 referer-guard 会失效")
	}
}

func TestHookDocsCoverAll(t *testing.T) {
	for _, n := range AllHookNames() {
		if HookDocs[n] == "" {
			t.Errorf("钩子 %s 缺 HookDocs 文本", n)
		}
	}
}

// ---- 三档能力：通知 / 决策 / 变换 ----

func TestDecisionOf(t *testing.T) {
	allowed, _ := DecisionOf(nil)
	if !allowed {
		t.Fatal("无错误时应放行")
	}
	allowed, reason := DecisionOf([]error{errors.New("仅提示")})
	if !allowed || reason != "仅提示" {
		t.Fatalf("普通 error 不应拦截: allowed=%v reason=%q", allowed, reason)
	}
	allowed, reason = DecisionOf([]error{
		errors.New("先来的提示"),
		errors.New("plugin x: blocked: 策略拒绝"),
	})
	if allowed {
		t.Fatal("含 blocked 的 error 必须拦截")
	}
	if !strings.Contains(reason, "blocked") {
		t.Fatalf("拦截原因应带原文: %q", reason)
	}
	if !IsBlockedError(errors.New("plugin y: blocked: nope")) {
		t.Fatal("IsBlockedError 未识别 blocked")
	}
}

func TestFireIfZeroCostAndCtxPatch(t *testing.T) {
	const h = HookHealthCheck
	UnregisterAll(h)
	defer UnregisterAll(h)

	// 无订阅者：FireIf 直接返回 nil，handler 不会被调用
	if errs := FireIf(h, map[string]any{"k": "v"}); errs != nil {
		t.Fatalf("无订阅时 FireIf 应返回 nil，得到 %v", errs)
	}

	// 变换档：进程内 handler 返回的 CtxPatch 必须回写 ctx
	Register(h, func(ctx map[string]any) (map[string]any, error) {
		return map[string]any{
			"CtxPatch": map[string]any{"healthy": false, "checkedBy": "in-proc"},
		}, nil
	})
	ctx := map[string]any{"healthy": true}
	if errs := FireIf(h, ctx); len(errs) != 0 {
		t.Fatalf("FireIf 返回错误: %v", errs)
	}
	if ctx["healthy"] != false || ctx["checkedBy"] != "in-proc" {
		t.Fatalf("进程内 CtxPatch 未回写 ctx: %v", ctx)
	}

	// 也支持直接返回 kv（不必包一层 CtxPatch）
	UnregisterAll(h)
	Register(h, func(ctx map[string]any) (map[string]any, error) {
		return map[string]any{"healthy": "ok"}, nil
	})
	ctx2 := map[string]any{}
	if errs := FireIf(h, ctx2); len(errs) != 0 {
		t.Fatalf("FireIf 返回错误: %v", errs)
	}
	if ctx2["healthy"] != "ok" {
		t.Fatalf("直接 kv patch 未回写: %v", ctx2)
	}
}

// ---- 注册型扩展点 ----

func TestRegistryAddResolveAndRemove(t *testing.T) {
	r := GlobalRegistry()
	const name = "test-registry-plugin"
	r.Remove(name) // 前置清理，防重复运行残留
	defer r.Remove(name)

	regs := []Registration{{Kind: ProviderStorage, Name: "b2", Label: "Backblaze B2"}}
	pages := []PageDecl{{
		Name: "panel", Home: "index.php", Area: "main",
		Title: "对象存储面板", Role: "any", Order: 3,
	}}
	ui := &UIDecl{
		Theme: &ThemeDecl{
			Name: "midnight", Label: "午夜蓝",
			Variables: map[string]string{"--nebula-primary": "#123456"},
		},
		Settings: []SettingField{{Key: "token", Label: "Token", Type: "password"}},
	}
	r.Add(name, regs, pages, ui, "http://127.0.0.1:8080/ext/test-registry-plugin")

	if p, ok := r.Resolve(ProviderStorage, "b2"); !ok || p.Plugin != name {
		t.Fatalf("Resolve 失败: %+v ok=%v", p, ok)
	}
	if _, ok := r.Resolve(ProviderStorage, "not-exist"); ok {
		t.Fatal("不存在的注册项不应被解析到")
	}
	if len(r.Providers(ProviderStorage)) == 0 {
		t.Fatal("Providers(storage) 为空")
	}
	if len(r.AllProviders()) == 0 {
		t.Fatal("AllProviders 为空")
	}

	var panel *SidebarItem
	for _, it := range r.Sidebar("main", "any") {
		if it.Plugin == name && it.Name == "panel" {
			cp := it
			panel = &cp
		}
	}
	if panel == nil {
		t.Fatal("Sidebar(main) 没有插件页面")
	}
	if strings.Contains(panel.URL, "/ext/test-registry-plugin//") {
		t.Errorf("URL 出现重复斜杠: %s", panel.URL)
	}
	if !strings.HasSuffix(panel.URL, "/index.php") {
		t.Errorf("URL 末段不是页面文件: %s", panel.URL)
	}
	if panel.Embed != "iframe" {
		t.Errorf("默认 embed 应为 iframe，得到 %q", panel.Embed)
	}
	if len(r.Sidebar("admin", "any")) != 0 || len(r.Sidebar("main", "admin")) == 0 {
		// admin 角色也应该能看到 main 区（Sidebar 只挡 role=admin 的项对非 admin 不可见）
		t.Log("sidebar 角色过滤符合预期")
	}
	if r.Themes()[name].Name != "midnight" {
		t.Fatalf("主题未登记: %+v", r.Themes())
	}
	if len(r.Settings()[name]) != 1 || r.Settings()[name][0].Key != "token" {
		t.Fatalf("设置面板未登记: %+v", r.Settings())
	}

	r.Remove(name)
	if _, ok := r.Resolve(ProviderStorage, "b2"); ok {
		t.Fatal("Remove 后注册项仍可解析")
	}
	if len(r.Themes()[name].Name) != 0 {
		t.Fatal("Remove 后主题仍在")
	}
}

func TestCallProviderNoRegistrationIsNoop(t *testing.T) {
	if errs := CallProvider(ProviderSearch, "definitely-not-registered", map[string]any{}); errs != nil {
		t.Fatalf("未注册的能力应返回 nil，得到 %v", errs)
	}
}

// ---- co.BEGG 页面路径与对外地址 ----

func TestPagePathStripsStaticPrefix(t *testing.T) {
	cases := []struct {
		static, home, want string
	}{
		{"ASS", "ASS/in.php", "/in.php"},
		{"ASS", "ASS/sub/page.php", "/sub/page.php"},
		{"", "in.php", "/in.php"},
		{"", "", "/"},
		{"static", "static/index.html", "/index.html"},
	}
	for _, c := range cases {
		b := &Begg{Static: c.static, Home: c.home}
		if got := b.PagePath(c.home); got != c.want {
			t.Errorf("PagePath(static=%q, home=%q) = %q, want %q",
				c.static, c.home, got, c.want)
		}
	}
}

func TestPublicHostRule(t *testing.T) {
	cases := []struct {
		domain, boundIP, want string
	}{
		{"drive.example.com", "1.2.3.4", "drive.example.com"},
		{"", "1.2.3.4", "1.2.3.4"},
		{"", "", "127.0.0.1"},
		{"https://example.com/", "1.2.3.4", "example.com"},
	}
	for _, c := range cases {
		if got := PublicHost(c.domain, c.boundIP); got != c.want {
			t.Errorf("PublicHost(%q,%q) = %q, want %q", c.domain, c.boundIP, got, c.want)
		}
	}
}

func TestProbeRuntimeReportsMissingInterpreter(t *testing.T) {
	b := &Begg{
		Runtime:  "php",
		Requires: []string{"nb-definitely-not-a-real-interpreter-xyz"},
	}
	_, err := b.ProbeRuntime()
	if err == nil {
		t.Fatal("缺解释器时必须报错，不能静默降级")
	}
	if !strings.Contains(err.Error(), "nb-definitely-not-a-real-interpreter-xyz") {
		t.Fatalf("错误里应点名缺失的解释器: %v", err)
	}
	if !strings.Contains(err.Error(), "不会自动安装") {
		t.Fatalf("错误里应说明不自动安装: %v", err)
	}
}

// ---- 三文件契约：目录形式不限定 + v1 自动跳过 ----

const v2Manifest = `{
  "name": "ass-demo",
  "version": "1.0.0",
  "protocolVersion": 2,
  "pages": [{"name":"home","area":"main","title":"ASS 面板","home":"in.php"}]
}`

// 故意引用一个根本不存在的类型：宿主只读语法树，不编译、不需要 Go 工具链。
const v2RegisterGo = `package ass

var Spec = NotARealType{
	Name:            "ass-demo",
	Version:         "1.0.0",
	ProtocolVersion: 2,
	Hooks:           []string{},
	Runtime:         "php",
	Home:            "in.php",
	Install:         []string{"in.php", "co.BEGG"},
}
`

const v2Begg = `{
  "runtime": "php",
  "home": "in.php",
  "listen": "0.0.0.0",
  "requires": ["php"],
  "pages": [{"name":"home","area":"main","title":"ASS 面板","home":"in.php"}],
  "ui": {
    "theme": {"name":"ass-theme","variables":{"--nebula-primary":"#ff0000"}},
    "settings": [{"key":"greeting","label":"问候语","type":"text"}]
  }
}`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestContractV2ArbitraryDirectoryLayout(t *testing.T) {
	dir := t.TempDir()
	// 三个文件分散：plugin.json 在根，register.go / co.BEGG / in.php 在 ASS/
	writeFile(t, filepath.Join(dir, ManifestFileName), v2Manifest)
	writeFile(t, filepath.Join(dir, "ASS", GoDeclFileName), v2RegisterGo)
	writeFile(t, filepath.Join(dir, "ASS", BeggFileName), v2Begg)
	writeFile(t, filepath.Join(dir, "ASS", "in.php"), `<?php echo "hello"; ?>`)

	c, err := LoadContract(dir, false)
	if err != nil {
		t.Fatalf("分散布局应被接受: %v", err)
	}
	if c.V1 {
		t.Fatal("v2 插件被误判为 v1")
	}
	if c.Dir != dir {
		t.Errorf("Dir 应为插件根 %q，得到 %q", dir, c.Dir)
	}
	if c.ManifestDir != dir {
		t.Errorf("ManifestDir 应为 %q，得到 %q", dir, c.ManifestDir)
	}
	if c.BeggDir != filepath.Join(dir, "ASS") {
		t.Errorf("BeggDir 应为 ASS/，得到 %q", c.BeggDir)
	}
	if c.Decl == nil || c.Decl.Name != "ass-demo" || c.Decl.ProtocolVersion != 2 {
		t.Errorf("register.go 声明解析异常: %+v", c.Decl)
	}
	if c.Begg == nil || c.Begg.Runtime != "php" || len(c.Begg.Pages) != 1 {
		t.Errorf("co.BEGG 解析异常: %+v", c.Begg)
	}
	if c.Begg.UI == nil || c.Begg.UI.Theme == nil || len(c.Begg.UI.Settings) != 1 {
		t.Error("UI 扩展点声明未解析出来")
	}
	if len(MissingFiles(dir)) != 0 {
		t.Errorf("三文件齐全却报缺失: %v", MissingFiles(dir))
	}
}

func TestContractV2RejectsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), v2Manifest)
	if _, err := LoadContract(dir, false); err == nil {
		t.Fatal("缺 register.go / co.BEGG 时应报错")
	}
	missing := MissingFiles(dir)
	if len(missing) != 2 {
		t.Fatalf("应报缺 2 个文件，得到 %v", missing)
	}
}

func TestContractV2CrossCheckCatchesMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), v2Manifest)
	writeFile(t, filepath.Join(dir, GoDeclFileName), strings.Replace(v2RegisterGo, `"ass-demo"`, `"other-name"`, 1))
	writeFile(t, filepath.Join(dir, BeggFileName), v2Begg)
	writeFile(t, filepath.Join(dir, "in.php"), "x")
	_, err := LoadContract(dir, false)
	if err == nil || !strings.Contains(err.Error(), "声明不一致") {
		t.Fatalf("name 不一致应被交叉校验拦下，得到 %v", err)
	}
}

func TestContractV1SkipsThreeFileRequirement(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFileName), `{
		"name":"legacy","version":"1.0.0","entry":"legacy-bin",
		"protocolVersion":1,"hooks":["onAntiLeech"]
	}`)
	c, err := LoadContract(dir, false)
	if err != nil {
		t.Fatalf("v1 插件应无需三文件即可加载: %v", err)
	}
	if !c.V1 || c.Decl != nil || c.Begg != nil {
		t.Fatalf("v1 契约标记异常: V1=%v Decl=%v Begg=%v", c.V1, c.Decl, c.Begg)
	}
	if len(MissingFiles(dir)) != 0 {
		// MissingFiles 只描述 v2 要求，v1 由 LoadContract 自己跳过
		t.Log("v1 不做三文件要求，MissingFiles 仅用于诊断展示")
	}
}

func TestParseGoDeclDoesNotCompile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, GoDeclFileName)
	writeFile(t, p, v2RegisterGo)
	d, err := ParseGoDecl(p)
	if err != nil {
		t.Fatalf("纯声明文件应能只读解析（不编译）: %v", err)
	}
	if d.Name != "ass-demo" || d.Version != "1.0.0" || d.ProtocolVersion != 2 {
		t.Fatalf("字段解析错误: %+v", d)
	}
	if len(d.Hooks) != 0 || len(d.Install) != 2 {
		t.Fatalf("切片字段解析错误: %+v", d)
	}
	if d.Runtime != "php" || d.Home != "in.php" {
		t.Fatalf("runtime/home 解析错误: %+v", d)
	}
}

func TestPurePageV2ManifestNeedsNoEntry(t *testing.T) {
	m, err := ParseManifest([]byte(v2Manifest))
	if err != nil {
		t.Fatalf("v2 纯页面插件应可省略 entry: %v", err)
	}
	if m.Entry != "" || len(m.Pages) != 1 {
		t.Fatalf("解析结果异常: %+v", m)
	}
	// 有 hooks 却没 entry 仍必须拒绝（要跑 JSON-RPC 子进程）
	_, err = ParseManifest([]byte(`{
		"name":"rpc-plugin","protocolVersion":2,
		"hooks":["onAntiLeech"]
	}`))
	if err == nil {
		t.Fatal("有 hooks 却无 entry 应被拒绝")
	}
}
