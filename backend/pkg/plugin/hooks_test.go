package plugin

import (
	"errors"
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
