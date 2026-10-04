package host

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
)

// ---- 测试基础设施 ----
//
// 假插件源码放在 testdata/<kind>/main.go，用 `go build` 现场编译成可执行文件。
// 之所以真实编译而不是用脚本模拟：IPC 协议的分帧、并发写、进程退出语义
// 只有真进程才能验证，脚本替代会漏掉真正会出问题的地方。
//
// 编译产物放在**包级临时目录**而非 t.TempDir()：后者在单个测试结束时
// 就被删除，而缓存是跨测试复用的 —— 放在 t.TempDir() 里会导致
// 第二个用到同一插件的测试读到已被删除的路径。

var (
	buildMu   sync.Mutex
	buildDone = map[string]string{} // kind → 二进制路径
	buildErrs = map[string]error{}
	buildDir  string
)

// fakeBinary 编译（或复用）某个测试插件，返回可执行文件路径。
func fakeBinary(t *testing.T, kind string) string {
	t.Helper()
	buildMu.Lock()
	defer buildMu.Unlock()
	if p, ok := buildDone[kind]; ok {
		return p
	}
	if e, ok := buildErrs[kind]; ok {
		t.Fatalf("编译测试插件 %s 失败: %v", kind, e)
	}

	if buildDir == "" {
		d, err := os.MkdirTemp("", "nebula-faplugin")
		if err != nil {
			t.Fatalf("创建编译目录失败: %v", err)
		}
		buildDir = d
	}
	out := filepath.Join(buildDir, kind)
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	build := exec.Command("go", "build", "-o", out, ".")
	build.Dir = filepath.Join("testdata", kind)
	build.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=", "CGO_ENABLED=0")
	if o, err := build.CombinedOutput(); err != nil {
		e := fmt.Errorf("%w: %s", err, o)
		buildErrs[kind] = e
		t.Fatalf("编译测试插件 %s 失败: %v", kind, e)
	}
	buildDone[kind] = out
	return out
}

// newFixture 造一个含单个插件目录的测试环境。
//
// 返回 Manager 与插件目录。插件二进制已放进目录并命名为 <entry>。
func newFixture(t *testing.T, kind string, autoRestart bool, fireTimeout time.Duration) (*Manager, string) {
	t.Helper()
	bin := fakeBinary(t, kind)

	base := t.TempDir()
	dir := filepath.Join(base, "plugins", "testplug")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, entryName()), data, 0o755); err != nil {
		t.Fatal(err)
	}

	m := New(Config{
		PluginsDir:   filepath.Join(base, "plugins"),
		DataDir:      base,
		HostVersion:  "test",
		StartTimeout: 30 * time.Second,
		FireTimeout:  fireTimeout,
		AutoRestart:  autoRestart,
		Log:          func(string, ...any) {},
	})
	t.Cleanup(m.StopAll)
	return m, dir
}

func entryName() string {
	if runtime.GOOS == "windows" {
		return "plug.exe"
	}
	return "plug"
}

func manifestFor(t *testing.T, hooks ...string) *plugin.Manifest {
	t.Helper()
	m := &plugin.Manifest{
		Name:            "testplug",
		Title:           "Test Plugin",
		Version:         "1.0.0",
		Author:          "test",
		Entry:           entryName(),
		ProtocolVersion: plugin.ProtocolVersion,
		Hooks:           hooks,
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest 校验失败: %v", err)
	}
	return m
}

// ---- 加载与生命周期 ----

func TestManager_LoadAndInit(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 3*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	in := m.Get("testplug")
	if in == nil {
		t.Fatal("加载后 Get 返回 nil")
	}
	if got := in.Status().Status; got != StatusRunning {
		t.Fatalf("status = %q, want running", got)
	}
	if in.Status().PID <= 0 {
		t.Fatal("pid 应为正数")
	}
	if m.Total() != 1 {
		t.Fatalf("Total = %d, want 1", m.Total())
	}
	if m.Count(plugin.HookAntiLeech) != 1 {
		t.Fatalf("Count(onAntiLeech) = %d, want 1", m.Count(plugin.HookAntiLeech))
	}
	// 插件专属数据目录应已创建
	if st, err := os.Stat(in.DataDir); err != nil || !st.IsDir() {
		t.Fatalf("插件数据目录未创建: %v", err)
	}
}

func TestManager_LoadInitFailureRejected(t *testing.T) {
	m, dir := newFixture(t, "initfail", false, 2*time.Second)
	err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")})
	if err == nil {
		t.Fatal("init 报告失败时 Load 应失败")
	}
	// 失败原因应透出插件给的信息，便于管理员定位
	if !strings.Contains(err.Error(), "apiKey") {
		t.Fatalf("错误应含插件给出的原因，实际: %v", err)
	}
	// 关键：失败的插件不能留在 registry，否则会被反复派发
	if m.Get("testplug") != nil {
		t.Fatal("启动失败的插件仍留在 registry")
	}
	if m.Total() != 0 {
		t.Fatalf("Total = %d, want 0", m.Total())
	}
}

func TestManager_ManifestNameMismatch(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 3*time.Second)
	man := manifestFor(t, "onAntiLeech")
	man.Name = "different"
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir, Manifest: man}); err == nil {
		t.Fatal("manifest.name 与标识不一致应报错")
	}
}

func TestManager_UnloadRemovesFromRegistry(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 3*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatal(err)
	}
	m.Unload("testplug")
	if m.Get("testplug") != nil {
		t.Fatal("卸载后仍在 registry")
	}
	if m.Total() != 0 {
		t.Fatalf("Total = %d, want 0", m.Total())
	}
	// 重复卸载不应 panic
	m.Unload("testplug")
}

// ---- 派发 ----

func TestManager_SyncHookRoundTrip(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 3*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatal(err)
	}
	ctx := map[string]any{"fileName": "a.txt", "fileId": uint(7)}
	if errs := m.Dispatch(plugin.HookAntiLeech, ctx); len(errs) != 0 {
		t.Fatalf("不应有错误: %v", errs)
	}
	// ctxPatch 必须回传到业务方的 ctx —— CollabOpen 靠它读 ctx["url"]
	if ctx["touched"] != true {
		t.Fatalf("ctxPatch 未回传: %v", ctx)
	}
	if ctx["echo_fileName"] != "a.txt" {
		t.Fatalf("echo_fileName = %v, want a.txt", ctx["echo_fileName"])
	}
}

func TestManager_AsyncHookDoesNotBlock(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 3*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech", "onCollabSave")}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	m.Dispatch(plugin.HookCollabSave, map[string]any{"content": "x"})
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Fatalf("异步派发耗时 %s，应立即返回", el)
	}
}

func TestManager_AsyncHookDoesNotMutateCallerCtx(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 3*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech", "onCollabSave")}); err != nil {
		t.Fatal(err)
	}
	ctx := map[string]any{"content": "x"}
	m.Dispatch(plugin.HookCollabSave, ctx)
	// 异步派发的结果绝不能写回业务 ctx（业务已经继续往下走了）
	if _, leaked := ctx["touched"]; leaked {
		t.Fatalf("异步结果泄漏回业务 ctx: %v", ctx)
	}
}

func TestManager_BlockSemantics(t *testing.T) {
	m, dir := newFixture(t, "blocker", false, 3*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatal(err)
	}

	// 正常文件名 → 放行
	ctx := map[string]any{"fileName": "ok.txt"}
	if errs := m.Dispatch(plugin.HookAntiLeech, ctx); len(errs) != 0 {
		t.Fatalf("不应拦截: %v", errs)
	}
	if ctx["allowed"] != true {
		t.Fatalf("allowed = %v, want true", ctx["allowed"])
	}

	// 命中 blockme → 拦截，且错误文本必须含 blocked（业务侧靠这个判定）
	ctx2 := map[string]any{"fileName": "blockme.txt"}
	errs := m.Dispatch(plugin.HookAntiLeech, ctx2)
	if len(errs) == 0 {
		t.Fatal("应被拦截")
	}
	blocked := false
	for _, e := range errs {
		if plugin.IsBlockedMessage(e.Error()) {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("错误未标记 blocked（业务会放行）: %v", errs)
	}
}

func TestManager_UndeclaredHookNotDispatched(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 3*time.Second)
	// 只声明 onEmail
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onEmail")}); err != nil {
		t.Fatal(err)
	}
	if got := m.Count(plugin.HookEmail); got != 1 {
		t.Fatalf("Count(onEmail) = %d, want 1", got)
	}
	if got := m.Count(plugin.HookAntiLeech); got != 0 {
		t.Fatalf("Count(onAntiLeech) = %d, want 0（未声明不应被派发）", got)
	}
	// 未声明的钩子即便传入 ctx 也不该有回写
	ctx := map[string]any{"fileName": "x"}
	m.Dispatch(plugin.HookAntiLeech, ctx)
	if _, leaked := ctx["touched"]; leaked {
		t.Fatalf("未声明的钩子被派发了: %v", ctx)
	}
}

func TestManager_ConcurrentDispatch(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 5*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatal(err)
	}
	// 20 路并发：writeMu 必须保证多行 JSON 不会交错写进管道
	// （交错的话插件解析失败，测试会随机失败）
	const n = 20
	var wg sync.WaitGroup
	errCh := make(chan error, n*2)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := map[string]any{"fileName": fmt.Sprintf("f%d.txt", i), "n": i}
			for _, e := range m.Dispatch(plugin.HookAntiLeech, ctx) {
				errCh <- fmt.Errorf("i=%d dispatch: %v", i, e)
			}
			if ctx["touched"] != true {
				errCh <- fmt.Errorf("i=%d 未收到 ctxPatch", i)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Error(e)
	}
}

// ---- 故障处理 ----

func TestManager_PluginCrashDoesNotKillHost(t *testing.T) {
	m, dir := newFixture(t, "crasher", false, 2*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatal(err)
	}
	pid := m.Get("testplug").Status().PID
	if pid <= 0 {
		t.Fatal("pid 无效")
	}

	// 触发崩溃：插件收到 fire 就 exit(3)，不响应
	m.Dispatch(plugin.HookAntiLeech, map[string]any{"fileName": "x"})

	// 宿主必须存活：等插件被标记为非 running
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if in := m.Get("testplug"); in != nil && !in.IsLive() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	in := m.Get("testplug")
	if in == nil {
		t.Fatal("崩溃后实例不应消失（应标记 crashed 供管理员查看）")
	}
	if in.IsLive() {
		t.Fatal("崩溃后仍显示 running")
	}
	st := in.Status()
	if st.LastErr == "" {
		t.Fatal("崩溃后应记录 lastErr")
	}
	// 崩溃后再次派发必须安全返回，不能 panic 或挂死
	done := make(chan struct{})
	go func() {
		m.Dispatch(plugin.HookAntiLeech, map[string]any{"fileName": "y"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("崩溃后派发挂死")
	}
	// 宿主自身仍能正常服务
	if m.Total() != 1 {
		t.Fatalf("宿主状态异常 Total = %d", m.Total())
	}
	_ = m.List()
	_ = m.Logs("testplug", 100)
}

func TestManager_FireTimeout(t *testing.T) {
	m, dir := newFixture(t, "slowpoke", false, 300*time.Millisecond)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	errs := m.Dispatch(plugin.HookAntiLeech, map[string]any{"fileName": "x"})
	el := time.Since(start)
	if el > 3*time.Second {
		t.Fatalf("耗时 %s，超时未生效", el)
	}
	if len(errs) == 0 {
		t.Fatal("超时应产生错误")
	}
}

func TestManager_LogsRecorded(t *testing.T) {
	m, dir := newFixture(t, "echo", false, 3*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatal(err)
	}
	logs := m.Logs("testplug", 100)
	if len(logs) == 0 {
		t.Fatal("应有启动日志")
	}
	joined := ""
	for _, l := range logs {
		if l.Plugin != "testplug" {
			t.Errorf("日志 Plugin 字段 = %q", l.Plugin)
		}
		joined += l.Message + "\n"
	}
	if !strings.Contains(joined, "已启动") {
		t.Fatalf("日志缺少启动记录: %s", joined)
	}
}

func TestManager_DispatchToNoPlugin(t *testing.T) {
	m := New(Config{DataDir: t.TempDir(), Log: func(string, ...any) {}})
	t.Cleanup(m.StopAll)
	ctx := map[string]any{"a": 1}
	if errs := m.Dispatch(plugin.HookAntiLeech, ctx); len(errs) != 0 {
		t.Fatalf("无插件时不应报错: %v", errs)
	}
	if ctx["a"] != 1 {
		t.Fatal("无插件时 ctx 不应被改动")
	}
}

// ---- 自动重启 ----

func TestManager_AutoRestartAfterCrash(t *testing.T) {
	// 开着自动重启，验证崩溃后能拉起来
	m, dir := newFixture(t, "crasher", true, 2*time.Second)
	if err := m.Load(LoadSpec{Name: "testplug", Dir: dir,
		Manifest: manifestFor(t, "onAntiLeech")}); err != nil {
		t.Fatal(err)
	}
	firstPID := m.Get("testplug").Status().PID

	m.Dispatch(plugin.HookAntiLeech, map[string]any{"fileName": "x"})

	// 必须分两阶段等待：先确认已死，再确认已活。
	// 若只等"活"，可能在 waitProc 还没被调度时就看到 running，
	// 于是拿到仍是崩溃前的 pid，误判为"未重启"。
	died := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !died {
		if in := m.Get("testplug"); in == nil || !in.IsLive() {
			died = true
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !died {
		t.Fatal("崩溃后未检测到插件停止")
	}

	// crasher 每次 fire 都死，退避 2s 后应自动重启并换 pid
	var newPID int
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if in := m.Get("testplug"); in != nil && in.IsLive() {
			newPID = in.Status().PID
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if newPID == 0 {
		t.Fatal("崩溃后未自动重启")
	}
	if newPID == firstPID {
		t.Fatalf("重启后 pid 未变化（仍为 %d）", firstPID)
	}
}
