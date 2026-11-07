package plugin

import (
	"errors"
	"strings"
	"testing"
)

func TestRegisterAndFire(t *testing.T) {
	// 使用独立钩子避免与其他测试冲突
	called := false
	Register(HookBackup, func(ctx map[string]any) (map[string]any, error) {
		called = true
		if v, ok := ctx["k"].(string); !ok || v != "v" {
			t.Errorf("unexpected ctx: %v", ctx)
		}
		return map[string]any{"ok": true}, nil
	})
	errs := Fire(HookBackup, map[string]any{"k": "v"})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !called {
		t.Fatal("handler not called")
	}
}

func TestFireError(t *testing.T) {
	Register(HookRestore, func(ctx map[string]any) (map[string]any, error) {
		return nil, errors.New("blocked by policy")
	})
	errs := Fire(HookRestore, map[string]any{})
	if len(errs) == 0 {
		t.Fatal("expected error")
	}
}

func TestFireNoHandlers(t *testing.T) {
	errs := Fire(HookCLI, map[string]any{})
	if errs != nil {
		t.Fatalf("expected nil errors, got %v", errs)
	}
}

func TestList(t *testing.T) {
	Register(HookEmail, func(ctx map[string]any) (map[string]any, error) { return nil, nil })
	m := List()
	if _, ok := m[HookEmail]; !ok {
		t.Fatalf("HookEmail not in list: %v", m)
	}
}

func TestHookDocs(t *testing.T) {
	if len(HookDocs) == 0 {
		t.Fatal("empty hook docs")
	}
	for _, h := range []HookName{HookEmail, HookBackup, HookRestore, HookApiAuth, HookDBMigrate, HookRateLimit, HookAntiLeech, HookCLI, HookCollabOpen, HookCollabSave} {
		if HookDocs[h] == "" {
			t.Errorf("missing doc for %s", h)
		}
	}
}

// TestV1FrozenHooksPreserved v1 冻结的 10 个钩子在 v2 里必须原样保留。
//
// 这是 v2 合并时新增的守卫：v2 把钩子表扩到 99 个并引入 Since 字段，
// 但 v1 插件（referer-guard、smtp-email、hmac-auth 及全部示例）依赖的
// 钩子名与语义不能变 —— 变了等于悄悄废掉所有存量插件。
// Wired 校验见 plugin_v2_test.go 的 TestHookTableFullyWired。
func TestV1FrozenHooksPreserved(t *testing.T) {
	for _, h := range []HookName{HookEmail, HookBackup, HookRestore, HookApiAuth, HookDBMigrate, HookRateLimit, HookAntiLeech, HookCLI, HookCollabOpen, HookCollabSave} {
		m, ok := MetaOf(h)
		if !ok {
			t.Errorf("v1 冻结钩子 %s 不在表里", h)
			continue
		}
		if m.Since != 1 {
			t.Errorf("v1 冻结钩子 %s 的 Since=%d，应为 1", h, m.Since)
		}
		if !m.Wired {
			t.Errorf("v1 冻结钩子 %s 应保持 Wired=true", h)
		}
		if HookDocs[h] == "" {
			t.Errorf("v1 冻结钩子 %s 缺文档", h)
		}
	}
	// 本地接线成果的语义锚点：onAntiLeech 必须是同步可拦截（referer-guard 依赖），
	// onApiAuth 必须是同步可拦截（扩展鉴权依赖，见 FIXLOG §22/§23）。
	anti, ok := MetaOf(HookAntiLeech)
	if !ok {
		t.Fatal("缺少 onAntiLeech")
	}
	if anti.Mode != ModeSync || !anti.Blockable {
		t.Errorf("onAntiLeech 应为同步可拦截，实际 mode=%v blockable=%v", anti.Mode, anti.Blockable)
	}
	api, ok := MetaOf(HookApiAuth)
	if !ok {
		t.Fatal("缺少 onApiAuth")
	}
	if api.Mode != ModeSync || !api.Blockable {
		t.Errorf("onApiAuth 应为同步可拦截（扩展鉴权依赖），实际 mode=%v blockable=%v", api.Mode, api.Blockable)
	}
	// v2 用 onShareDownloadBefore 承接公开分享下载/预览（v1 的 onAntiLeech
	// 只管登录用户）。这个钩子必须存在且可拦截，否则 FIXLOG §20 补上的
	// "分享链接也要过防盗链闸"在 v2 里就断了。
	share, ok := MetaOf(HookShareDownloadBefore)
	if !ok {
		t.Fatal("缺少 onShareDownloadBefore（v2 的分享下载闸）")
	}
	if !share.Blockable {
		t.Error("onShareDownloadBefore 应可拦截（分享下载的防盗链/限流依赖它）")
	}
	// v2 的 onAntiLeech Doc 明确说"公开分享下载不走本钩子"并指路新钩子 ——
	// 这条指路必须保留，否则 v1 插件作者会误以为自己的插件也保护分享链接。
	if !strings.Contains(anti.Doc, "onFileDownloadBefore") && !strings.Contains(anti.Doc, "onShareDownloadBefore") {
		t.Errorf("onAntiLeech 的 Doc 应指路新钩子（v1 插件作者据此迁移）：%q", anti.Doc)
	}
}

