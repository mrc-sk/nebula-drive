// Package host 是插件宿主：管理进程外插件的完整生命周期。
//
// 职责：
//   - 拉起/停止插件子进程（真正的热加载，启停不重启主服务）
//   - stdio 上的 JSON-RPC 2.0 编解码
//   - 调用超时、崩溃检测、指数退避自动重启
//   - 环形日志缓冲，供管理端查看
//   - 实现 plugin.Dispatcher，挂在 plugin.Fire 上
//
// 关键设计：
//
// 崩溃隔离 —— 插件 panic/野指针只杀子进程，宿主不受影响。宿主只在
// 收到 pipe 错误（进程已死）时把它标记为 crashed 并跳过。
//
// 不持锁做 IO —— 任何 RPC 往返都可能在锁外完成。registry 用 RWMutex
// 保护，但取出 *Instance 后立刻放锁，再对它调 Call。
package host

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
)

// 进程状态。
const (
	StatusStopped   = "stopped"   // 未运行（已禁用或未安装）
	StatusStarting  = "starting"  // 正在拉起
	StatusRunning   = "running"   // 正常运行
	StatusCrashed   = "crashed"   // 异常退出
	StatusFailed    = "failed"    // 启动失败（manifest 错误、init 失败等）
	StatusStopping  = "stopping"  // 正在停止
	StatusDisabled  = "disabled"  // 管理员禁用
)

// 自动重启的退避参数。
const (
	// restartBaseDelay 首次重启延迟。
	restartBaseDelay = 2 * time.Second
	// restartMaxDelay 重启延迟上限。
	restartMaxDelay = 5 * time.Minute
	// maxRestarts 一个插件连续崩溃这么多次后放弃自动重启，
	// 转为 crashed 等待人工介入。防止崩溃插件无限重启刷爆日志。
	maxRestarts = 5
	// crashWindow 连续崩溃的统计窗口。
	crashWindow = 10 * time.Minute
	// logBufferSize 环形日志容量（条）。
	logBufferSize = 500
)

// Config 宿主配置。
type Config struct {
	// PluginsDir 插件安装根目录。每个插件一个子目录。
	PluginsDir string
	// DataDir 宿主数据目录。
	DataDir string
	// HostVersion 传给插件的宿主版本串。
	HostVersion string
	// SiteURL 站点根 URL。
	SiteURL string
	// StartTimeout 单插件启动预算。
	StartTimeout time.Duration
	// FireTimeout 单次同步 fire 往返超时。
	FireTimeout time.Duration
	// AutoRestart 崩溃后是否自动重启。
	AutoRestart bool
	// Log 宿主日志输出。
	Log func(format string, args ...any)
	// ConfigFor 返回某插件的配置（来自 plugins.config 字段）。
	ConfigFor func(pluginName string) json.RawMessage
}

// Instance 一个已加载的插件进程。
type Instance struct {
	// Name 插件标识（manifest.name）。
	Name string
	// Manifest 解析后的清单。
	Manifest *plugin.Manifest
	// Dir 插件目录。
	Dir string
	// DataDir 插件专属可写目录。
	DataDir string

	// ---- 以下字段由 mu 保护 ----
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	status   string
	lastErr  string
	pid      int
	restarts int
	firstCr  time.Time
	stopping bool

	// pending 等待中的调用（id → chan）
	pendMu  sync.Mutex
	pending map[int64]chan *plugin.RPCResponse
	nextID  int64
	seq     int64

	// writeMu 序列化对 stdin 的写。
	// 多个并发 Call 共享同一条管道，不加锁会把两行 JSON 交错写进去，
	// 插件必然解析失败。
	writeMu sync.Mutex
}

// Status 返回当前状态快照。
func (in *Instance) Status() StatusInfo {
	in.mu.Lock()
	defer in.mu.Unlock()
	return StatusInfo{
		Name:     in.Name,
		Title:    in.Manifest.Title,
		Version:  in.Manifest.Version,
		Author:   in.Manifest.Author,
		Status:   in.status,
		PID:      in.pid,
		LastErr:  in.lastErr,
		Restarts: in.restarts,
		Hooks:    in.Manifest.Hooks,
	}
}

// StatusInfo 插件状态（对外 JSON）。
type StatusInfo struct {
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	Version  string   `json:"version"`
	Author   string   `json:"author"`
	Status   string   `json:"status"`
	PID      int      `json:"pid"`
	LastErr  string   `json:"lastErr,omitempty"`
	Restarts int      `json:"restarts"`
	Hooks    []string `json:"hooks"`
}

// IsLive 返回实例是否可用于派发。
func (in *Instance) IsLive() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.status == StatusRunning
}

// ---- Manager ----

// mu 保护 insts/logs/closed；closing 在 StopAll 时关闭，用于唤醒等待重启的 goroutine。
type Manager struct {
	cfg Config

	mu      sync.RWMutex
	insts   map[string]*Instance
	closing chan struct{}

	logMu  sync.Mutex
	logs   map[string][]LogEntry
	closed bool
}

// LogEntry 一条宿主侧日志。
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Plugin  string `json:"plugin"`
	Message string `json:"message"`
}

// New 创建 Manager。
func New(cfg Config) *Manager {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = plugin.DefaultStartTimeout
	}
	if cfg.FireTimeout <= 0 {
		cfg.FireTimeout = plugin.DefaultFireTimeout
	}
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	return &Manager{
		cfg:     cfg,
		insts:   map[string]*Instance{},
		logs:    map[string][]LogEntry{},
		closing: make(chan struct{}),
	}
}

// Get 返回全局 Manager，未初始化时返回 nil。
var global *Manager

// InitGlobal 初始化全局 Manager 并挂到 plugin.Fire 上。
// 由 main.go 在数据库就绪后调用。
func InitGlobal(cfg Config) *Manager {
	m := New(cfg)
	global = m
	plugin.SetDispatcher(m)
	return m
}

// Global 返回全局 Manager。
func Global() *Manager { return global }

// ---- 加载与卸载 ----

// LoadSpec 加载一个插件所需的信息。
type LoadSpec struct {
	// Name 插件标识。
	Name string
	// Dir 插件目录。
	Dir string
	// Manifest 已解析的清单。
	Manifest *plugin.Manifest
}

// Load 加载（或重载）一个插件：读清单、拉起进程、完成 init 握手。
//
// 若同名插件已在运行，会先停止旧的再拉新的 —— 这是"重载"的语义。
// 任一步失败都不会污染 registry：失败时实例被移除并返回 error。
func (m *Manager) Load(spec LoadSpec) error {
	if spec.Manifest == nil {
		return errors.New("manifest 为空")
	}
	if spec.Manifest.Name != spec.Name {
		return fmt.Errorf("manifest.name %q 与插件标识 %q 不一致",
			spec.Manifest.Name, spec.Name)
	}

	// 卸载同名旧实例（若有）
	if old := m.Get(spec.Name); old != nil {
		m.Unload(spec.Name)
	}

	dataDir := fmt.Sprintf("%s/plugin-data/%s", m.cfg.DataDir, spec.Name)
	if err := ensureDir(dataDir, 0o750); err != nil {
		return fmt.Errorf("创建插件数据目录失败: %w", err)
	}

	in := &Instance{
		Name:     spec.Name,
		Manifest: spec.Manifest,
		Dir:      spec.Dir,
		DataDir:  dataDir,
		status:   StatusStopped,
		pending:  map[int64]chan *plugin.RPCResponse{},
	}

	m.mu.Lock()
	m.insts[spec.Name] = in
	m.mu.Unlock()

	m.appendLog(in.Name, "info", fmt.Sprintf("正在启动（entry=%s protocol=%d）",
		spec.Manifest.Entry, spec.Manifest.ProtocolVersion))

	if err := m.start(in); err != nil {
		in.mu.Lock()
		in.status = StatusFailed
		in.lastErr = err.Error()
		in.mu.Unlock()
		m.appendLog(in.Name, "error", "启动失败: "+err.Error())
		// 启动失败：移出 registry，避免污染
		m.mu.Lock()
		delete(m.insts, spec.Name)
		m.mu.Unlock()
		return err
	}
	return nil
}

// start 拉起子进程并完成 init 握手。
func (m *Manager) start(in *Instance) error {
	execPath, err := in.Manifest.ExecPath(in.Dir)
	if err != nil {
		return err
	}

	cmd := exec.Command(execPath, in.Manifest.Args...)
	cmd.Dir = in.Dir
	// 插件日志走 stderr，stdout 专供 RPC —— 两者混在一起会破坏分帧。
	cmd.Stderr = &logWriter{m: m, plugin: in.Name}
	// 独立进程组：Stop 时能连带杀掉插件自己拉起的孙进程
	cmd.SysProcAttr = sysProcAttr()

	env := append(stdenv(), "NEBULA_PLUGIN_NAME="+in.Name,
		"NEBULA_PLUGIN_DATA="+in.DataDir)
	for k, v := range in.Manifest.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("创建 stdin 管道失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("创建 stdout 管道失败: %w", err)
	}

	in.mu.Lock()
	in.status = StatusStarting
	in.stopping = false
	in.restarts = 0
	in.firstCr = time.Time{}
	in.mu.Unlock()

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("启动进程失败: %w", err)
	}

	in.mu.Lock()
	in.cmd = cmd
	in.stdin = stdin
	in.pid = cmd.Process.Pid
	in.status = StatusRunning
	in.mu.Unlock()

	// 读 stdout 的 goroutine：负责分帧并把响应派发给等待方
	go m.readLoop(in, stdout)

	// init 握手
	initParams := plugin.InitParams{
		HostVersion:        m.cfg.HostVersion,
		HostProtocol:       plugin.ProtocolVersion,
		PluginName:         in.Name,
		DataDir:            in.DataDir,
		StartTimeoutMillis: int(m.cfg.StartTimeout / time.Millisecond),
		SiteURL:            m.cfg.SiteURL,
	}
	if m.cfg.ConfigFor != nil {
		initParams.Config = m.cfg.ConfigFor(in.Name)
	}
	resp, err := in.Call(plugin.MethodInit, initParams, m.cfg.StartTimeout)
	if err != nil {
		m.kill(in, "init 握手失败: "+err.Error())
		return err
	}
	if resp.Error != nil {
		m.kill(in, "init 返回错误: "+resp.Error.Error())
		return fmt.Errorf("init 失败: %s", resp.Error.Error())
	}
	var ir plugin.InitResult
	if err := decodeResult(resp, &ir); err != nil {
		m.kill(in, "init 结果解析失败: "+err.Error())
		return err
	}
	if !ir.OK {
		msg := ir.Error
		if msg == "" {
			msg = "插件报告初始化失败但未给出原因"
		}
		m.kill(in, "init 不成功: "+msg)
		return errors.New(msg)
	}

	// 监视进程退出
	go m.waitProc(in)

	m.appendLog(in.Name, "info", fmt.Sprintf("已启动 pid=%d 声明钩子=%v",
		in.pid, ir.Hooks))
	return nil
}

// readLoop 读取 stdout 并按行分帧。
//
// 插件若把非 JSON 内容写进 stdout，这里会记一条警告并丢弃该行
// （而不是直接崩掉读循环）—— 插件作者误用 stdout 是常见错误。
func (m *Manager) readLoop(in *Instance, stdout io.ReadCloser) {
	defer stdout.Close()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20) // 单行上限 8MB
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp plugin.RPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			m.appendLog(in.Name, "warn",
				"stdout 收到非 JSON 行（插件应把日志写到 stderr）: "+
					plugin.TruncateLog(string(line)))
			continue
		}
		in.pendMu.Lock()
		ch, ok := in.pending[resp.ID]
		if ok {
			delete(in.pending, resp.ID)
		}
		in.pendMu.Unlock()
		if ok {
			// 带缓冲：等待方可能已超时返回，send 不能阻塞读循环
			select {
			case ch <- &resp:
			default:
			}
		}
	}
	if err := sc.Err(); err != nil && !isClosedErr(err) {
		m.appendLog(in.Name, "warn", "读取插件 stdout 失败: "+err.Error())
	}
	// 管道关闭 = 进程已退出。等待方立刻拿到错误而不是干等到超时。
	in.pendMu.Lock()
	for id, ch := range in.pending {
		select {
		case ch <- &plugin.RPCResponse{
			ID:    id,
			Error: &plugin.RPCError{Code: plugin.CodeInternalError, Message: "plugin process exited"},
		}:
		default:
		}
		delete(in.pending, id)
	}
	in.pendMu.Unlock()
}

// waitProc 等待进程退出，崩溃时按退避策略重启。
func (m *Manager) waitProc(in *Instance) {
	in.mu.Lock()
	cmd := in.cmd
	in.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	err := cmd.Wait()

	in.mu.Lock()
	stopping := in.stopping
	pid := in.pid
	in.mu.Unlock()

	if stopping {
		return
	}

	exitDesc := describeExit(err)
	in.mu.Lock()
	now := time.Now()
	if in.firstCr.IsZero() || now.Sub(in.firstCr) > crashWindow {
		in.firstCr = now
		in.restarts = 0
	}
	in.restarts++
	restarts := in.restarts
	// 进程已死，管道不能再用
	in.stdin = nil
	in.pid = 0
	if restarts >= maxRestarts {
		in.status = StatusCrashed
		in.lastErr = fmt.Sprintf("连续崩溃 %d 次，已停止自动重启: %s", restarts, exitDesc)
	} else {
		in.status = StatusCrashed
		in.lastErr = exitDesc
	}
	in.mu.Unlock()

	m.appendLog(in.Name, "error", fmt.Sprintf("进程退出（pid=%d）: %s", pid, exitDesc))

	if restarts >= maxRestarts || !m.cfg.AutoRestart {
		return
	}

	delay := backoff(restarts)
	m.appendLog(in.Name, "warn", fmt.Sprintf("将在 %s 后第 %d 次重启", delay, restarts))
	select {
	case <-time.After(delay):
	case <-m.closing:
		return
	}

	// 重启前确认未被卸载、宿主未关闭
	if m.Get(in.Name) == nil {
		return
	}
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return
	}
	if err := m.start(in); err != nil {
		m.appendLog(in.Name, "error", "自动重启失败: "+err.Error())
	}
}

// Unload 停止并移除一个插件。
func (m *Manager) Unload(name string) {
	m.mu.Lock()
	in, ok := m.insts[name]
	if ok {
		delete(m.insts, name)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	m.kill(in, "卸载")
	m.appendLog(name, "info", "已卸载")
}

// Reload 重载插件（重读 manifest 并重启）。
//
// 用于插件升级后生效：调用方负责重新解析 manifest。
func (m *Manager) Reload(spec LoadSpec) error {
	return m.Load(spec)
}

// StopAll 停止全部插件。main.go 退出时调用。
func (m *Manager) StopAll() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.closing) // 唤醒所有等待自动重启的 goroutine
	names := make([]string, 0, len(m.insts))
	for n := range m.insts {
		names = append(names, n)
	}
	m.mu.Unlock()
	for _, n := range names {
		if in := m.Get(n); in != nil {
			m.kill(in, "宿主退出")
		}
	}
}

// Get 返回实例。
func (m *Manager) Get(name string) *Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.insts[name]
}

// List 返回全部实例的状态。
func (m *Manager) List() []StatusInfo {
	m.mu.RLock()
	names := make([]string, 0, len(m.insts))
	for n := range m.insts {
		names = append(names, n)
	}
	m.mu.RUnlock()

	out := make([]StatusInfo, 0, len(names))
	for _, n := range names {
		if in := m.Get(n); in != nil {
			out = append(out, in.Status())
		}
	}
	return out
}

// Total 返回已加载的进程外插件数。
func (m *Manager) Total() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.insts)
}

// Count 返回某钩子上的进程外插件数。
func (m *Manager) Count(h plugin.HookName) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, in := range m.insts {
		if in.Manifest.HookSet()[h] {
			n++
		}
	}
	return n
}

// ---- 派发（实现 plugin.Dispatcher）----

// Dispatch 按钩子既定模式派发（见 plugin.HookMeta.Mode）。
func (m *Manager) Dispatch(h plugin.HookName, ctx map[string]any) []error {
	if plugin.HookModeOf(h) == plugin.ModeAsync {
		m.dispatchAsync(h, ctx)
		return nil
	}
	return m.dispatchSync(h, ctx)
}

// DispatchSync 强制同步等待全部插件返回。
//
// 目前只有 onRateLimit 用这个入口：它整体是异步钩子（7 个调用点里
// 6 个不读结果），但限流中间件那一处必须做拦截决策。
func (m *Manager) DispatchSync(h plugin.HookName, ctx map[string]any) []error {
	return m.dispatchSync(h, ctx)
}

// dispatchSync 串行等待每个插件，把 ctx 变更与 error 逐个带回。
//
// 用串行而非并行：后一个插件应能看到前一个的写入（与进程内 Fire 的
// 顺序语义一致），且插件数量少（通常 0-2 个），串行的延迟可接受。
func (m *Manager) dispatchSync(h plugin.HookName, ctx map[string]any) []error {
	targets := m.targets(h)
	if len(targets) == 0 {
		return nil
	}

	var errs []error
	// 逐个 ctx 副本串行：后面的插件要能看到前面插件的写入，
	// 且任一插件的写入都要能回传给业务。
	work := make(map[string]any, len(ctx))
	for k, v := range ctx {
		work[k] = v
	}

	for _, in := range targets {
		if !in.IsLive() {
			continue
		}
		seq := atomic.AddInt64(&in.seq, 1)
		res, err := in.Call(plugin.MethodFire,
			plugin.FireParams{Hook: h, Ctx: work, Seq: seq}, m.cfg.FireTimeout)
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", in.Name, err))
			continue
		}
		var fr plugin.FireResult
		if err := decodeResult(res, &fr); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s 结果解析失败: %w", in.Name, err))
			continue
		}
		for _, l := range fr.Log {
			level := string(l.Level)
			if level == "" {
				level = "info"
			}
			m.appendLog(in.Name, level, l.Message)
		}
		mergeCtx(work, fr.CtxPatch)
		if fr.Error != "" {
			errs = append(errs, fmt.Errorf("plugin %s: %s", in.Name, fr.Error))
		}
	}

	// 把插件的写入回传给业务方的 ctx（CollabOpen 依赖这个）
	for k, v := range work {
		ctx[k] = v
	}
	return errs
}

// dispatchAsync 异步派发，仅事件通知。
func (m *Manager) dispatchAsync(h plugin.HookName, ctx map[string]any) {
	targets := m.targets(h)
	if len(targets) == 0 {
		return
	}
	// ctx 是业务方还要继续用的 map，异步不能直接传 —— 深拷贝一份。
	snapshot := make(map[string]any, len(ctx))
	for k, v := range ctx {
		snapshot[k] = v
	}
	for _, in := range targets {
		if !in.IsLive() {
			continue
		}
		seq := atomic.AddInt64(&in.seq, 1)
		go func(in *Instance, seq int64) {
			// 独立 ctx 副本：并发插件之间不能互相污染
			local := make(map[string]any, len(snapshot))
			for k, v := range snapshot {
				local[k] = v
			}
			res, err := in.Call(plugin.MethodFire,
				plugin.FireParams{Hook: h, Ctx: local, Seq: seq}, m.cfg.FireTimeout)
			if err != nil {
				m.appendLog(in.Name, "warn", "异步 "+string(h)+" 失败: "+err.Error())
				return
			}
			var fr plugin.FireResult
			if err := decodeResult(res, &fr); err != nil {
				return
			}
			for _, l := range fr.Log {
				level := string(l.Level)
				if level == "" {
					level = "info"
				}
				m.appendLog(in.Name, level, l.Message)
			}
			if fr.Error != "" {
				m.appendLog(in.Name, "warn", string(h)+": "+fr.Error)
			}
		}(in, seq)
	}
}

// targets 返回声明了该钩子的存活实例。
func (m *Manager) targets(h plugin.HookName) []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Instance
	for _, in := range m.insts {
		if in.Manifest.HookSet()[h] {
			out = append(out, in)
		}
	}
	return out
}

// ---- IPC 调用 ----

// Call 发起一次同步 RPC 并等待响应。
func (in *Instance) Call(method string, params any, timeout time.Duration) (*plugin.RPCResponse, error) {
	in.mu.Lock()
	if in.status != StatusRunning || in.stdin == nil {
		st := in.status
		in.mu.Unlock()
		return nil, fmt.Errorf("plugin not running (status=%s)", st)
	}
	w := in.stdin
	in.mu.Unlock()

	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("序列化参数失败: %w", err)
	}

	in.pendMu.Lock()
	in.nextID++
	id := in.nextID
	ch := make(chan *plugin.RPCResponse, 1)
	in.pending[id] = ch
	in.pendMu.Unlock()

	defer func() {
		in.pendMu.Lock()
		delete(in.pending, id)
		in.pendMu.Unlock()
	}()

	req := plugin.RPCRequest{
		JSONRPC: plugin.RPCVersion,
		ID:      id,
		Method:  method,
		Params:  raw,
	}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 加锁序列化写：多个并发 Call 共享同一条 stdin，
	// 不加锁会把两行 JSON 交错写进管道，插件必然解析失败。
	in.writeMu.Lock()
	_, werr := w.Write(append(line, '\n'))
	in.writeMu.Unlock()
	if werr != nil {
		return nil, fmt.Errorf("写入插件 stdin 失败: %w", werr)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp == nil {
			return nil, errors.New("plugin closed the connection")
		}
		return resp, nil
	case <-timer.C:
		return nil, fmt.Errorf("%s 超时（%s）", method, timeout)
	}
}

// Shutdown 礼貌地通知插件退出。
func (in *Instance) Shutdown(timeout time.Duration) {
	if !in.IsLive() {
		return
	}
	// 超时短一点：这是清理路径，不能拖住 Stop
	if _, err := in.Call(plugin.MethodShutdown, map[string]any{}, timeout); err != nil {
		return
	}
}

// ---- 内部工具 ----

// kill 停止插件进程。
func (m *Manager) kill(in *Instance, reason string) {
	in.mu.Lock()
	in.stopping = true
	cmd := in.cmd
	stdin := in.stdin
	in.stdin = nil
	in.cmd = nil
	in.pid = 0
	in.status = StatusStopped
	in.stopping = false
	in.mu.Unlock()

	if stdin != nil {
		// 关 stdin 让插件的读循环自然结束
		stdin.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return
	}
	// 先礼后兵：先 SIGTERM/kill，短暂等待，再强杀
	_ = terminate(cmd)
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = killTree(cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

func (m *Manager) appendLog(pluginName, level, msg string) {
	entry := LogEntry{
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Level:   level,
		Plugin:  pluginName,
		Message: plugin.TruncateLog(msg),
	}
	m.logMu.Lock()
	buf := append(m.logs[pluginName], entry)
	if len(buf) > logBufferSize {
		buf = buf[len(buf)-logBufferSize:]
	}
	m.logs[pluginName] = buf
	m.logMu.Unlock()
	m.cfg.Log("[plugin:%s] %s", pluginName, msg)
}

// Logs 返回某插件的日志（name 为空则返回全部）。
func (m *Manager) Logs(name string, limit int) []LogEntry {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	var out []LogEntry
	if name == "" {
		for _, v := range m.logs {
			out = append(out, v...)
		}
	} else {
		out = append(out, m.logs[name]...)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// logWriter 把子进程 stderr 收进日志缓冲。
type logWriter struct {
	m      *Manager
	plugin string
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.m.appendLog(w.plugin, "info", string(p))
	return len(p), nil
}

func mergeCtx(dst, patch map[string]any) {
	for k, v := range patch {
		dst[k] = v
	}
}

func decodeResult(resp *plugin.RPCResponse, out any) error {
	if resp.Error != nil {
		return resp.Error
	}
	if len(resp.Result) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

func backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	d := restartBaseDelay
	for i := 1; i < n && d < restartMaxDelay; i++ {
		d *= 2
	}
	if d > restartMaxDelay {
		d = restartMaxDelay
	}
	return d
}

// describeExit 把 Wait 的返回值转成人能读的描述。
func describeExit(err error) string {
	if err == nil {
		return "正常退出（exit 0）"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code >= 0 {
			return "退出码 " + strconv.Itoa(code)
		}
		// 负值代表被信号杀死
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return "被信号 " + ws.Signal().String() + " 终止"
		}
		return "异常退出: " + err.Error()
	}
	return "等待进程失败: " + err.Error()
}
