// Package plugin 定义 NebulaDrive 插件协议。
//
// # 架构
//
// 插件以**独立子进程**运行，通过 stdin/stdout 上的 JSON-RPC 2.0 与宿主通信。
// 不用 Go 原生 plugin.Open（不支持 Windows、需 cgo、ABI 脆弱），也不用 WASM
// （需 CGO，Go host function 绑定成本过高）。子进程方案跨平台、语言无关、
// 崩溃隔离，且启停不需要重启主服务 —— 这才是真正的热加载。
//
// # 三种能力（v2 起）
//
//  1. **通知（Notify）**：异步事件，插件只收不改，结果无人读取。
//     对应 ModeAsync + Blockable=false + Mutatable=false。
//  2. **决策（Decide）**：同步派发，插件返回含 "blocked" 的 error（v1）
//     或 decision="deny"（v2）即可拦截请求。对应 Blockable=true。
//  3. **变换（Mutate）**：同步派发，插件通过 FireResult.CtxPatch 回写 ctx，
//     业务侧读回并应用。对应 Mutatable=true。可同时具备决策能力。
//
// 业务侧统一通过 internal/hooks.Emit / Decide / Mutate 调用，本包只提供
// 底层派发与元信息，不掺业务逻辑。
//
// # 注册型扩展点（v2 起）
//
// 通知 / 决策 / 变换都是「宿主调用插件」。另有一类需求是「插件提供实现，
// 宿主来选」：自定义存储后端、认证源、通知渠道、预览器、支付回调等。
// 这类通过 Registry 登记（见 registry.go）：插件在 init 握手时声明
// providers，业务侧用 Resolve / CallProvider 找到并调用对应插件。
//
// # 锁与性能
//
// Fire() 先复制 handler 快照再释放读锁，然后才执行 —— 绝不能持锁执行
// 用户代码。热路径一律用 FireIf（无订阅零开销），避免为每个请求都做 IPC。
package plugin

import (
	"strings"
	"sync"
	"time"
)

// ---- 协议版本 ----

const (
	// ProtocolVersion 当前宿主协议版本。
	// v2：分组钩子、结构化决策、CtxPatch 变换、注册型扩展点、
	//     HTTP 页面插件、UI 扩展点、三文件契约。
	ProtocolVersion = 2

	// MinSupportedProtocolVersion 宿主仍能加载的最低插件协议版本。
	// v1 插件自动映射：钩子名与语义不变，只是用不上 v2 新钩子，
	// 且不要求 register.go / co.BEGG。示例插件 referer-guard 无需改动。
	MinSupportedProtocolVersion = 1
)

// handlerTimeout 单个进程内 handler 的执行上限。
const handlerTimeout = 5 * time.Second

// HandlerFunc 插件处理函数。
// ctx 是宿主传入的可变 map —— 它是插件**唯一**的输出通道。
// 第一个返回值在实现上会被 Fire 丢弃（历史设计），请通过修改 ctx 输出。
type HandlerFunc func(ctx map[string]any) (map[string]any, error)

// ---- 进程内注册表 ----

var (
	registryMu sync.RWMutex
	registry   = map[HookName][]HandlerFunc{}
)

// Dispatcher 进程外插件派发器。由 pkg/plugin/host 在初始化时注入。
type Dispatcher interface {
	Dispatch(h HookName, ctx map[string]any) []error
	DispatchSync(h HookName, ctx map[string]any) []error
	Count(h HookName) int
	Total() int
}

var (
	dispatchMu sync.RWMutex
	dispatcher Dispatcher
)

// SetDispatcher 注入进程外插件派发器。
func SetDispatcher(d Dispatcher) {
	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	dispatcher = d
}

func currentDispatcher() Dispatcher {
	dispatchMu.RLock()
	defer dispatchMu.RUnlock()
	return dispatcher
}

// Register 注册一个进程内 handler。同一钩子可注册多个，按注册顺序执行。
func Register(h HookName, fn HandlerFunc) {
	if fn == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[h] = append(registry[h], fn)
}

// UnregisterAll 清空某钩子的全部进程内 handler。仅供测试使用。
func UnregisterAll(h HookName) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, h)
}

func snapshot(h HookName) []HandlerFunc {
	registryMu.RLock()
	defer registryMu.RUnlock()
	src := registry[h]
	if len(src) == 0 {
		return nil
	}
	out := make([]HandlerFunc, len(src))
	copy(out, src)
	return out
}

// HasSubscribers 判断某钩子是否有任何订阅者（进程内或进程外）。
//
// 这是「轻量门」的基础：热路径先问一句有没有插件订阅，
// 没有就直接返回，连 ctx 都不用构造 —— 无插件时零开销。
func HasSubscribers(h HookName) bool {
	if len(snapshot(h)) > 0 {
		return true
	}
	if d := currentDispatcher(); d != nil {
		return d.Count(h) > 0
	}
	return false
}

// IsBlockedError 判断 error 是否为拦截型。
//
// 全项目唯一的拦截判定实现。v2 的结构化 decision="deny" 由 host
// 在合并 FireResult 时转成含 "blocked" 的 error，因此这里无需改动。
func IsBlockedError(err error) bool {
	if err == nil {
		return false
	}
	if b, ok := err.(Blockable); ok {
		return b.IsBlocked()
	}
	return strings.Contains(err.Error(), "blocked")
}

// Blockable 可拦截的错误。
type Blockable interface {
	error
	IsBlocked() bool
}

// DecisionOf 把 Fire 返回的 error 列表汇总成一个决策。
// 任一 error 含 "blocked" 即拒绝；其余 error 只作为原因文本返回（不拦截）。
func DecisionOf(errs []error) (allowed bool, reason string) {
	for _, err := range errs {
		if err == nil {
			continue
		}
		if IsBlockedError(err) {
			return false, err.Error()
		}
		if reason == "" {
			reason = err.Error()
		}
	}
	return true, reason
}

// RateLimitGate 是 onRateLimit 的同步拦截门（v1 兼容保留）。
//
// 该钩子在 7 处被调用，只有限流中间件这一处需要读返回值做拦截决策。
// 钩子保持异步不拖累热路径，仅在此处强制同步等待。
func RateLimitGate(ctx map[string]any) []error {
	if ctx == nil || !HasSubscribers(HookRateLimit) {
		return nil
	}
	d := currentDispatcher()
	if d == nil {
		return nil
	}
	var errs []error
	for _, fn := range snapshot(HookRateLimit) {
		if err := runHandler(HookRateLimit, fn, ctx); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, d.DispatchSync(HookRateLimit, ctx)...)
	return errs
}

// FireIf 仅在有订阅者时派发钩子（轻量门）。
//
// 业务侧热路径一律用它：没有插件声明该钩子时，连 ctx 都不必构造，
// 也不会产生 IPC 往返。需要读拦截结果时用 internal/hooks.Decide。
func FireIf(h HookName, ctx map[string]any) []error {
	if !HasSubscribers(h) {
		return nil
	}
	return Fire(h, ctx)
}

// Fire 派发钩子（按钩子既定模式）。
//
// 执行顺序：进程内 handler 全部执行完，再派发给进程外插件。
// ctx 是共享的可变 map：同步钩子下插件的 CtxPatch 会原地回写，
// 业务侧可在 Fire 返回后直接读取；异步钩子不回写。
//
// 不要在 ctx 里放敏感数据后再传给不可信插件 —— 第三方插件能看到
// 这个 hook 给出的全部字段。
func Fire(h HookName, ctx map[string]any) []error {
	if ctx == nil {
		ctx = map[string]any{}
	}
	var errs []error

	for _, fn := range snapshot(h) {
		if err := runHandler(h, fn, ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if d := currentDispatcher(); d != nil {
		errs = append(errs, d.Dispatch(h, ctx)...)
	}
	return errs
}

// applyHandlerPatch 把进程内 handler 返回的 kv 合并回 ctx。
//
// 兼容两种写法：直接返回 {"maxStorage": 123}，或按进程外 FireResult 的
// 约定返回 {"CtxPatch": {"maxStorage": 123}}。后者让进程内/进程外插件
// 可以用同一份代码处理变换结果。
//
// 早先 runHandler 直接丢弃 fn 的返回值，导致「变换」档只对进程外插件生效，
// 进程内 Go 插件的 CtxPatch 静默失效 —— 三档能力因此缺了一档。
func applyHandlerPatch(ctx, patch map[string]any) {
	if ctx == nil || len(patch) == 0 {
		return
	}
	if inner, ok := patch["CtxPatch"].(map[string]any); ok {
		patch = inner
	}
	for k, v := range patch {
		if k == "CtxPatch" {
			continue
		}
		ctx[k] = v
	}
}

// runHandler 执行单个进程内 handler，带 panic recover 与超时保护。
func runHandler(h HookName, fn HandlerFunc, ctx map[string]any) (err error) {
	type result struct {
		patch map[string]any
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: &PanicError{Hook: h, Value: r}}
			}
		}()
		patch, e := fn(ctx)
		ch <- result{patch: patch, err: e}
	}()

	select {
	case r := <-ch:
		applyHandlerPatch(ctx, r.patch)
		return r.err
	case <-time.After(handlerTimeout):
		return &TimeoutError{Hook: h, Timeout: handlerTimeout}
	}
}

// PanicError handler panic 时的错误。
type PanicError struct {
	Hook  HookName
	Value any
}

func (e *PanicError) Error() string { return "plugin panic in " + string(e.Hook) }

// TimeoutError handler 超时。
type TimeoutError struct {
	Hook    HookName
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return "plugin timeout in " + string(e.Hook) + ": handler exceeded " + e.Timeout.String()
}

// List 返回各钩子的 handler 数量（进程内 + 进程外合计）。
func List() map[HookName]int {
	in, out := ListSplit()
	total := make(map[HookName]int, len(in))
	for k, v := range in {
		total[k] = v
	}
	for k, v := range out {
		total[k] += v
	}
	return total
}

// ListSplit 分别返回进程内与进程外的 handler 数量。
func ListSplit() (inProc, outProc map[HookName]int) {
	inProc = map[HookName]int{}
	outProc = map[HookName]int{}

	registryMu.RLock()
	for k, v := range registry {
		inProc[k] = len(v)
	}
	registryMu.RUnlock()

	if d := currentDispatcher(); d != nil {
		for _, h := range AllHookNames() {
			if n := d.Count(h); n > 0 {
				outProc[h] = n
			}
		}
	}
	return inProc, outProc
}
