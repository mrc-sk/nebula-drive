// Package plugin 定义 NebulaDrive 插件协议。
//
// # 架构
//
// 插件以**独立子进程**运行，通过 stdin/stdout 上的 JSON-RPC 2.0 与宿主通信。
// 不用 Go 原生 plugin.Open（不支持 Windows、需 cgo、ABI 脆弱），也不用 WASM
// （需 CGO，Go host function 绑定成本过高）。子进程方案跨平台、语言无关、
// 崩溃隔离，且启停不需要重启主服务 —— 这才是真正的热加载。
//
// # 两种插件形态
//
//  1. 进程内（in-process）：宿主二进制里 init() 时调 Register 注册的 Go 函数。
//     零 IPC 开销，用于内置功能。
//  2. 进程外（out-of-process）：独立可执行文件，由宿主拉起并通过 RPC 调用。
//     可热插拔、可独立崩溃，用于第三方扩展。
//
// 两种形态对业务代码完全透明：业务只调 Fire()，不需要关心 handler 在哪。
//
// # 锁
//
// Fire() 会先复制 handler 快照再释放读锁，然后才执行 —— 绝不能持锁执行
// 用户代码：进程外插件的 RPC 往返可能长达数百毫秒甚至超时，持锁会把
// Register 卡死，也会让 List() 阻塞。
package plugin

import (
	"strings"
	"sync"
	"time"
)

// ProtocolVersion 宿主与插件之间的 RPC 协议版本。
// 宿主与插件 manifest 声明的版本必须一致，否则拒绝加载 ——
// 避免低版本插件把高版本才有的字段当成旧语义解析。
const ProtocolVersion = 1

// HookName 钩子名。
type HookName string

// 全部钩子常量。
const (
	HookEmail      HookName = "onEmail"
	HookBackup     HookName = "onBackup"
	HookRestore    HookName = "onRestore"
	HookApiAuth    HookName = "onApiAuth"
	HookDBMigrate  HookName = "onDBMigrate"
	HookRateLimit  HookName = "onRateLimit"
	HookAntiLeech  HookName = "onAntiLeech"
	HookCLI        HookName = "onCLI"
	HookCollabOpen HookName = "onCollabOpen"
	HookCollabSave HookName = "onCollabSave"
)

// HookMode 决定宿主如何派发该钩子。
type HookMode int

const (
	// ModeAsync 派发后不等待结果。
	// 用于"通知型"钩子：插件被调用只是收到一份事件，回调结果无人读取。
	// 异步派发让业务请求立即返回，插件慢/挂/死都不影响主流程。
	ModeAsync HookMode = iota

	// ModeSync 派发后阻塞等待插件返回，直到超时。
	// 用于"决策型"钩子：业务要读返回值才能决定放行/拒绝。
	ModeSync
)

func (m HookMode) String() string {
	if m == ModeSync {
		return "sync"
	}
	return "async"
}

// handlerTimeout 单个进程内 handler 的执行上限。
// handler 是宿主进程内的 Go 函数，属于可信代码，但仍需兜底
// —— 一个死循环的 handler 不该把 HTTP 请求挂死。
const handlerTimeout = 5 * time.Second

// HandlerFunc 插件处理函数。
//
// ctx 是宿主传入的可变 map —— 它是插件**唯一**的输出通道。
// 第一个返回值在实现上会被 Fire 丢弃（历史设计，见 Fire 注释），
// 保留它只是为了兼容既有 handler 签名；请通过修改 ctx 输出。
type HandlerFunc func(ctx map[string]any) (map[string]any, error)

// HookMeta 描述一个钩子的静态信息。
type HookMeta struct {
	Name string   `json:"name"`
	Doc  string   `json:"doc"`
	Mode HookMode `json:"-"`
	// Blockable 该钩子的 error 能否拦截请求。
	// 宿主用 strings.Contains(err.Error(), "blocked") 判定。
	Blockable bool `json:"blockable"`
	// Wired 该钩子是否已接入业务代码（有实际 Fire 调用点）。
	Wired bool `json:"wired"`
}

// Hooks 是全部钩子的元信息表。顺序即前端展示顺序。
//
// Mode 的取值依据来自实际触发点代码，不是设计意图：
//
//   - ModeSync 仅两个：HookAntiLeech（file.go:396 读返回值决定 403，
//     且只在那一处被调用）与 HookCollabOpen（需读 ctx["url"] 才能 302，
//     而 ctx 是引用类型，异步派发时业务读 ctx 可能早于插件写入 → 竞态）。
//   - HookRateLimit 是矛盾点：它在 7 处被调用，但只有
//     middleware/rate_limit.go:99 检查返回值（403），其余 6 处丢弃。
//     设 ModeSync 会让那 6 处每次白等 IPC 往返；设 ModeAsync 又会让
//     中间件那处的拦截失效。取舍：设 ModeAsync + Blockable=false，
//     中间件改用 D 包的 RateLimitGate 做同步拦截（见 HookRateLimit 文档）。
//   - 其余钩子返回值全被丢弃，异步即可。
var Hooks = []HookMeta{
	{Name: string(HookAntiLeech), Mode: ModeSync, Blockable: true, Wired: true,
		Doc: "文件下载前触发。ctx={userId,userName,ip,ua,fileId,fileName,referer}；" +
			"返回 error 且文本含 \"blocked\" 即 403。目前唯一下载拦截点，做防盗链必须用它。" +
			"注意：公开分享下载不走本钩子。"},
	{Name: string(HookRateLimit), Mode: ModeAsync, Blockable: false, Wired: true,
		Doc: "高频操作前触发（登录/上传/下载/分享增删/全局限流中间件）。" +
			"ctx 字段随调用点而变：中间件处为 {category,ip,ua,path,userId?,userName?}，" +
			"业务处含 {userId,userName,ip,ua,action|fileId|fileName|shareId}。" +
			"⚠ 异步派发，仅事件通知：需要在下载前拦截请用 onAntiLeech。"},
	{Name: string(HookCollabOpen), Mode: ModeSync, Blockable: false, Wired: true,
		Doc: "打开协作编辑时触发。ctx={userId,userName,fileId,fileName,mimeType,ip}；" +
			"在 ctx 中写入 url 字段则 302 跳转到该地址（OnlyOffice 等外部编辑器）。"},
	{Name: string(HookCollabSave), Mode: ModeAsync, Blockable: false, Wired: true,
		Doc: "保存协作内容时触发，仅事件通知。" +
			"ctx={userId,userName,fileId,fileName,content}；content 是完整文件明文，" +
			"大文件会显著增加内存占用。"},
	{Name: string(HookEmail), Mode: ModeAsync, Blockable: false, Wired: true,
		Doc: "创建站内通知时触发，仅事件通知。ctx={to,subject,body}，用户邮箱为空则不触发。" +
			"系统本身不含任何发信实现，此钩子是邮件外发的唯一入口。"},
	{Name: string(HookDBMigrate), Mode: ModeAsync, Blockable: false, Wired: true,
		Doc: "AutoMigrate 与版本化迁移全部完成后触发，**每次启动都会触发**（不只升级时）。" +
			"ctx={fromVersion,toVersion}，两者相等表示本次无迁移。插件建表必须幂等。"},
	{Name: string(HookBackup), Mode: ModeAsync, Blockable: false, Wired: false,
		Doc: "【预留未接入】后端暂无 Fire 调用点，注册处理器不会有任何效果。"},
	{Name: string(HookRestore), Mode: ModeAsync, Blockable: false, Wired: false,
		Doc: "【预留未接入】后端暂无 Fire 调用点，注册处理器不会有任何效果。"},
	{Name: string(HookApiAuth), Mode: ModeAsync, Blockable: false, Wired: false,
		Doc: "【预留未接入】后端暂无 Fire 调用点，注册处理器不会有任何效果。"},
	{Name: string(HookCLI), Mode: ModeAsync, Blockable: false, Wired: false,
		Doc: "【预留未接入】后端暂无 Fire 调用点，且不存在对应的 CLI 子命令。"},
}

// HookModeOf 返回钩子的派发模式。未登记的钩子按异步处理
// （最安全的选择：宁可漏拦截也不能拖慢主流程）。
func HookModeOf(h HookName) HookMode {
	for _, m := range Hooks {
		if m.Name == string(h) {
			return m.Mode
		}
	}
	return ModeAsync
}

// AllHookNames 返回全部钩子名。
func AllHookNames() []HookName {
	out := make([]HookName, 0, len(Hooks))
	for _, m := range Hooks {
		out = append(out, HookName(m.Name))
	}
	return out
}

// MetaOf 返回某钩子的元信息。
func MetaOf(h HookName) (HookMeta, bool) {
	for _, m := range Hooks {
		if m.Name == string(h) {
			return m, true
		}
	}
	return HookMeta{}, false
}

// ---- 进程内注册表 ----

// registry 保存进程内 handler。进程外插件不走这里，
// 由 pkg/plugin/host 单独管理，通过 SetDispatcher 挂到 Fire 上。
var (
	registryMu sync.RWMutex
	registry   = map[HookName][]HandlerFunc{}
)

// Dispatcher 进程外插件派发器。由 pkg/plugin/host 在初始化时注入，
// 避免 host 与 plugin 包互相 import（host 可以 import plugin，反之不行）。
type Dispatcher interface {
	// Dispatch 按钩子的既定模式派发（见 HookMeta.Mode）。
	// ctx 会被原地修改（插件写入的字段会回传）。
	// 返回 error 列表；其中含 "blocked" 的会被业务判定为拦截。
	// 异步钩子不返回结果，error 列表恒为空。
	Dispatch(h HookName, ctx map[string]any) []error

	// DispatchSync 无视钩子的既定模式，强制同步等待全部插件返回。
	//
	// 用于「钩子整体异步、但个别调用点必须读结果」的场景 ——
	// 目前只有 onRateLimit：它在 7 处被调用，只有限流中间件那一处
	// 需要拦截决策。给 Dispatcher 单独开这个口，比把整个钩子改成
	// 同步（让另外 6 处每次都白等 IPC 往返）划算。
	DispatchSync(h HookName, ctx map[string]any) []error

	// Count 返回该钩子上已加载的进程外插件数。
	Count(h HookName) int

	// Total 返回已加载的进程外插件总数。
	Total() int
}

var (
	dispatchMu sync.RWMutex
	dispatcher Dispatcher
)

// SetDispatcher 注入进程外插件派发器。由 host 包在初始化时调用。
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

// snapshot 复制 handler 列表。
//
// 必须复制而不是持锁遍历：执行用户代码期间不能持 registryMu，
// 否则一个卡住的 handler 会让 Register 和 List 全部阻塞。
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

// IsBlockedError 判断 error 是否为拦截型。
//
// 这是全项目**唯一**的拦截判定实现：middleware/rate_limit.go、
// controllers/file.go 与本包的同步门都走它。
// 改判定语义时务必同步那几处，否则拦截行为会分裂。
func IsBlockedError(err error) bool {
	if err == nil {
		return false
	}
	if b, ok := err.(Blockable); ok {
		return b.IsBlocked()
	}
	return strings.Contains(err.Error(), "blocked")
}

// Blockable 可拦截的错误。插件返回的 error 若实现该接口，
// 无论文本是什么都算拦截（供未来结构化拦截用）。
type Blockable interface {
	error
	IsBlocked() bool
}

// RateLimitGate 是 onRateLimit 的同步拦截门。
//
// 背景：该钩子在 7 处被调用，只有��流中间件这一处需要读返回值做拦截
// 决策，其余 6 处（登录/上传/下载/分享增删）都丢弃结果。
//
//   - 若把整个钩子设为 ModeSync，那 6 处每次都要白等一次 IPC 往返；
//   - 若保持 ModeAsync，中间件这处的拦截就永远失效。
//
// 解决：钩子保持异步（不拖累 6 处热路径），仅在需要决策的地方调本函数，
// 由它走 Dispatcher.DispatchSync 强制同步等待。
//
// 代价：每个请求最多多等一个 FireTimeout（默认 2s）。
// 但仅在**有插件声明了 onRateLimit** 时才有实际等待 ——
// 没有插件时 DispatchSync 立即返回空。
func RateLimitGate(ctx map[string]any) []error {
	if ctx == nil {
		return nil
	}
	d := currentDispatcher()
	if d == nil {
		return nil
	}
	// 进程内 handler 走 Fire（异步语义保持不变），
	// 进程外插件走 DispatchSync 强制同步。
	var errs []error
	for _, fn := range snapshot(HookRateLimit) {
		if err := runHandler(HookRateLimit, fn, ctx); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, d.DispatchSync(HookRateLimit, ctx)...)
	return errs
}
//
// Fire 派发钩子（按钩子既定模式）。
//
// 执行顺序：进程内 handler 全部执行完，再派发给进程外插件。
//
// 两种 handler 的结果都会汇总进返回的 error 列表。业务侧判定拦截的规则
// 不变：err.Error() 含子串 "blocked"。
//
// ctx 是共享的可变 map：进程内 handler 与进程外插件都能写入它，
// 且能读到彼此的写入。因此**不要**在 ctx 里放敏感数据后再传给不可信插件 ——
// 第三方插件能看到这个 hook 给出的全部字段。
//
// 本函数不捕获进程内 handler 的 panic（它与业务代码同进程，
// recover 之后继续跑业务更难排查）；host 包派发进程外插件时已做隔离 ——
// 插件崩溃只杀子进程，不影响宿主。
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

// runHandler 执行单个进程内 handler，带 panic recover 与超时保护。
func runHandler(h HookName, fn HandlerFunc, ctx map[string]any) (err error) {
	type result struct {
		err error
	}
	// 容量 1：超时后 handler 仍可写入而不永久阻塞在 send 上。
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: &PanicError{Hook: h, Value: r}}
			}
		}()
		_, e := fn(ctx)
		ch <- result{err: e}
	}()

	select {
	case r := <-ch:
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

func (e *PanicError) Error() string {
	return "plugin panic in " + string(e.Hook)
}

// TimeoutError handler 超时。
type TimeoutError struct {
	Hook    HookName
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return "plugin timeout in " + string(e.Hook) + ": handler exceeded " +
		e.Timeout.String()
}

// List 返回各钩子的 handler 数量（进程内 + 进程外合计）。
// 这是 /api/admin/plugins/hooks 的数据源 —— 之前前端显示随机数，
// 正是因为它拿不到这个接口。
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
//
// 分开展示对管理员有意义：进程内的是编译进主程序的（改不了），
// 进程外的是可热插拔的第三方插件（能启停）。混在一起会让人以为
// 装了插件就能改主程序行为。
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

// HookDocs 钩子中文描述（供前端展示）。由 Hooks 表生成，避免两处维护。
var HookDocs = func() map[HookName]string {
	m := map[HookName]string{}
	for _, m2 := range Hooks {
		m[HookName(m2.Name)] = m2.Doc
	}
	return m
}()
