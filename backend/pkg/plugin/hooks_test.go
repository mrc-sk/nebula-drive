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

// TestHookPlanMetadata 守住「规划中」占位元数据的完整性。
//
// 管理端把 Planned 当成新功能占位状态的唯一数据源：漏标会让一个
// 实际没接入的钩子显示成可用，等于对管理员撒谎；标了却不写 Plan
// 则会让界面出现一个没有任何解释的空徽章。
func TestHookPlanMetadata(t *testing.T) {
	for _, m := range Hooks {
		if m.Planned && strings.TrimSpace(m.Plan) == "" {
			t.Errorf("钩子 %s 标了 Planned 却没有 Plan 说明", m.Name)
		}
		if !m.Planned && m.Plan != "" {
			t.Errorf("钩子 %s 没标 Planned 却写了 Plan（界面不会展示，属于失效文案）", m.Name)
		}
		// 未接线的钩子必须进计划，否则它就是个没人认领的死钩子
		if !m.Wired && !m.Planned {
			t.Errorf("钩子 %s 既未接入也未列入计划", m.Name)
		}
	}

	// onAntiLeech 的公开分享覆盖**曾经缺失**（分享下载与预览都不过闸，
	// 分享链接因此没有任何防盗链能力）。现已补齐，所以这里反向断言。
	//
	// 反向断言的价值：将来若有人把分享路径的调用点删掉（例如重构
	// serveShareFile 时漏了），必须同时把 Planned/Plan 加回来，
	// 否则管理端会显示"可用"而实际不管分享 —— 那正是这套元数据要防的谎报。
	// 行为层面的守卫另见 controllers 的分享防盗链测试。
	anti, ok := MetaOf(HookAntiLeech)
	if !ok {
		t.Fatal("缺少 onAntiLeech")
	}
	if !anti.Wired {
		t.Error("onAntiLeech 应保持 Wired=true（登录下载与公开分享都有调用点）")
	}
	if anti.Planned {
		t.Errorf("onAntiLeech 的分享覆盖已补齐，不应再标 Planned（当前 Plan=%q）", anti.Plan)
	}
	for _, want := range []string{"shareKind", "shareId"} {
		if !strings.Contains(anti.Doc, want) {
			t.Errorf("onAntiLeech 的 Doc 应说明分享路径的 %s 字段：%q", want, anti.Doc)
		}
	}
}

