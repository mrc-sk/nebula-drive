package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可注入的假时钟。
//
// 窗口逻辑（1 分钟 / 1 小时）如果只能靠 time.Sleep 验证，测试会跑上小时，
// 而且边界处照样不稳。limiter.now 就是为此存在的：测试直接推进时间。
//
// 加锁是因为并发测试里多个 goroutine 会同时读 Now。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	// 固定起点：让失败信息可复现，不依赖运行当天的时间。
	return &fakeClock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newTestLimiter 构造一个用假时钟驱动的限流器。
func newTestLimiter(t *testing.T, cfg Config) (*limiter, *fakeClock) {
	t.Helper()
	clk := newFakeClock()
	return newLimiter(cfg, clk.Now), clk
}

// loginCtx 构造登录下载的 ctx。数字刻意保持 float64：
// 宿主经 JSON-RPC 传进来的数字就是这个类型，用 int 测会掩盖类型断言 bug。
func loginCtx(ip string, userID float64) map[string]any {
	ctx := map[string]any{
		"ip":       ip,
		"ua":       "Mozilla/5.0 (test)",
		"fileId":   float64(42),
		"fileName": "archive.zip",
		"referer":  "https://example.com/files",
	}
	if userID > 0 {
		ctx["userId"] = userID
		ctx["userName"] = "tester"
	}
	return ctx
}

// shareCtx 构造公开分享的 ctx。分享访问者匿名：userId=0、userName=""。
func shareCtx(ip, kind string) map[string]any {
	return map[string]any{
		"userId":       float64(0),
		"userName":     "",
		"ip":           ip,
		"ua":           "curl/8.0",
		"fileId":       float64(7),
		"fileName":     "share.zip",
		"referer":      "https://example.com/s/abc",
		"shareId":      float64(3),
		"shareKind":    kind,
		"shareOwnerId": float64(1),
	}
}

func mustAllow(t *testing.T, l *limiter, ctx map[string]any) {
	t.Helper()
	if blocked, msg := l.check(ctx); blocked {
		t.Fatalf("预期放行，却被拦截: %s", msg)
	}
}

func mustBlock(t *testing.T, l *limiter, ctx map[string]any) string {
	t.Helper()
	blocked, msg := l.check(ctx)
	if !blocked {
		t.Fatal("预期拦截，却被放行")
	}
	return msg
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.MaxPerMinute != 60 {
		t.Errorf("maxPerMinute 默认值 = %d, 期望 60", cfg.MaxPerMinute)
	}
	if cfg.MaxPerHour != 1200 {
		t.Errorf("maxPerHour 默认值 = %d, 期望 1200", cfg.MaxPerHour)
	}
	if cfg.CountSharePreview {
		t.Error("countSharePreview 默认应为 false")
	}
	if cfg.MaxTrackedIPs != 10000 {
		t.Errorf("maxTrackedIPs 默认值 = %d, 期望 10000", cfg.MaxTrackedIPs)
	}
	if cfg.ExemptUserIDs == nil || len(cfg.ExemptUserIDs) != 0 {
		t.Errorf("exemptUserIds 默认应为空切片, 实际 %v", cfg.ExemptUserIDs)
	}

	// 显式配置不能被默认值覆盖。
	cfg = Config{MaxPerMinute: 30, MaxPerHour: 600, MaxTrackedIPs: 5}.withDefaults()
	if cfg.MaxPerMinute != 30 || cfg.MaxPerHour != 600 || cfg.MaxTrackedIPs != 5 {
		t.Errorf("显式配置被默认值覆盖了: %+v", cfg)
	}
}

func TestAllowUnderLimit(t *testing.T) {
	l, clk := newTestLimiter(t, Config{MaxPerMinute: 10, MaxPerHour: 100})

	for i := 0; i < 5; i++ {
		mustAllow(t, l, loginCtx("1.2.3.4", 0))
		clk.Advance(2 * time.Second)
	}
	if got := l.totalCount(); got != 5 {
		t.Errorf("1 分钟窗口计数 = %d, 期望 5", got)
	}
	if got := l.trackedIPs(); got != 1 {
		t.Errorf("跟踪 IP 数 = %d, 期望 1", got)
	}
}

func TestBlockedOverMinuteLimit(t *testing.T) {
	l, clk := newTestLimiter(t, Config{MaxPerMinute: 3, MaxPerHour: 1000})

	for i := 0; i < 3; i++ {
		mustAllow(t, l, loginCtx("1.2.3.4", 0))
		clk.Advance(time.Second)
	}

	msg := mustBlock(t, l, loginCtx("1.2.3.4", 0))
	if !strings.Contains(msg, "blocked") {
		t.Errorf("拦截文案必须含小写 blocked，实际: %q", msg)
	}
	if !strings.HasPrefix(msg, "blocked by download-limiter: ") {
		t.Errorf("拦截文案前缀不对: %q", msg)
	}
	if !strings.Contains(msg, "1 分钟内下载 4 次，超过上限 3") {
		t.Errorf("拦截文案未给出可操作信息: %q", msg)
	}
	if !strings.Contains(msg, "IP 1.2.3.4") {
		t.Errorf("拦截文案未包含 IP: %q", msg)
	}

	// 被拦截的请求不能写入窗口，否则持续重试会永久占住配额。
	if got := l.totalCount(); got != 3 {
		t.Errorf("拦截请求不应计数：窗口计数 = %d, 期望 3", got)
	}
	// 紧接着再打一次，仍然必须拦截（限流没有被"漏掉"一次）。
	if again, _ := l.check(loginCtx("1.2.3.4", 0)); !again {
		t.Error("超限后连续第二次请求仍应被拦截")
	}
}

func TestWindowExpiryRestoresAllow(t *testing.T) {
	l, clk := newTestLimiter(t, Config{MaxPerMinute: 3, MaxPerHour: 1000})

	for i := 0; i < 4; i++ {
		if i < 3 {
			mustAllow(t, l, loginCtx("5.6.7.8", 0))
		} else {
			mustBlock(t, l, loginCtx("5.6.7.8", 0))
		}
		clk.Advance(time.Second)
	}

	// 推进 61 秒：三次下载都落在 60 秒窗口之外。
	clk.Advance(61 * time.Second)
	mustAllow(t, l, loginCtx("5.6.7.8", 0))

	// 窗口被真正淘汰，而不是"继续累加"：此刻分钟窗口里只剩刚放行的那 1 次。
	if got := l.totalCount(); got != 1 {
		t.Errorf("窗口过期后计数 = %d, 期望 1（旧时间戳必须被丢弃）", got)
	}
}

func TestHourLimitIndependent(t *testing.T) {
	l, clk := newTestLimiter(t, Config{MaxPerMinute: 1000, MaxPerHour: 5})

	for i := 0; i < 5; i++ {
		mustAllow(t, l, loginCtx("8.8.8.8", 0))
		clk.Advance(time.Second)
	}

	msg := mustBlock(t, l, loginCtx("8.8.8.8", 0))
	if !strings.Contains(msg, "1 小时内下载 6 次，超过上限 5") {
		t.Errorf("小时上限文案不对: %q", msg)
	}

	// 1 分钟窗口已过期，但 1 小时窗口还在，必须继续拦 —— 两个窗口独立生效。
	clk.Advance(61 * time.Second)
	msg = mustBlock(t, l, loginCtx("8.8.8.8", 0))
	if !strings.Contains(msg, "1 小时内") {
		t.Errorf("分钟窗口过期后应由小时窗口拦截，实际: %q", msg)
	}

	// 再推进 1 小时：所有小时窗口时间戳过期，恢复放行。
	clk.Advance(3600 * time.Second)
	mustAllow(t, l, loginCtx("8.8.8.8", 0))
}

func TestExemptUserAllowedAndNotCounted(t *testing.T) {
	l, _ := newTestLimiter(t, Config{
		MaxPerMinute:  1,
		MaxPerHour:    100,
		ExemptUserIDs: []uint64{7},
	})

	// 豁免用户连打 100 次：一次都不该被拦。
	for i := 0; i < 100; i++ {
		mustAllow(t, l, loginCtx("9.9.9.9", 7))
	}
	if got := l.totalCount(); got != 0 {
		t.Errorf("豁免用户的请求不应计数，窗口计数 = %d", got)
	}
	if got := l.trackedIPs(); got != 0 {
		t.Errorf("豁免用户的请求不应建立 IP 状态，跟踪数 = %d", got)
	}

	// 同一个 IP 换成匿名访问者：上限是 1，第一次必须放行 ——
	// 如果豁免路径偷偷计了数，这里会被拦。
	mustAllow(t, l, loginCtx("9.9.9.9", 0))
	if got := l.totalCount(); got != 1 {
		t.Errorf("匿名请求应计数 1 次，实际 %d", got)
	}
	// 非豁免用户仍然受限。
	mustBlock(t, l, loginCtx("9.9.9.9", 8))
}

func TestSharePreviewNotCountedByDefault(t *testing.T) {
	l, _ := newTestLimiter(t, Config{MaxPerMinute: 2, MaxPerHour: 100})

	for i := 0; i < 10; i++ {
		mustAllow(t, l, shareCtx("5.6.7.8", "preview"))
	}
	if got := l.totalCount(); got != 0 {
		t.Errorf("preview 默认不应计数，实际 %d", got)
	}
	if got := l.trackedIPs(); got != 0 {
		t.Errorf("preview 默认不应建立 IP 状态，跟踪数 = %d", got)
	}

	// 分享下载（shareKind=download）仍然计数并受限。
	mustAllow(t, l, shareCtx("5.6.7.8", "download"))
	mustAllow(t, l, shareCtx("5.6.7.8", "download"))
	msg := mustBlock(t, l, shareCtx("5.6.7.8", "download"))
	if !strings.Contains(msg, "blocked") {
		t.Errorf("分享下载超限文案必须含 blocked: %q", msg)
	}
}

func TestSharePreviewCountedWhenEnabled(t *testing.T) {
	l, _ := newTestLimiter(t, Config{
		MaxPerMinute:      2,
		MaxPerHour:        100,
		CountSharePreview: true,
	})

	mustAllow(t, l, shareCtx("4.4.4.4", "preview"))
	mustAllow(t, l, shareCtx("4.4.4.4", "preview"))
	msg := mustBlock(t, l, shareCtx("4.4.4.4", "preview"))
	if !strings.Contains(msg, "blocked") {
		t.Errorf("countSharePreview=true 时预览超限必须拦截: %q", msg)
	}
	if got := l.totalCount(); got != 2 {
		t.Errorf("预览计数 = %d, 期望 2", got)
	}
}

func TestEmptyIPAllowedWithoutCounting(t *testing.T) {
	l, _ := newTestLimiter(t, Config{MaxPerMinute: 1, MaxPerHour: 1})

	for i := 0; i < 50; i++ {
		ctx := loginCtx("", 0)
		ctx["ip"] = "   " // 只有空白也应视为取不到 IP
		mustAllow(t, l, ctx)
	}
	if got := l.totalCount(); got != 0 {
		t.Errorf("无 IP 的请求不应计数，实际 %d", got)
	}
	if got := l.trackedIPs(); got != 0 {
		t.Errorf("无 IP 的请求不应建立状态，跟踪数 = %d", got)
	}
}

func TestConcurrentFire(t *testing.T) {
	const (
		goroutines = 20
		perRoutine = 50
		want       = goroutines * perRoutine
	)
	l, _ := newTestLimiter(t, Config{MaxPerMinute: 1_000_000, MaxPerHour: 1_000_000})

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perRoutine; j++ {
				if blocked, msg := l.check(loginCtx("7.7.7.7", 0)); blocked {
					t.Errorf("并发下不应拦截: %s", msg)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := l.totalCount(); got != want {
		t.Errorf("并发计数 = %d, 期望 %d（可能有更新丢失）", got, want)
	}
	if got := l.trackedIPs(); got != 1 {
		t.Errorf("跟踪 IP 数 = %d, 期望 1", got)
	}
}

func TestMemoryCapEvictsLeastRecentlyActive(t *testing.T) {
	l, clk := newTestLimiter(t, Config{
		MaxPerMinute:  1_000_000,
		MaxPerHour:    1_000_000,
		MaxTrackedIPs: 3,
	})

	for i := 1; i <= 10; i++ {
		mustAllow(t, l, loginCtx(fmt.Sprintf("10.0.0.%d", i), 0))
		clk.Advance(time.Second) // 让 lastSeen 有先后
	}

	if got := l.trackedIPs(); got > 3 {
		t.Fatalf("跟踪 IP 数 = %d, 必须 <= maxTrackedIPs(3)", got)
	}
	if got := l.trackedIPs(); got != 3 {
		t.Errorf("跟踪 IP 数 = %d, 期望稳定在 3", got)
	}
	// 最近活动的三个应留下，最早的应被淘汰。
	for _, ip := range []string{"10.0.0.8", "10.0.0.9", "10.0.0.10"} {
		if !l.isTracked(ip) {
			t.Errorf("最近活动的 IP %s 不应被淘汰", ip)
		}
	}
	if l.isTracked("10.0.0.1") {
		t.Error("最久未活动的 IP 10.0.0.1 应被淘汰")
	}
}

func TestCLIOutputTopTen(t *testing.T) {
	l, clk := newTestLimiter(t, Config{MaxPerMinute: 1_000_000, MaxPerHour: 1_000_000})

	// 第 i 个 IP 打 i 次，排名确定：10.0.1.12 最高，10.0.1.01 最低。
	for i := 1; i <= 12; i++ {
		ip := fmt.Sprintf("10.0.1.%02d", i)
		for j := 0; j < i; j++ {
			mustAllow(t, l, loginCtx(ip, 0))
		}
		clk.Advance(time.Second)
	}

	out := l.cliOutput()
	if !strings.Contains(out, pluginName) {
		t.Errorf("CLI 输出缺少插件名前缀: %q", out)
	}
	if !strings.Contains(out, "10.0.1.12") {
		t.Errorf("CLI 输出缺少计数最高的 IP: %q", out)
	}
	// 只展示 top 10，最低的两个不应出现。
	for _, ip := range []string{"10.0.1.01", "10.0.1.02"} {
		if strings.Contains(out, ip) {
			t.Errorf("CLI 输出应只展示 top 10，却出现了 %s: %q", ip, out)
		}
	}
	if !strings.Contains(out, "另有 2 个 IP 未展示") {
		t.Errorf("CLI 输出缺少未展示数量提示: %q", out)
	}
}
