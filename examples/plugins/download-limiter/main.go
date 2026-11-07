// Command download-limiter 是一个 NebulaDrive 插件：按客户端 IP 限制下载频率，
// 用来挡住"批量抓取整个网盘"这类行为。
//
// 它实现两个钩子：
//
//  1. onAntiLeech（同步、可拦截）
//     登录下载：ctx={userId,userName,ip,ua,fileId,fileName,referer}
//     公开分享：ctx={userId(恒为0),userName(恒为空),ip,ua,fileId,fileName,
//     referer,shareId,shareKind,shareOwnerId}，shareKind 为 download 或 preview。
//     超限时返回 {"error":"blocked by download-limiter: ..."}，宿主据此返回 403。
//
//  2. onCLI（同步）
//     `nebula plugin download-limiter` 会把当前窗口计数（top 10 IP）写进
//     ctxPatch.cliOutput，由宿主打印到命令行。
//
// # 配置
//
//	{
//	  "maxPerMinute": 30,
//	  "maxPerHour": 600,
//	  "exemptUserIds": [1, 2],
//	  "countSharePreview": false,
//	  "maxTrackedIPs": 10000
//	}
//
// 字段缺失或为 0（含负数）时使用默认值：60 / 1200 / [] / false / 10000。
//
// # 几个刻意的设计
//
//   - **滑动窗口，而不是"计数器 + 定时清零"。** 固定窗口在边界上有 2 倍
//     突刺：第 59 秒打满 N 次、第 61 秒再打满 N 次，实际 2 秒内过了 2N 次。
//     本插件给每个 IP 保存最近 1 分钟与 1 小时的时间戳队列，每次请求前把
//     早于窗口的时间戳真正丢弃，所以任意 60 秒区间内的放行次数都不超过
//     maxPerMinute。代价是每个 IP 最多保留 maxPerMinute+maxPerHour 个时间戳，
//     这也是 maxTrackedIPs 上限存在的第二个理由。
//
//   - **跟踪 IP 数必须有硬上限。** 攻击者可以伪造海量源 IP（或走代理池），
//     每个 IP 只打一两次。没有上限时，限流器为每个新 IP 建一份状态，
//     内存随攻击流量线性增长 —— 限流器本身成了 DoS 面。超过 maxTrackedIPs
//     时淘汰最久未活动的 IP（近似 LRU）。淘汰意味着它的窗口被重置，
//     这是有意的取舍：内存有界 > 对无限 IP 池做精确记账，后者不可能赢。
//
//   - **只在拦截时写日志。** 下载是高频操作，放行日志会瞬间填满宿主给每个
//     插件的 500 条环形缓冲，把真正有用的拦截记录挤出去。所以放行路径完全
//     静默，拦截时打一行带 IP 与计数的 warn。
//
//   - **取不到 IP 时放行且不计数。** 宁可少拦，也不要因为宿主某个调用点
//     没传 ip 就把所有下载拦死（那是一次全站故障，不是防护）。
//
//   - **时间可注入。** limiter.now 默认 time.Now，测试里换成假时钟，
//     窗口逻辑不需要靠 sleep 验证。
//
//   - **并发安全。** onAntiLeech 会被并发的 HTTP 请求触发；所有共享状态都在
//     limiter.mu 保护下。持锁期间只做纯内存操作，没有任何 I/O。
//
// 构建：go build -o download-limiter .
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- 协议结构（与 pkg/plugin 保持一致）----
//
// 插件不 import 宿主包：宿主与插件是独立进程，import 宿主会把
// 整个后端拖进依赖图，也会造成版本耦合。协议结构在此处独立声明。

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      int64     `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type initParams struct {
	HostVersion        string          `json:"hostVersion"`
	HostProtocol       int             `json:"hostProtocol"`
	PluginName         string          `json:"pluginName"`
	DataDir            string          `json:"dataDir"`
	Config             json.RawMessage `json:"config"`
	StartTimeoutMillis int             `json:"startTimeoutMillis"`
}

type initResult struct {
	OK      bool     `json:"ok"`
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Hooks   []string `json:"hooks"`
	Error   string   `json:"error,omitempty"`
}

type fireParams struct {
	Hook string         `json:"hook"`
	Ctx  map[string]any `json:"ctx"`
	Seq  int64          `json:"seq"`
}

type fireResult struct {
	CtxPatch map[string]any `json:"ctxPatch,omitempty"`
	Error    string         `json:"error,omitempty"`
}

type logLine struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Time    int64  `json:"time"`
}

const (
	// pluginName 同时是 plugin.json 的 name 与 onCLI 的响应命令名。
	pluginName = "download-limiter"
	version    = "1.0.0"

	hookAntiLeech = "onAntiLeech"
	hookCLI       = "onCLI"

	// 两个窗口的长度。用常量而不是散落的 time.Minute，避免某处写成 60s、
	// 另一处写成 1m 导致窗口与文案不一致。
	minuteWindow = time.Minute
	hourWindow   = time.Hour

	// cliTopN 是 CLI 统计只展示前 N 个 IP。
	cliTopN = 10
)

// Config 插件配置，字段与文件头示例一一对应。
type Config struct {
	// MaxPerMinute 单个 IP 任意 60 秒内允许的下载次数。
	MaxPerMinute int `json:"maxPerMinute"`
	// MaxPerHour 单个 IP 任意 60 分钟内允许的下载次数。
	MaxPerHour int `json:"maxPerHour"`
	// ExemptUserIDs 免限流的登录用户 ID。命中者直接放行且**不计数**。
	ExemptUserIDs []uint64 `json:"exemptUserIds"`
	// CountSharePreview 是否把公开分享的 preview 也计入下载次数。
	// 默认 false：预览是本站分享页的内联展示，和批量下载不是一回事；
	// 但 preview 确实也是完整文件出口，觉得被滥用时把它打开。
	CountSharePreview bool `json:"countSharePreview"`
	// MaxTrackedIPs 最多同时跟踪多少个 IP。超过后淘汰最久未活动的 IP。
	// 这是内存上限，不是性能调优项（见文件头）。
	MaxTrackedIPs int `json:"maxTrackedIPs"`
}

// defaultConfig 返回全部默认值。
func defaultConfig() Config {
	return Config{
		MaxPerMinute: 60,
		MaxPerHour:   1200,
		// 非 nil 的空切片让 JSON 输出稳定为 []；nil 会序列化成 null。
		ExemptUserIDs:     []uint64{},
		CountSharePreview: false,
		MaxTrackedIPs:     10000,
	}
}

// withDefaults 只填"没填"（<=0）的数值项，不覆盖管理员显式配置。
//
// 不在这里做区间校验：maxPerMinute 配成 10 亿也只是等于不限流，
// 不是错误配置；而配成 0 的语义是"没填"，用默认值。
func (c Config) withDefaults() Config {
	d := defaultConfig()
	if c.MaxPerMinute <= 0 {
		c.MaxPerMinute = d.MaxPerMinute
	}
	if c.MaxPerHour <= 0 {
		c.MaxPerHour = d.MaxPerHour
	}
	if c.MaxTrackedIPs <= 0 {
		c.MaxTrackedIPs = d.MaxTrackedIPs
	}
	if c.ExemptUserIDs == nil {
		c.ExemptUserIDs = []uint64{}
	}
	return c
}

// ipState 是单个 IP 的窗口状态。
//
// 两个队列都按时间升序追加（now 单调递增时 append 即有序），
// 每次判定前从头部丢弃过期时间戳 —— 这就是"滑动"的来源：
// 窗口跟着当前时刻走，而不是跟着某个固定周期清零。
type ipState struct {
	minute   []time.Time
	hour     []time.Time
	lastSeen time.Time
}

// prune 丢弃两个窗口中已经过期的时间戳。
func (s *ipState) prune(now time.Time) {
	s.minute = pruneBefore(s.minute, now.Add(-minuteWindow))
	s.hour = pruneBefore(s.hour, now.Add(-hourWindow))
}

// pruneBefore 返回只保留 t > cutoff 的切片。
//
// 用 append(ts[:0], ts[i:]...) 原地前移而不是 ts[i:]：后者会让整个底层数组
// 一直可达，已过期的 time.Time 无法被回收。
func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	if i == 0 {
		return ts
	}
	return append(ts[:0], ts[i:]...)
}

// limiter 是全部运行时状态。
//
// 除 cfg / exempt / now 在构造后只读外，ips 与其中的 ipState 都受 mu 保护。
type limiter struct {
	cfg    Config
	exempt map[uint64]bool
	// now 是可注入的时钟。测试用假时钟推进窗口，生产用 time.Now。
	now func() time.Time

	mu  sync.Mutex
	ips map[string]*ipState
}

// newLimiter 构造限流器。cfg 在这里补齐默认值，now 为 nil 时用 time.Now。
func newLimiter(cfg Config, now func() time.Time) *limiter {
	cfg = cfg.withDefaults()
	if now == nil {
		now = time.Now
	}
	l := &limiter{
		cfg:    cfg,
		exempt: make(map[uint64]bool, len(cfg.ExemptUserIDs)),
		now:    now,
		ips:    make(map[string]*ipState),
	}
	for _, id := range cfg.ExemptUserIDs {
		l.exempt[id] = true
	}
	return l
}

// check 判定一次下载请求。
//
// 返回 (是否拦截, 拦截文案)。文案已带 "blocked by download-limiter: " 前缀，
// 宿主只认小写 "blocked" 子串，这里保证唯一一处拼装，避免某个分支漏掉前缀。
func (l *limiter) check(ctx map[string]any) (bool, string) {
	// 1. 没有 IP 就不限流。
	//
	// 取不到客户端 IP 时无法归属请求；如果因此拦截，宿主某个调用点漏传 ip
	// 就会变成全站下载故障。宁可漏拦。
	ip := strings.TrimSpace(str(ctx["ip"]))
	if ip == "" {
		return false, ""
	}

	// 2. 登录用户豁免。豁免必须发生在计数之前，否则"豁免"只是"不拦"，
	//    它照样会把窗口打满，连带影响同 IP 的其他匿名访问者（NAT 场景）。
	//
	// 类型注意：ctx 经 JSON 往返后数字是 float64，断言 int/uint 会静默失败。
	if id, ok := numOf(ctx["userId"]); ok && id > 0 && l.exempt[id] {
		return false, ""
	}

	// 3. 分享预览豁免（默认）。preview 也是完整文件内容，但它是分享页的
	//    内联展示，限流意义与批量下载不同；需要保护时把 countSharePreview
	//    打开即可，无需改代码。
	if !l.cfg.CountSharePreview && str(ctx["shareKind"]) == "preview" {
		return false, ""
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.ips[ip]
	if st == nil {
		// 新 IP：先腾位置再插入，保证插入后 len(ips) <= MaxTrackedIPs。
		//
		// 顺序很重要：若先插入再淘汰，当所有 IP 的 lastSeen 相同（时钟精度
		// 低或同一毫秒内涌入）时可能把刚建好的状态淘汰掉，新 IP 的计数
		// 就凭空丢了。
		if len(l.ips) >= l.cfg.MaxTrackedIPs {
			l.evictOldestLocked()
		}
		st = &ipState{}
		l.ips[ip] = st
	}

	// 每次判定前真正淘汰过期时间戳。不这么做就退化成固定窗口。
	st.prune(now)
	st.lastSeen = now

	// 4. 判定。被拦截的请求不写入时间戳：它没有产生下载，
	//    记进去会让"持续重试"永久占住窗口，反而延长封禁。
	if n := len(st.minute); n >= l.cfg.MaxPerMinute {
		// 报告 n+1：本次尝试也算一次，读起来才是"你正在第 n+1 次"。
		return true, fmt.Sprintf(
			"blocked by download-limiter: 1 分钟内下载 %d 次，超过上限 %d（IP %s）",
			n+1, l.cfg.MaxPerMinute, ip)
	}
	if n := len(st.hour); n >= l.cfg.MaxPerHour {
		return true, fmt.Sprintf(
			"blocked by download-limiter: 1 小时内下载 %d 次，超过上限 %d（IP %s）",
			n+1, l.cfg.MaxPerHour, ip)
	}

	st.minute = append(st.minute, now)
	st.hour = append(st.hour, now)
	return false, ""
}

// evictOldestLocked 淘汰最久未活动的 IP。调用方必须持有 mu。
//
// 线性扫描 map：仅在"新 IP 且已达上限"时触发一次，10k 规模的扫描是微秒级，
// 比维护额外的 LRU 链表更不容易出错。不要为此在每次请求都扫描。
func (l *limiter) evictOldestLocked() {
	var oldestIP string
	var oldest time.Time
	for ip, st := range l.ips {
		if oldestIP == "" || st.lastSeen.Before(oldest) {
			oldestIP, oldest = ip, st.lastSeen
		}
	}
	if oldestIP != "" {
		delete(l.ips, oldestIP)
	}
}

// ipStat 是 CLI 展示用的一行统计。
type ipStat struct {
	IP       string
	Minute   int
	Hour     int
	LastSeen time.Time
}

// snapshot 复制当前各 IP 的窗口计数，按"1 分钟计数降序、1 小时计数降序、
// 最近活动降序、IP 升序"排序，保证命令行输出稳定可 diff。
//
// 锁内只复制数据，排序与格式化都在锁外做：虽然这里没有 I/O，
// 但让锁的临界区尽量短是并发代码的基本纪律。
func (l *limiter) snapshot(now time.Time) []ipStat {
	l.mu.Lock()
	out := make([]ipStat, 0, len(l.ips))
	for ip, st := range l.ips {
		st.prune(now)
		if len(st.minute) == 0 && len(st.hour) == 0 {
			// 窗口已空：对统计没有意义，跳过。
			continue
		}
		out = append(out, ipStat{
			IP:       ip,
			Minute:   len(st.minute),
			Hour:     len(st.hour),
			LastSeen: st.lastSeen,
		})
	}
	l.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Minute != out[j].Minute {
			return out[i].Minute > out[j].Minute
		}
		if out[i].Hour != out[j].Hour {
			return out[i].Hour > out[j].Hour
		}
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].IP < out[j].IP
	})
	return out
}

// cliOutput 生成 `nebula plugin download-limiter` 的输出。
//
// 前缀写死 download-limiter：多个插件可能同时响应同一命令，
// 宿主的 cliOutput 是后写覆盖，没有前缀就分不清是谁的统计。
func (l *limiter) cliOutput() string {
	now := l.now()
	stats := l.snapshot(now)

	var b strings.Builder
	fmt.Fprintf(&b, "%s 下载频率统计（跟踪 IP %d/%d；1 分钟上限 %d，1 小时上限 %d）\n",
		pluginName, l.trackedIPs(), l.cfg.MaxTrackedIPs,
		l.cfg.MaxPerMinute, l.cfg.MaxPerHour)
	if len(stats) == 0 {
		b.WriteString("  当前没有窗口内的下载计数\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  %-4s %-18s %-8s %-8s %s\n", "#", "IP", "1 分钟", "1 小时", "最近活动")
	for i, s := range stats {
		if i >= cliTopN {
			break
		}
		fmt.Fprintf(&b, "  %-4d %-18s %-8d %-8d %s\n",
			i+1, s.IP, s.Minute, s.Hour, s.LastSeen.Format("2006-01-02 15:04:05"))
	}
	if len(stats) > cliTopN {
		fmt.Fprintf(&b, "  ... 另有 %d 个 IP 未展示\n", len(stats)-cliTopN)
	}
	return b.String()
}

// trackedIPs 返回当前状态表大小。
func (l *limiter) trackedIPs() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.ips)
}

// isTracked 报告某 IP 是否还有状态。仅供测试断言内存上限与淘汰行为。
func (l *limiter) isTracked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.ips[ip]
	return ok
}

// totalCount 返回所有 IP 在 1 分钟窗口内的计数之和。
// 仅供测试使用（生产读路径走 snapshot），绝不参与限流判定。
func (l *limiter) totalCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, st := range l.ips {
		n += len(st.minute)
	}
	return n
}

// ---- 主循环 ----

// g 是当前生效的限流器。只会在 init 时被替换一次，而 RPC 主循环是串行的，
// 因此不需要额外加锁（init 一定先于任何 fire 到达）。
var g *limiter

// outMu 让响应写入串行化：宿主通常串行调用，但 fire 的并发语义值得自保，
// 否则两行 JSON 可能交错写进 stdout，宿主侧会解析失败。
var outMu sync.Mutex

func main() {
	sc := bufio.NewScanner(os.Stdin)
	// 上限 8MB：正常 RPC 行只有几百字节，留余量给宿主未来加的字段。
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			// 宿主写出的行必须是合法 JSON；解析不了说明协议层有问题，
			// 但插件不能因此退出（否则宿主看不到原因）。
			logf("error", "无法解析请求: %v", err)
			continue
		}
		switch req.Method {
		case "init":
			handleInit(req)
		case "fire":
			handleFire(req)
		case "ping":
			reply(req, map[string]any{"pong": true}, nil)
		case "shutdown":
			reply(req, map[string]any{"bye": true}, nil)
			return
		default:
			replyErr(req, -32601, "不支持的方法: "+req.Method)
		}
	}
}

func handleInit(req request) {
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		reply(req, initResult{OK: false, Error: "init 参数解析失败: " + err.Error()}, nil)
		return
	}
	if p.HostProtocol != 1 {
		// 明确失败比"勉强跑起来"好：协议不匹配时静默降级会出难查的问题。
		reply(req, initResult{OK: false,
			Error: fmt.Sprintf("协议版本不匹配：宿主 %d，插件只支持 1", p.HostProtocol)}, nil)
		return
	}

	var cfg Config
	if len(p.Config) > 0 {
		// 配置非法时退回默认而不是拒绝启动：插件不该因为配置笔误
		// 就完全不可用。但要在日志里说清楚。
		if err := json.Unmarshal(p.Config, &cfg); err != nil {
			logf("warn", "配置解析失败，使用默认配置: %v", err)
			cfg = Config{}
		}
	}
	g = newLimiter(cfg, time.Now)

	reply(req, initResult{
		OK:      true,
		Name:    pluginName,
		Version: version,
		Hooks:   []string{hookAntiLeech, hookCLI},
	}, nil)
}

func handleFire(req request) {
	var p fireParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		replyErr(req, -32602, "fire 参数解析失败: "+err.Error())
		return
	}

	switch p.Hook {
	case hookAntiLeech:
		l := g
		if l == nil {
			// 未经 init 就被调用：放行，不猜测配置。
			reply(req, fireResult{}, nil)
			return
		}
		blocked, msg := l.check(p.Ctx)
		if blocked {
			// 只在拦截时记一行：放行日志会淹没宿主 500 条环形缓冲。
			logf("warn", "拦截下载: %s", msg)
			reply(req, fireResult{Error: msg}, nil)
			return
		}
		reply(req, fireResult{}, nil)

	case hookCLI:
		// 命令名不是本插件时保持安静：同一命令可能有多插件响应，
		// 这里不该写 cliOutput（后写会覆盖别人的输出）。
		if strings.TrimSpace(str(p.Ctx["command"])) != pluginName {
			reply(req, fireResult{}, nil)
			return
		}
		out := pluginName + ": 插件尚未初始化，暂无计数\n"
		if g != nil {
			out = g.cliOutput()
		}
		reply(req, fireResult{CtxPatch: map[string]any{"cliOutput": out}}, nil)

	default:
		// 宿主只派发 manifest 里声明过的钩子；多派时回空结果即可。
		reply(req, fireResult{}, nil)
	}
}

// ---- 协议输出 ----

func reply(req request, result any, rerr *rpcError) {
	out := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
	b, err := json.Marshal(out)
	if err != nil {
		logf("error", "序列化响应失败: %v", err)
		return
	}
	b = append(b, '\n')
	// stdout 只允许出现 RPC 行，日志一律 stderr。
	outMu.Lock()
	os.Stdout.Write(b)
	outMu.Unlock()
}

func replyErr(req request, code int, msg string) {
	reply(req, nil, &rpcError{Code: code, Message: msg})
}

func logf(level, format string, args ...any) {
	// 日志必须走 stderr：stdout 是 RPC 专用通道。
	rec := logLine{
		Level:   level,
		Message: fmt.Sprintf(format, args...),
		Time:    time.Now().UnixMilli(),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", level, fmt.Sprintf(format, args...))
		return
	}
	os.Stderr.Write(append(b, '\n'))
}

// ---- 工具 ----

// str 把 ctx 字段安全地转成字符串。
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// numOf 把 ctx 里的数字字段转成 uint64。
//
// ctx 是 map[string]any，经 JSON 反序列化后数字全是 float64。
// 直接写 ctx["userId"].(uint) 会永远失败 —— 这类"静默不生效"的 bug
// 极难察觉：代码没报错，豁免就是不工作。
func numOf(v any) (uint64, bool) {
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case int64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case uint64:
		return n, true
	case uint:
		return uint64(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil && i >= 0 {
			return uint64(i), true
		}
	}
	return 0, false
}
