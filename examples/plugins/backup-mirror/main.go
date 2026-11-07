// Command backup-mirror 是一个 NebulaDrive 插件：把备份包镜像到异地目录、
// 按保留数轮转，并把"有人提交过恢复"这件事写进审计日志。
//
// # 它解决的问题
//
// 宿主在 <dataDir>/backups/ 下生成备份包，生成后触发 onBackup（异步通知）。
// 如果备份和数据库在同一块盘上，一次磁盘故障会把两者一起带走 —— 这就是
// 本插件存在的理由：把包复制到管理员配置的每个异地 target 目录，再按 keep
// 只保留最近若干份。
//
// 恢复是**暂存制**：宿主只把上传的库文件放进 <dataDir>/restore/nebula.db，
// 真正的替换发生在下次启动。所以 onRestore 里追加的审计行，是"有人提交过
// 恢复"的唯一持久痕迹 —— 这条记录不能丢。
//
// # 配置（安装后在插件页填写）
//
//	{
//	  "targets": ["D:/nd-backup", "/mnt/backup/nebula"],
//	  "keep": 7,
//	  "verify": true
//	}
//
// keep <= 0 表示不轮转（保留全部）；verify 缺省为 true。
//
// # 三条安全设计（不是可选项）
//
//  1. **原子写入。** 先写 <target>/<filename>.part，fsync 后 rename 成正式名。
//     rename 在同一文件系统内是原子的：读者要么看到完整的旧文件，要么看到
//     完整的新文件，绝不会看到一个大小正确但内容是半截的"备份"。中途失败
//     （磁盘满、网络盘断开）只会留下 .part，而轮转模式永远不会把它当成备份。
//     没有这一步，最坏情况是"备份看起来存在，恢复时才发现是坏的"。
//
//  2. **轮转只删匹配命名规则的文件。** 宿主的备份名形如
//     backup-<unix>-<8hex>.zip。target 目录里完全可能有用户自己放的东西
//     （说明文档、别的工具的包、手工留档），按"目录里所有文件"轮转会把人家的
//     数据删掉。所以正则 ^backup-\d+-[0-9a-f]{8}\.zip$ 之外的文件一律不碰，
//     也不参与 keep 计数。
//
//  3. **轮转绝不跟随符号链接。** 如果 target 里有人放了一个名为
//     backup-...zip 的链接指向 /etc 或别的目录，跟随它去 stat/删除就可能
//     作用到目录之外。这里用 os.ReadDir + entry.Type()&os.ModeSymlink 先排除，
//     再只对普通文件做删除 —— 链接本身也不删，避免误伤管理员放的跳转。
//
// # 其它刻意保持的行为
//
//   - **onBackup 永远不返回 error。** 它是异步、不可拦截的钩子，宿主不读返回值；
//     所有失败只记 stderr（JSON 行），绝不让备份创建本身失败。
//   - **单个 target 失败不影响其它 target。** 异地目录可能暂时不可写/没挂载，
//     不该因此放弃所有镜像目标。
//   - **target 与源文件同目录直接跳过。** 那不是镜像，是自己复制给自己，
//     还会在轮转时把唯一一份备份算进 keep。
//   - **大文件流式处理。** 复制用 io.Copy，重算摘要也是流式读，绝不
//     os.ReadFile 整个备份包（可能有上百 MB）。
//   - **日志只打路径、大小、sha256 与错误。** 不打印文件内容；路径是管理员
//     自己配的，可以出现在日志里，方便排查。
//
// # 构建
//
//	go build -o backup-mirror .
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- 协议结构（与 pkg/plugin 保持一致）----
//
// 插件不 import 宿主包：宿主与插件是独立进程，import 宿主会把整个后端
// 拖进依赖图，也会造成版本耦合。协议结构在此独立声明。

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
	SiteURL            string          `json:"siteUrl,omitempty"`
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
	// version 必须与 plugin.json 的 version 一致，否则管理端展示与日志对不上。
	version = "1.0.0"
	// pluginName 同时是 onCLI 的命令名：`nebula plugin backup-mirror`。
	pluginName = "backup-mirror"

	hookBackup  = "onBackup"
	hookRestore = "onRestore"
	hookCLI     = "onCLI"

	// auditFileName 恢复审计日志的文件名，位于 init 下发的 dataDir 下。
	// 用 JSONL 而不是 JSON：追加写不需要先读回整个文件，也不会因为一行损坏
	// 就让整份审计无法解析。
	auditFileName = "restore-audit.jsonl"
)

// backupFilePattern 是**唯一**允许被轮转删除的文件名形状。
//
// 宿主的命名在 controllers/backup.go: `backup-<unix>-<8hex>.zip`，
// 其中 8hex 来自 crypto/rand。这里用有捕获组的正则，既做白名单校验，
// 又顺手取出 unix 时间戳用于"最旧优先"排序。
//
// 刻意写死小写 hex：宿主生成的本来就是小写，放宽容错只会扩大删除面。
var backupFilePattern = regexp.MustCompile(`^backup-(\d+)-[0-9a-f]{8}\.zip$`)

// ---- 配置 ----

// Config 插件配置，字段与文件头示例一一对应。
type Config struct {
	// Targets 镜像目标目录。允许不存在（首次备份时 MkdirAll 创建）。
	Targets []string `json:"targets"`
	// Keep 每个 target 保留多少个备份包。<=0 表示不轮转（保留全部）。
	Keep int `json:"keep"`
	// Verify 复制完成后是否重算 SHA-256 与 ctx.sha256 比对。缺省 true。
	Verify bool `json:"verify"`
}

// defaultConfig 的零值取舍：
//
//   - Verify=true：校验是"备份能不能用"的分界线，宁可多读一遍盘；
//     只有管理员显式写 false 才关掉。
//   - Keep=0：**不轮转**。默认值绝不能是"删旧文件"—— 管理员没配 keep
//     时悄悄删掉人家的历史备份是不可接受的。要轮转必须显式配正数。
func defaultConfig() Config {
	return Config{Verify: true}
}

// normalized 清洗 targets：去空白、去空项、去重。
//
// 去重是必要的：同一目录配两遍会让 onBackup 复制两次（第二次 rename 覆盖），
// 还会让统计与轮转做两遍无用功。Windows 上路径大小写不敏感，
// 所以按 EqualFold 判重。
func (c Config) normalized() Config {
	out := make([]string, 0, len(c.Targets))
	seen := make(map[string]struct{}, len(c.Targets))
	for _, raw := range c.Targets {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		key := filepath.Clean(t)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	c.Targets = out
	return c
}

// validate 返回配置里值得提醒管理员的问题。
//
// 与 smtp-email 一样**只报告、不阻止启动**：配置不全时插件仍应能起来看日志。
// 真正的兜底在各操作内部（没有 target 就不复制、写不进去就记错误）。
func (c Config) validate() []string {
	var problems []string
	if len(c.Targets) == 0 {
		problems = append(problems, "未配置 targets，onBackup 不会复制任何文件")
	}
	if c.Keep < 0 {
		problems = append(problems, fmt.Sprintf("keep=%d 为负数，按不轮转处理", c.Keep))
	}
	return problems
}

// ---- 插件状态 ----

// pluginState 是进程内的全部可变状态：init 下发的配置与 dataDir。
// 主循环单线程处理请求，不需要额外加锁。
type pluginState struct {
	cfg     Config
	dataDir string
}

func newPluginState() *pluginState {
	return &pluginState{cfg: defaultConfig()}
}

// init 应用宿主下发的初始化参数并返回 init 结果。
//
// 与 RPC 回复分开，方便测试直接调用而不启动子进程。
func (s *pluginState) init(p initParams) initResult {
	if p.HostProtocol != 1 {
		// 协议不匹配时明确失败，不"勉强跑起来"：静默降级会出难查的问题。
		return initResult{
			OK:    false,
			Error: fmt.Sprintf("协议版本不匹配：宿主 %d，插件只支持 1", p.HostProtocol),
		}
	}

	// 从默认值开始反序列化：配置里**没写** verify 时保留 true，
	// 写了 false 才真的关掉。直接对零值 Config 反序列化做不到这个区分。
	cfg := defaultConfig()
	if len(p.Config) > 0 {
		if err := json.Unmarshal(p.Config, &cfg); err != nil {
			// 配置非法只警告，不拒绝启动 —— 否则管理员连日志都看不到。
			logf("warn", "配置解析失败，使用默认配置: %v", err)
			cfg = defaultConfig()
		}
	}
	cfg = cfg.normalized()

	s.cfg = cfg
	s.dataDir = strings.TrimSpace(p.DataDir)

	logf("info", "宿主版本 %s，站点 %s，dataDir=%s", p.HostVersion, p.SiteURL, s.dataDir)
	logf("info", "已加载 %d 个镜像 target，keep=%d，verify=%t",
		len(cfg.Targets), cfg.Keep, cfg.Verify)
	for _, t := range cfg.Targets {
		logf("info", "镜像 target: %s", t)
	}
	if problems := cfg.validate(); len(problems) > 0 {
		logf("warn", "配置不完整（%s）—— 补齐后在插件页重启即可生效",
			strings.Join(problems, "、"))
	}
	if cfg.Keep <= 0 {
		logf("info", "keep<=0：不轮转，target 里的备份会全部保留")
	}

	return initResult{
		OK:      true,
		Name:    pluginName,
		Version: version,
		Hooks:   []string{hookBackup, hookRestore, hookCLI},
	}
}

// ---- onBackup：镜像 + 轮转 ----

// mirrorBackup 处理 onBackup。
//
// 返回的 map 会作为 ctxPatch 回给宿主（异步钩子不读，但协议要求回传）。
// **永远不返回 error**：任何失败都只记 stderr，绝不让一次备份创建失败。
func (s *pluginState) mirrorBackup(ctx map[string]any) map[string]any {
	srcPath := strings.TrimSpace(str(ctx["path"]))
	filename := sanitizeFilename(str(ctx["filename"]))
	expectSHA := strings.TrimSpace(str(ctx["sha256"]))

	if srcPath == "" {
		logf("error", "onBackup ctx 缺少 path，无法镜像")
		return map[string]any{"backupMirror": "failed"}
	}
	if filename == "" {
		// 宿主一定会给 filename；真缺了就退回源文件名，至少别丢这次镜像。
		filename = filepath.Base(srcPath)
	}
	st, err := os.Stat(srcPath)
	if err != nil {
		logf("error", "备份包 %s 不可访问，无法镜像: %v", srcPath, err)
		return map[string]any{"backupMirror": "failed"}
	}
	if !st.Mode().IsRegular() {
		// 源必须是普通文件。指向别处的链接在这里不跟随，避免把奇怪的东西复制出去。
		logf("error", "备份包 %s 不是普通文件（%s），跳过镜像", srcPath, st.Mode())
		return map[string]any{"backupMirror": "failed"}
	}

	if len(s.cfg.Targets) == 0 {
		logf("warn", "未配置 targets，备份包 %s 未镜像（只留在宿主本地）", filename)
		return map[string]any{"backupMirror": "no-targets"}
	}

	srcDir := filepath.Dir(srcPath)
	mirrored, failed, skipped := 0, 0, 0

	for _, raw := range s.cfg.Targets {
		target := strings.TrimSpace(raw)
		if target == "" {
			skipped++
			continue
		}
		// 同目录 = 自己复制给自己。跳过并明确警告：它既不是镜像，
		// 又会让后面的轮转把"唯一一份备份"当成可轮转对象。
		if sameDir(target, srcDir) {
			logf("warn", "target %s 与源文件目录相同，跳过（镜像不能是自己复制给自己）", target)
			skipped++
			continue
		}
		// target 可能还不存在（例如新挂载点），创建失败只影响这一个 target。
		if err := os.MkdirAll(target, 0o750); err != nil {
			logf("error", "创建 target 目录 %s 失败: %v", target, err)
			failed++
			continue
		}

		dest, err := copyBackupAtomically(srcPath, target, filename, expectSHA, s.cfg.Verify)
		if err != nil {
			// 校验失败时 copyBackupAtomically 已删掉 .part；这里再记一条可操作的错误。
			logf("error", "镜像备份包 %s 到 %s 失败: %v", filename, target, err)
			failed++
			continue
		}
		mirrored++

		if s.cfg.Verify && expectSHA != "" {
			logf("info", "已镜像 %s -> %s（%d 字节，sha256=%s 校验通过）",
				filename, dest, st.Size(), expectSHA)
		} else {
			logf("info", "已镜像 %s -> %s（%d 字节，未校验）",
				filename, dest, st.Size())
		}

		// 只有本次复制成功才轮转：复制失败时若还按 keep 删旧备份，
		// 等于"新的没写成、旧的被删了"，是最糟的结果。
		rotateTarget(target, s.cfg.Keep)
	}

	patch := map[string]any{
		"backupMirror":  "ok",
		"mirrored":      mirrored,
		"failedTargets": failed,
	}
	switch {
	case mirrored > 0 && failed == 0:
		// 保持 ok
	case mirrored > 0:
		patch["backupMirror"] = "partial"
	case skipped > 0 && failed == 0:
		patch["backupMirror"] = "skipped"
	default:
		patch["backupMirror"] = "failed"
	}
	return patch
}

// sanitizeFilename 只接受纯文件名，拒绝任何路径成分。
//
// 文件名来自宿主 ctx，正常形如 backup-1700000000-abcdef12.zip。即使宿主可信，
// 也不该把 `../../x` 直接拼进 target —— 一次协议层改动就能变成任意文件写入。
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	base := filepath.Base(name)
	if base == "." || base == string(filepath.Separator) || base == ".." {
		return ""
	}
	return base
}

// errChecksumMismatch 用于让调用方区分"校验失败"与 IO 失败。
var errChecksumMismatch = errors.New("sha256 校验不一致")

// testHookAfterCopy 是测试接缝：在复制完成、校验之前调用，参数是 .part 路径。
//
// 用它可以在测试里真的去篡改 .part，验证"校验失败 → 删文件、不 rename"。
// 生产路径为 nil，不产生任何行为。
var testHookAfterCopy func(partPath string)

// copyBackupAtomically 把 srcPath 流式复制成 targetDir/filename。
//
// 顺序是刻意的：copy → fsync → (可选校验) → rename。
// 任何一步失败都删掉 .part 并返回错误，正式文件名只有成功后才出现。
func copyBackupAtomically(srcPath, targetDir, filename, expectSHA string, verify bool) (string, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("打开源文件失败: %w", err)
	}
	defer src.Close()

	dest := filepath.Join(targetDir, filename)
	part := dest + ".part"

	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	// 失败路径统一清理：绝不能留下一个叫 <filename>.part 的半截文件让管理员误判。
	discard := func() {
		_ = out.Close()
		_ = os.Remove(part)
	}

	// 流式复制：备份包可能上百 MB，绝不整包读进内存。
	if _, err := io.Copy(out, src); err != nil {
		discard()
		return "", fmt.Errorf("复制内容失败: %w", err)
	}
	// fsync 必须在 rename 之前：否则断电后 rename 可能先于数据落盘，
	// 留下一个大小对、内容空的"正常"备份 —— 这比没有备份更危险。
	if err := out.Sync(); err != nil {
		discard()
		return "", fmt.Errorf("fsync 临时文件失败: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(part)
		return "", fmt.Errorf("关闭临时文件失败: %w", err)
	}

	if testHookAfterCopy != nil {
		testHookAfterCopy(part)
	}

	if verify {
		if expectSHA == "" {
			// 宿主没给摘要时无法比对。只警告并继续：拒绝镜像会让"没有 sha 的宿主"
			// 永远得不到异地副本，而这里的目标是尽量多一份拷贝。
			logf("warn", "ctx 未提供 sha256，%s 已复制但无法校验", filename)
		} else {
			got, err := fileSHA256(part)
			if err != nil {
				_ = os.Remove(part)
				return "", fmt.Errorf("重算摘要失败: %w", err)
			}
			if !strings.EqualFold(got, expectSHA) {
				// 明确删掉临时文件：一个校验失败的包绝不能以正式名存在，
				// 否则恢复时会被当成可用备份。
				_ = os.Remove(part)
				return "", fmt.Errorf("%w：期望 %s，实际 %s", errChecksumMismatch, expectSHA, got)
			}
		}
	}

	if err := os.Rename(part, dest); err != nil {
		// Windows 的 os.Rename 不能覆盖已存在的文件（Unix 可以）。
		// 先尝试 rename，失败且目标存在时才替换 —— 而不是"先删旧再 rename"，
		// 后者会留下一个"目标不存在"的窗口，崩溃时旧备份就没了。
		if _, statErr := os.Stat(dest); statErr == nil {
			if rmErr := os.Remove(dest); rmErr == nil {
				err = os.Rename(part, dest)
			}
		}
		if err != nil {
			_ = os.Remove(part)
			return "", fmt.Errorf("rename 成正式文件失败: %w", err)
		}
	}
	return dest, nil
}

// fileSHA256 流式计算文件摘要，不整包读入内存。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// backupEntry 是轮转候选：文件名与文件名里携带的 unix 时间戳。
type backupEntry struct {
	name string
	ts   int64
}

// rotateTarget 在 dir 里按 keep 保留最新若干份备份包，删除更旧的。
//
// 三条约束缺一不可：
//  1. 只认 backupFilePattern 匹配的普通文件 —— 目录里其它东西一概不碰；
//  2. 符号链接先排除，绝不跟随（否则链接指向的系统文件可能被删）；
//  3. keep<=0 直接返回，不轮转。
//
// 返回被删除的文件名，便于测试与排查。
func rotateTarget(dir string, keep int) []string {
	if keep <= 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		logf("error", "轮转时读取 target 目录 %s 失败: %v", dir, err)
		return nil
	}

	candidates := make([]backupEntry, 0, len(entries))
	for _, e := range entries {
		// 先看类型再看名字：符号链接可能是有人故意放的（指向 /etc、指向别处），
		// 跟随它做删除等于让 target 目录成为任意路径删除的跳板。
		if e.Type()&os.ModeSymlink != 0 {
			logf("warn", "轮转跳过符号链接 %s（不跟随）", filepath.Join(dir, e.Name()))
			continue
		}
		// 只处理普通文件；目录、设备、管道等即使名字匹配也不碰。
		if !e.Type().IsRegular() {
			continue
		}
		m := backupFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			// **这是安全边界本身**：不匹配的文件既不算 keep，也永不被删除。
			continue
		}
		ts, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			logf("warn", "轮转跳过时间戳无法解析的文件 %s: %v", e.Name(), err)
			continue
		}
		candidates = append(candidates, backupEntry{name: e.Name(), ts: ts})
	}

	if len(candidates) <= keep {
		return nil
	}

	// 新的在前：时间戳相同（同一秒内多次备份）时用文件名倒序兜底，
	// 保证排序是确定的，不会每次运行删掉不同的文件。
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ts != candidates[j].ts {
			return candidates[i].ts > candidates[j].ts
		}
		return candidates[i].name > candidates[j].name
	})

	var removed []string
	for _, c := range candidates[keep:] {
		full := filepath.Join(dir, c.name)
		if err := os.Remove(full); err != nil {
			// 删不掉不算致命（可能是别的进程占用）：记日志，继续处理更旧的。
			logf("error", "轮转删除 %s 失败: %v", full, err)
			continue
		}
		removed = append(removed, c.name)
		logf("info", "轮转删除旧备份 %s（keep=%d）", full, keep)
	}
	return removed
}

// ---- onRestore：审计 ----

// restoreAuditRecord 是 restore-audit.jsonl 的一行。
//
// 字段名与宿主 ctx 对齐，且**不加 omitempty**：空 operator 也要留字段，
// 否则读日志的人无法区分"没记录操作者"和"操作者字段不存在"。
type restoreAuditRecord struct {
	Time       string `json:"time"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	Operator   string `json:"operator"`
	StagedPath string `json:"stagedPath"`
}

// auditRestore 处理 onRestore：往 <dataDir>/restore-audit.jsonl 追加一行。
//
// 为什么必须落盘：恢复是暂存制，宿主把待恢复的库放进 restore/，重启才替换。
// 一旦服务器在重启前失联或日志被清，这一行就是"有人提交过恢复"的唯一痕迹。
// 因此写完要 Sync 再关闭。
func (s *pluginState) auditRestore(ctx map[string]any) map[string]any {
	if strings.TrimSpace(s.dataDir) == "" {
		logf("error", "init 未下发 dataDir，无法写恢复审计（这次恢复没有被记录）")
		return map[string]any{"restoreAudit": "failed"}
	}
	if err := os.MkdirAll(s.dataDir, 0o750); err != nil {
		logf("error", "创建插件 dataDir %s 失败，无法写恢复审计: %v", s.dataDir, err)
		return map[string]any{"restoreAudit": "failed"}
	}

	rec := restoreAuditRecord{
		Time:     time.Now().Format(time.RFC3339Nano),
		Filename: str(ctx["filename"]),
		// ctx 的数字经 JSON 往返后是 float64，直接断言 int64 会静默拿到 0。
		Size:       int64(numberFromCtx(ctx["size"])),
		Operator:   str(ctx["operator"]),
		StagedPath: str(ctx["stagedPath"]),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		logf("error", "序列化恢复审计失败: %v", err)
		return map[string]any{"restoreAudit": "failed"}
	}
	line = append(line, '\n')

	path := filepath.Join(s.dataDir, auditFileName)
	// O_APPEND：并发恢复时每次写入都落到文件末尾，不会互相覆盖。
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		logf("error", "打开恢复审计 %s 失败: %v", path, err)
		return map[string]any{"restoreAudit": "failed"}
	}
	_, werr := f.Write(line)
	if werr == nil {
		// 审计是"不能丢"的记录，落盘后再算成功。
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr != nil {
		logf("error", "写入恢复审计 %s 失败: %v", path, werr)
		return map[string]any{"restoreAudit": "failed"}
	}
	if cerr != nil {
		logf("warn", "关闭恢复审计 %s 时出错: %v", path, cerr)
	}

	logf("info", "已追加恢复审计 %s（filename=%s operator=%q）", path, rec.Filename, rec.Operator)
	return map[string]any{"restoreAudit": "logged"}
}

// ---- onCLI：状态 ----

// targetStat 是 CLI 输出的一个 target 汇总。
type targetStat struct {
	path    string
	count   int
	bytes   int64
	newest  string
	oldest  string
	missing bool
	readErr error
}

// statTarget 统计 target 里**匹配命名规则的**备份包。
//
// 只统计插件自己会轮转的那批文件，用户放在目录里的其它东西不计入 ——
// 否则"备份包数量"会虚高，和 keep 的含义也对不上。符号链接同样跳过。
func statTarget(dir string) targetStat {
	st := targetStat{path: dir}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			st.missing = true
		} else {
			st.readErr = err
		}
		return st
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		st.readErr = err
		return st
	}

	var newestTS, oldestTS int64
	first := true
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 || !e.Type().IsRegular() {
			continue
		}
		m := backupFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		ts, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		st.count++
		st.bytes += info.Size()
		if first || ts > newestTS {
			newestTS = ts
			st.newest = e.Name()
		}
		if first || ts < oldestTS {
			oldestTS = ts
			st.oldest = e.Name()
		}
		first = false
	}
	return st
}

// cliStatus 处理 onCLI（同步钩子）。
//
// 命令名不是 backup-mirror 时**什么都不做**：多个插件的 onCLI 会同时收到
// 同一个命令，乱输出会污染别人的 CLI。
func (s *pluginState) cliStatus(ctx map[string]any) map[string]any {
	if str(ctx["command"]) != pluginName {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s v%s（targets=%d，keep=%d，verify=%t）\n",
		pluginName, version, len(s.cfg.Targets), s.cfg.Keep, s.cfg.Verify)
	if len(s.cfg.Targets) == 0 {
		b.WriteString("未配置 targets：onBackup 不会镜像任何备份包\n")
		return map[string]any{"cliOutput": b.String()}
	}

	if s.cfg.Keep <= 0 {
		b.WriteString("轮转：关闭（keep<=0，保留全部备份）\n")
	} else {
		fmt.Fprintf(&b, "轮转：只处理匹配 %s 的文件，每个 target 保留最新 %d 份\n",
			backupFilePattern.String(), s.cfg.Keep)
	}

	for _, t := range s.cfg.Targets {
		st := statTarget(t)
		switch {
		case st.missing:
			fmt.Fprintf(&b, "- %s：目录不存在（下一次 onBackup 会创建）\n", t)
		case st.readErr != nil:
			fmt.Fprintf(&b, "- %s：读取失败: %v\n", t, st.readErr)
		case st.count == 0:
			fmt.Fprintf(&b, "- %s：0 个备份包，0 字节\n", t)
		default:
			fmt.Fprintf(&b, "- %s：%d 个备份包，共 %d 字节，最新 %s，最旧 %s\n",
				t, st.count, st.bytes, st.newest, st.oldest)
		}
	}
	return map[string]any{"cliOutput": b.String()}
}

// ---- 主循环 ----

func main() {
	st := newPluginState()
	sc := bufio.NewScanner(os.Stdin)
	// 上限 8MB：正常 RPC 行只有几百字节，留足余量。
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			// 协议层出问题时不能退出，否则宿主看不到原因。
			logf("error", "无法解析请求: %v", err)
			continue
		}
		switch req.Method {
		case "init":
			handleInit(req, st)
		case "fire":
			handleFire(req, st)
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

func handleInit(req request, st *pluginState) {
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		reply(req, initResult{OK: false, Error: "init 参数解析失败: " + err.Error()}, nil)
		return
	}
	reply(req, st.init(p), nil)
}

// handleFire 处理钩子调用。
//
// 加 recover 兜底：插件是独立进程，一次 panic 会让宿主看到子进程消失；
// 宁可记错误并回一个空 patch，也不要把整个插件带走。
func handleFire(req request, st *pluginState) {
	var p fireParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		replyErr(req, -32602, "fire 参数解析失败: "+err.Error())
		return
	}

	defer func() {
		if r := recover(); r != nil {
			// 异步钩子不读返回值，这里回 patch 只是维持协议完整。
			logf("error", "处理 %s 时 panic: %v", p.Hook, r)
			reply(req, fireResult{CtxPatch: map[string]any{"backupMirror": "panic"}}, nil)
		}
	}()

	switch p.Hook {
	case hookBackup:
		// 异步通知：只回 ctxPatch，Error 恒为空。
		reply(req, fireResult{CtxPatch: st.mirrorBackup(p.Ctx)}, nil)
	case hookRestore:
		reply(req, fireResult{CtxPatch: st.auditRestore(p.Ctx)}, nil)
	case hookCLI:
		reply(req, fireResult{CtxPatch: st.cliStatus(p.Ctx)}, nil)
	default:
		// 宿主只派发 manifest 里声明过的钩子；多派了回空结果即可。
		reply(req, fireResult{}, nil)
	}
}

// ---- 协议输出与日志 ----

func reply(req request, result any, rerr *rpcError) {
	out := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
	b, err := json.Marshal(out)
	if err != nil {
		logf("error", "序列化响应失败: %v", err)
		return
	}
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func replyErr(req request, code int, msg string) {
	reply(req, nil, &rpcError{Code: code, Message: msg})
}

// logf 写一条结构化日志到 stderr。
//
// stdout 是 RPC 专用通道，任何多余输出都会让宿主解析失败 —— 所以日志只能走这里。
// 只打路径/大小/摘要/错误，不打印文件内容。
func logf(level, format string, args ...any) {
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
	_, _ = os.Stderr.Write(append(b, '\n'))
}

// ---- 小工具 ----

// sameDir 判断两个路径是否指向同一个目录。
//
// 先比绝对路径（Windows 上大小写不敏感）；路径暂时不存在时也能判断，
// 再用 os.SameFile 兜底处理符号链接/短路径等别名情况。
func sameDir(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 == nil && err2 == nil {
		aa, bb = filepath.Clean(aa), filepath.Clean(bb)
		if aa == bb {
			return true
		}
		if runtime.GOOS == "windows" && strings.EqualFold(aa, bb) {
			return true
		}
	}
	sa, e1 := os.Stat(a)
	sb, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(sa, sb)
}

// str 把 ctx 里的值转成字符串。JSON 往返后字符串仍是 string，
// 但数字/布尔也可能出现在需要展示的位置，统一用 fmt.Sprint 兜底。
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// numberFromCtx 读取 ctx 里的数字。
//
// 关键：进程外插件的 ctx 经过 JSON 往返，数字**一定是 float64**。
// 只断言 int64 会静默拿到 0（size 变 0 还不会报错），所以所有分支都要覆盖。
func numberFromCtx(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0
		}
		return f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0
		}
		return f
	}
	return 0
}
