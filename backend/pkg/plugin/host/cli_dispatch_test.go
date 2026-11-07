package host

import (
	"testing"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
)

// onCLI 的派发与回传。
//
// 命令行必须**拿到插件的输出**，所以 onCLI 与其它通知型钩子不同：
// 它是 ModeSync，插件写进 ctx 的 cliOutput / cliExitCode 会经 ctxPatch
// 回传给 main.go 里的 runPluginCLI。
//
// 这里用 echo 假插件（把每个 ctx 键以 echo_ 前缀回写）验证这条链路：
// 只要回写能到达宿主，cliOutput 就一定能到达命令行。
func TestOnCLIDispatchCarriesCommandAndReturnsPatch(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 5*time.Second)
	defer m.StopAll()

	man := manifestFor(t, string(plugin.HookCLI))
	if err := m.Load(LoadSpec{Name: man.Name, Dir: dir, Manifest: man}); err != nil {
		t.Fatalf("加载插件失败: %v", err)
	}
	if got := m.Count(plugin.HookCLI); got != 1 {
		t.Fatalf("Count(onCLI) = %d, want 1", got)
	}

	ctx := map[string]any{
		"command": "hello",
		"args":    []string{"a", "b"},
		"argsRaw": "a b",
	}
	m.DispatchSync(plugin.HookCLI, ctx)

	// 命令与参数要原样到达插件（否则插件无法分辨该响应哪个命令）
	if ctx["echo_command"] != "hello" {
		t.Errorf("插件没收到 command: %v", ctx["echo_command"])
	}
	if ctx["echo_argsRaw"] != "a b" {
		t.Errorf("插件没收到 argsRaw: %v", ctx["echo_argsRaw"])
	}
	// ctxPatch 能回传 = cliOutput / cliExitCode 也能回传
	if ctx["touched"] != true {
		t.Errorf("插件的回写没有到达宿主: %v", ctx)
	}
}

// TestOnCLIBlockable onCLI 是可拦截钩子：插件返回 blocked 时，
// main.go 的 runPluginCLI 会以非 0 退出码结束。
//
// 这条守住"判定走统一实现"：拦截语义只认 plugin.IsBlockedError，
// 命令行不该自己再写一遍字符串匹配。
//
// 用进程内 handler 而不是子进程插件：这里验证的是**判定与派发语义**，
// 真进程往返已由上面那个用例和 manager_test.go 覆盖。
func TestOnCLIBlockable(t *testing.T) {
	plugin.UnregisterAll(plugin.HookCLI)
	defer plugin.UnregisterAll(plugin.HookCLI)

	plugin.Register(plugin.HookCLI, func(ctx map[string]any) (map[string]any, error) {
		return nil, &blockedErr{}
	})

	ctx := map[string]any{"command": "dangerous"}
	var blocked bool
	for _, e := range plugin.Fire(plugin.HookCLI, ctx) {
		if plugin.IsBlockedError(e) {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("插件拒绝时 Fire 应报出可拦截错误（命令行据此设置非 0 退出码）")
	}
}

// blockedErr 模拟插件返回的拦截错误（文本含 "blocked"）。
type blockedErr struct{}

func (*blockedErr) Error() string { return "blocked by test plugin" }
