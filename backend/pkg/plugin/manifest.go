package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ManifestFileName 插件目录内必须存在的清单文件名。
const ManifestFileName = "plugin.json"

// MaxManifestSize 清单文件大小上限（64KB）。
// 清单是人写的配置文件，不是二进制 —— 超限基本是误操作或恶意输入。
const MaxManifestSize = 64 << 10

// MaxHooksPerPlugin 单个插件可声明的钩子数上限。
const MaxHooksPerPlugin = 32

// Manifest 插件清单（plugin.json）。
//
// 放在插件目录根下，宿主加载时解析。字段全部显式校验 ——
// 插件是第三方代码，manifest 里的路径会直接用于拼可执行文件路径。
type Manifest struct {
	// Name 唯一标识，[a-z0-9-_]，2-64 字符。也是数据目录下的子目录名。
	Name string `json:"name"`
	// Title 展示名。为空时回退到 Name。
	Title string `json:"title"`
	// Version 语义化版本 x.y.z。
	Version string `json:"version"`
	// Author 作者名。
	Author string `json:"author"`
	// Description 一句话描述。
	Description string `json:"description"`
	// Entry 可执行文件名（相对插件目录）。
	// 必须是单个文件名，不含路径分隔符 —— 见 Validate 的说明。
	Entry string `json:"entry"`
	// Args 传给可执行文件的参数。
	Args []string `json:"args"`
	// Env 附加环境变量。会覆盖宿主的同名变量，故不用于传敏感配置。
	Env map[string]string `json:"env"`
	// ProtocolVersion 协议版本，必须等于宿主常量。
	ProtocolVersion int `json:"protocolVersion"`
	// Hooks 声明本插件实现哪些钩子。宿主只派发这里列出的钩子 ——
	// 不声明就不会被调用，避免插件被误用于它没实现的钩子。
	Hooks []string `json:"hooks"`
	// Permissions 权限声明。宿主目前只做记录与展示，
	// 真正的强制点在宿主代码里（插件本来就有宿主同等的文件与网络权限，
	// 这份声明的价值是让管理员在安装前知道作者索取了什么）。
	Permissions []string `json:"permissions"`
	// License SPDX 标识，默认 AGPL-3.0-only。
	License string `json:"license"`
	// Homepage 项目地址。
	Homepage string `json:"homepage"`
	// MinHostVersion 最低宿主版本（语义化）。留空表示不限。
	MinHostVersion string `json:"minHostVersion"`
}

// manifestLimits 用于限制字符串字段长度，防止超长字段撑爆内存与日志。
var manifestLimits = map[string]int{
	"name": 64, "title": 128, "version": 32, "author": 64,
	"description": 512, "entry": 128, "license": 64,
	"homepage": 256, "minHostVersion": 32,
}

// ParseManifest 解析并校验清单。raw 是 plugin.json 的内容。
//
// 校验失败返回的 error 已包含具体字段名，可直接展示给管理员。
func ParseManifest(raw []byte) (*Manifest, error) {
	if len(raw) == 0 {
		return nil, errors.New("plugin.json 为空")
	}
	if len(raw) > MaxManifestSize {
		return nil, fmt.Errorf("plugin.json 超过 %d 字节上限", MaxManifestSize)
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	// 未知字段报错：拼错的字段名静默生效比直接报错难查得多。
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("plugin.json 解析失败: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate 校验清单字段。
func (m *Manifest) Validate() error {
	// name
	if m.Name == "" {
		return errors.New("plugin.json 缺少 name")
	}
	if len(m.Name) < 2 || len(m.Name) > 64 {
		return fmt.Errorf("name 长度需在 2-64 之间，当前 %d", len(m.Name))
	}
	for _, r := range m.Name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return fmt.Errorf("name 含非法字符 %q（只允许小写字母、数字、-、_）", string(r))
		}
	}
	if strings.HasPrefix(m.Name, "-") || strings.HasPrefix(m.Name, "_") {
		return errors.New("name 不能以 - 或 _ 开头")
	}

	// entry
	if m.Entry == "" {
		return errors.New("plugin.json 缺少 entry（可执行文件名）")
	}
	// entry 会拼成 <插件目录>/<entry> 再交给 exec。若允许分隔符，
	// manifest 就能让宿主执行插件目录外的任意文件，等于任意代码执行。
	if strings.ContainsAny(m.Entry, `/\`) || strings.Contains(m.Entry, "..") {
		return fmt.Errorf("entry %q 必须是单个文件名，不能含路径分隔符或 ..", m.Entry)
	}
	if strings.ContainsAny(m.Entry, ":*?\"<>|") {
		return fmt.Errorf("entry %q 含非法字符", m.Entry)
	}

	// version
	if m.Version != "" {
		if err := validateSemver(m.Version); err != nil {
			return fmt.Errorf("version %q 无效: %w", m.Version, err)
		}
	}

	// protocolVersion
	if m.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("protocolVersion 不匹配：插件声明 %d，宿主要求 %d。"+
			"请升级插件或使用匹配版本的 NebulaDrive", m.ProtocolVersion, ProtocolVersion)
	}

	// hooks
	if len(m.Hooks) == 0 {
		return errors.New("plugin.json 的 hooks 不能为空（声明插件实现的钩子）")
	}
	if len(m.Hooks) > MaxHooksPerPlugin {
		return fmt.Errorf("hooks 数量 %d 超过上限 %d", len(m.Hooks), MaxHooksPerPlugin)
	}
	valid := map[string]bool{}
	for _, h := range AllHookNames() {
		valid[string(h)] = true
	}
	seen := map[string]bool{}
	for _, h := range m.Hooks {
		if !valid[h] {
			return fmt.Errorf("未知钩子 %q。可用钩子：%s", h, strings.Join(hookNames(), ", "))
		}
		if seen[h] {
			return fmt.Errorf("hooks 中 %q 重复", h)
		}
		seen[h] = true
	}

	// 长度限制
	for field, limit := range manifestLimits {
		v := m.fieldValue(field)
		if len(v) > limit {
			return fmt.Errorf("%s 长度 %d 超过上限 %d", field, len(v), limit)
		}
	}
	for i, a := range m.Args {
		if len(a) > 1024 {
			return fmt.Errorf("args[%d] 过长", i)
		}
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("args[%d] 含 NUL 字节", i)
		}
	}

	// 权限声明做白名单，避免前端渲染时被注入任意内容
	for _, p := range m.Permissions {
		if len(p) > 64 {
			return fmt.Errorf("permissions 项过长: %q", p)
		}
	}

	if m.License == "" {
		m.License = "AGPL-3.0-only"
	}
	if m.Title == "" {
		m.Title = m.Name
	}
	return nil
}

// fieldValue 按字段名取值，供长度校验用。
func (m *Manifest) fieldValue(field string) string {
	switch field {
	case "name":
		return m.Name
	case "title":
		return m.Title
	case "version":
		return m.Version
	case "author":
		return m.Author
	case "description":
		return m.Description
	case "entry":
		return m.Entry
	case "license":
		return m.License
	case "homepage":
		return m.Homepage
	case "minHostVersion":
		return m.MinHostVersion
	}
	return ""
}

func hookNames() []string {
	out := make([]string, 0, len(Hooks))
	for _, h := range Hooks {
		out = append(out, h.Name)
	}
	return out
}

// HookSet 返回 Hooks 声明的钩子集合。
func (m *Manifest) HookSet() map[HookName]bool {
	out := make(map[HookName]bool, len(m.Hooks))
	for _, h := range m.Hooks {
		out[HookName(h)] = true
	}
	return out
}

// DeclaredHooks 返回 Hooks 声明的钩子（已去重）。
func (m *Manifest) DeclaredHooks() []HookName {
	out := make([]HookName, 0, len(m.Hooks))
	for _, h := range m.Hooks {
		out = append(out, HookName(h))
	}
	return out
}

// ExecPath 返回可执行文件的绝对路径。
//
// dir 是插件目录。仍会做一次 filepath.Clean 与前缀校验 ——
// Validate 已挡住分隔符，但这里是最后一道防线，成本极低。
//
// 平台后缀：manifest 里写 "myplugin"（不带 .exe）时，Windows 上会自动
// 尝试 "myplugin.exe"。这样同一份 plugin.json 能跨三个平台通用 ——
// 否则插件作者要为每个平台改一次 entry，manifest 也就没法跟二进制分开发。
func (m *Manifest) ExecPath(dir string) (string, error) {
	base := filepath.Clean(dir)
	resolve := func(name string) (string, error) {
		p := filepath.Clean(filepath.Join(base, name))
		if p != base && !strings.HasPrefix(p, base+string(filepath.Separator)) {
			return "", fmt.Errorf("entry 越界：解析后路径 %q 超出插件目录 %q", p, base)
		}
		return p, nil
	}

	p, err := resolve(m.Entry)
	if err != nil {
		return "", err
	}
	if fileExists(p) {
		return p, nil
	}
	// Windows 可执行文件通常带 .exe；清单不带后缀时补一次。
	// 仅在当前平台是 Windows 且原名**没有**扩展名时才补 ——
	// 清单显式写了 .exe 但文件不存在，说明是真的缺文件，不该被静默掩盖。
	if runtime.GOOS == "windows" && filepath.Ext(m.Entry) == "" {
		if p2, err2 := resolve(m.Entry + ".exe"); err2 == nil && fileExists(p2) {
			return p2, nil
		}
	}
	return p, nil
}

// fileExists 报告路径是否为已存在的普通文件。
func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func validateSemver(v string) error {
	core := v
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		core = v[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return fmt.Errorf("需要 x.y.z 三段式")
	}
	for _, p := range parts {
		if p == "" || len(p) > 5 {
			return fmt.Errorf("版本号段 %q 无效", p)
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return fmt.Errorf("版本号段 %q 含非数字", p)
			}
		}
	}
	return nil
}

// ---- JSON-RPC 2.0 over stdio ----
//
// 协议极简：请求/响应/通知三类。每行一个 JSON 对象，UTF-8，不做跨行分帧
// （插件日志会混进 stdout，日志走 stderr）。

// RPCVersion 协议标识。
const RPCVersion = "2.0"

// RPC 方法名。
const (
	// MethodInit 宿主→插件。params = InitParams，result = InitResult。
	// 插件必须在启动后主动等待这个请求才能工作。
	MethodInit = "init"
	// MethodFire 宿主→插件。params = FireParams，result = FireResult。
	MethodFire = "fire"
	// MethodShutdown 宿主→插件。插件收到后应清理资源并退出进程。
	MethodShutdown = "shutdown"
	// MethodPing 宿主→插件。用于健康检查，插件应立即响应。
	MethodPing = "ping"
	// MethodLog 插件→宿主通知。插件把日志发给宿主，由宿主统一落库/转发。
	MethodLog = "log"
)

// LogLevel 插件日志级别。
type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// RPCRequest 宿主→插件，或插件用于向宿主索取数据。
//
// ID 为 0 表示通知（不期待响应）。插件→宿主目前只有 log 通知。
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCError JSON-RPC 错误对象。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e.Data != "" {
		return e.Message + " (" + e.Data + ")"
	}
	return e.Message
}

// RPCResponse 响应。
type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPC 标准错误码。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// CodePluginBlocked 插件主动拒绝。消息会带 "blocked" 前缀，
	// 宿主据此构造含 blocked 的 error 交给业务侧判定。
	CodePluginBlocked = -32001
	// CodeHookNotImplemented 插件未实现该钩子。
	CodeHookNotImplemented = -32002
)

// InitParams 宿主下发的初始化参数。
type InitParams struct {
	// HostVersion 宿主版本串，仅供插件日志与条件逻辑使用。
	HostVersion string `json:"hostVersion"`
	// HostProtocol 协议版本（= ProtocolVersion）。
	HostProtocol int `json:"hostProtocol"`
	// PluginName 宿主侧的插件标识（= manifest.name）。
	PluginName string `json:"pluginName"`
	// DataDir 插件专属数据目录（已创建，可写）。插件的所有持久化放这里，
	// 不要去写宿主的数据目录。
	DataDir string `json:"dataDir"`
	// Config 宿主配置。来源是 plugins.config（JSON 字符串），
	// 由管理员在安装后填写。
	Config json.RawMessage `json:"config,omitempty"`
	// StartTimeoutMillis 宿主给插件的启动预算（毫秒）。
	StartTimeoutMillis int `json:"startTimeoutMillis"`
	// SiteURL 站点根 URL，插件需要回调宿主时可用。
	SiteURL string `json:"siteUrl,omitempty"`
}

// InitResult 插件的初始化响应。
type InitResult struct {
	// OK 是否初始化成功。false 时宿主会卸载该插件并记录 error。
	OK bool `json:"ok"`
	// Name 插件自报名称，用于日志。
	Name string `json:"name,omitempty"`
	// Version 插件自报版本。
	Version string `json:"version,omitempty"`
	// Error 失败原因。
	Error string `json:"error,omitempty"`
	// Hooks 插件实际实现的钩子（应覆盖 manifest 声明的）。
	Hooks []string `json:"hooks,omitempty"`
}

// FireParams 单次钩子调用参数。
type FireParams struct {
	Hook HookName       `json:"hook"`
	Ctx  map[string]any `json:"ctx"`
	// Seq 调用序号，插件可用于日志关联。
	Seq int64 `json:"seq"`
}

// FireResult 插件处理结果。
//
// 注意：**没有 Blocked 字段**。拦截只能通过 Error 表达，且 Error 文本
// 必须含 "blocked"（大小写敏感）—— 这与进程内插件的既有约定一致，
// 两种插件形态的拦截语义因此完全相同。
type FireResult struct {
	// CtxPatch 要回写到宿主 ctx 的字段。
	//
	// 只做浅层合并：顶层键覆盖。插件不能删除宿主已有的键 ——
	// 若需要删除请显式置 nil（会被转成 JSON null）。
	CtxPatch map[string]any `json:"ctxPatch,omitempty"`
	// Error 错误消息。含 "blocked" 时宿主会把它转成可拦截请求的 error。
	Error string `json:"error,omitempty"`
	// Log 插件返回的日志行（可选）。
	Log []LogLine `json:"log,omitempty"`
}

// LogLine 一条日志。
type LogLine struct {
	Level   LogLevel `json:"level"`
	Message string   `json:"message"`
	Time    int64    `json:"time,omitempty"` // Unix 毫秒
}

// 默认超时。
const (
	// DefaultStartTimeout 插件启动 + init 往返的预算。
	DefaultStartTimeout = 10 * time.Second
	// DefaultFireTimeout 单次 fire 同步往返的超时。
	DefaultFireTimeout = 2 * time.Second
	// MaxLogLineLen 单条日志最大长度，超出截断。
	MaxLogLineLen = 2000
	// MaxLogLinesPerResponse 一次响应最多带回的日志行数。
	MaxLogLinesPerResponse = 50
)

// IsBlockedMessage 判断消息是否构成拦截。
//
// 与业务侧判定规则严格一致：strings.Contains(msg, "blocked")，
// 区分大小写。改这里必须同步改 middleware/rate_limit.go 与
// controllers/file.go 的判定，否则拦截行为会分裂。
func IsBlockedMessage(msg string) bool {
	return strings.Contains(msg, "blocked")
}

// TruncateLog 截断超长日志行。
func TruncateLog(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > MaxLogLineLen {
		// 按 rune 截断，避免切坏 UTF-8 多字节字符
		b := []byte(s[:MaxLogLineLen])
		for len(b) > 0 && !isValidUTF8(b) {
			b = b[:len(b)-1]
		}
		return string(b) + "...[truncated]"
	}
	return s
}

func isValidUTF8(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c < 0x80:
			i++
		case c&0xE0 == 0xC0:
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
		case c&0xF0 == 0xE0:
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		case c&0xF8 == 0xF0:
			if i+3 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 || b[i+3]&0xC0 != 0x80 {
				return false
			}
			i += 4
		default:
			return false
		}
	}
	return true
}
