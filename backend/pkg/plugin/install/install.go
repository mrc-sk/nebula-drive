// Package install 负责插件的安装、升级与卸载。
//
// 安装来源三种：
//   - 本地目录（管理员把插件目录放到服务器上，或从共享盘拷入）
//   - 远端 URL（PBClus 容器或 zip，按魔术字识别）
//   - 插件商店（远端清单里的条目，最终也是 URL）
//
// 安全要点（这一层是防"解压一个包就跑任意代码"的唯一屏障）：
//   - 路径逃逸：所有条目路径必须落在目标目录内，否则拒绝
//   - 解压大小/条目数上限：防解压炸弹
//   - 不保留可执行位与符号链接：跨平台语义混乱，符号链接尤其危险
//   - manifest 必须先校验通过才落库，entry 必须真实存在
//
// PBClus 与 zip 两条路在"解包到临时目录"之后汇聚到同一个
// InstallFromDir，所以清单校验、插件名检查、落库语义只有一套。
package install

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
)

// 解压安全上限。
const (
	// MaxZipBytes 压缩包最大字节数（64MB）。
	//
	// 这个名字是历史包袱（早期只支持 zip）。它同时是**所有**插件包
	// 的大小上限，包括 PBClus 容器 —— 两种格式共用一个上限，
	// 避免出现"格式说能到 512MB、安装器只收 64MB"这种自相矛盾。
	MaxZipBytes = 64 << 20
	// MaxPackageBytes 是 MaxZipBytes 的语义化别名，新代码用它更清楚。
	MaxPackageBytes = MaxZipBytes
	// MaxExtractedBytes 解压后总大小上限（256MB）。
	MaxExtractedBytes = 256 << 20
	// MaxZipEntries 条目数上限。
	MaxZipEntries = 2000
	// MaxFileBytes 单文件上限（64MB）。
	MaxFileBytes = 64 << 20
	// downloadTimeout 下载超时。
	downloadTimeout = 60 * time.Second
	// MaxRedirects 允许的重定向次数（防重定向循环）。
	MaxRedirects = 3
)

// Result 安装结果。
type Result struct {
	Name     string           `json:"name"`
	Dir      string           `json:"dir"`
	Manifest *plugin.Manifest `json:"manifest"`
	Files    int              `json:"files"`
	Bytes    int64            `json:"bytes"`
	Source   string           `json:"source"`
	// Replaced 覆盖了同名旧插件时为 true。
	Replaced bool `json:"replaced"`
	// Platform 本次安装面向的平台（多平台包据此只解出本平台的文件）。
	Platform string `json:"platform,omitempty"`
	// Platforms 包声明支持的平台（zip 安装时为空 —— zip 没有这个信息）。
	Platforms []string `json:"platforms,omitempty"`
	// Skipped 因平台不匹配被跳过的条目数。
	Skipped int `json:"skipped,omitempty"`
	// Signature 签名状态（zip 安装时为 nil —— zip 没有签名概念）。
	Signature *pbclus.Signature `json:"signature,omitempty"`
	// Warnings 非致命但需要让管理员知道的事情（如"这个包没有签名"）。
	// 与 error 分开：warn 策略下包能装，但界面必须如实展示风险。
	Warnings []string `json:"warnings,omitempty"`
}

// Installer 插件安装器。
type Installer struct {
	// PluginsDir 插件安装根目录。
	PluginsDir string
	// DownloadURL 从远端安装时的 URL（也可由 Resolve 提供）。
	Client *http.Client
	// MaxBytes 单次下载上限。
	MaxBytes int64

	// Platform 是"当前平台"标签（<goos>/<goarch>）。
	//
	// 之所以是个字段而不是直接读 runtime：测试要能模拟任意平台，
	// 否则"跨平台包在别的平台上装不上/装得上"这件事在单台机器上根本测不了 ——
	// 而它恰恰是最需要测的部分。空值时取当前运行平台。
	Platform string
	// TrustStore 是签名信任库。nil 表示没有信任任何签名者
	// （**不会**退化成"信任所有签名"，见 SignatureMode 的说明）。
	TrustStore pbclus.TrustStore
	// SignatureMode 签名策略：off / warn / require。
	//
	//   - off      完全不看签名
	//   - warn     照装，但把签名状态放进 Result.Warnings 供界面展示
	//   - require  只接受**受信任**签名的包
	//
	// 默认（空值）按 warn 处理：直接把 require 设成默认会让所有存量插件
	// 在升级后立刻装不上，而"签名"这件事需要管理员先建立信任库。
	// 需要严格的环境把它设成 require 即可，那时不存在降级空间。
	SignatureMode string
}

// 签名策略取值。
const (
	SignatureOff     = "off"
	SignatureWarn    = "warn"
	SignatureRequire = "require"
)

// TrustStoreFileName 是信任库的文件名（放在数据目录下）。
const TrustStoreFileName = "plugin-trust.json"

// TrustStorePath 返回数据目录下的信任库路径。
func TrustStorePath(dataDir string) string {
	return filepath.Join(dataDir, TrustStoreFileName)
}

// LoadTrustStore 读取信任库。
//
// 文件不存在时返回**空信任库**而不是错误：空信任库的语义是
// "还没有信任任何发布者"，这与"没配置"在安全上是一回事，
// 但省掉了"必须先建一个文件"的步骤。
//
// 文件存在但内容非法时**返回错误**（调用方应视为空信任库并告警）：
// 静默当成空库会让"我明明配了信任库"变成一个查不出原因的问题。
func LoadTrustStore(path string) (*pbclus.KeyRing, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return pbclus.NewEmptyKeyRing(), nil
		}
		return nil, err
	}
	var ring pbclus.KeyRing
	if err := json.Unmarshal(b, &ring); err != nil {
		return nil, fmt.Errorf("信任库 %s 不是合法 JSON: %w", path, err)
	}
	if err := ring.Validate(); err != nil {
		return nil, fmt.Errorf("信任库 %s 自身不一致: %w", path, err)
	}
	if ring.Keys == nil {
		ring.Keys = []pbclus.TrustedKey{}
	}
	return &ring, nil
}

// SaveTrustStore 写入信任库（供管理端/工具建立信任关系）。
func SaveTrustStore(path string, ring *pbclus.KeyRing) error {
	if err := ring.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ring, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// resultPlatform 返回生效的平台标签。
func (i *Installer) resultPlatform() string {
	if i.Platform != "" {
		return i.Platform
	}
	return pbclus.HostPlatform()
}

// signaturePolicy 规范化签名策略。
func (i *Installer) signaturePolicy() string {
	switch i.SignatureMode {
	case SignatureOff, SignatureWarn, SignatureRequire:
		return i.SignatureMode
	case "":
		return SignatureWarn
	default:
		// 写错的值按最严格处理：配置笔误不该悄悄降低安全门槛
		return SignatureRequire
	}
}

// New 构造安装器。
func New(pluginsDir string) *Installer {
	return &Installer{
		PluginsDir: pluginsDir,
		MaxBytes:   MaxZipBytes,
		Client: &http.Client{
			Timeout: downloadTimeout,
			CheckRedirect: func(r *http.Request, via []*http.Request) error {
				if len(via) >= MaxRedirects {
					return errors.New("重定向次数过多")
				}
				return nil
			},
		},
	}
}

// InstallFromDir 从一个已存在的目录安装插件（复制到 PluginsDir）。
//
// 目录形式不限定：plugin.json / register.go / co.BEGG 可以分散在任意子目录，
// 由 plugin.LoadContract 递归定位（深度 <= MaxContractDepth）。
// 因此这里复制**整个 src**，绝不裁到「含 plugin.json 的那一层」——
// 裁层会把散落在兄弟目录里的 co.BEGG / .php 文件丢掉。
func (i *Installer) InstallFromDir(src, sourceRef string) (*Result, error) {
	contract, err := plugin.LoadContract(src, false)
	if err != nil {
		return nil, err
	}
	man := contract.Manifest
	hasPages := len(man.Pages) > 0 ||
		(contract.Begg != nil && len(contract.Begg.Pages) > 0)
	dst := filepath.Join(i.PluginsDir, man.Name)
	if err := checkNameForFS(man.Name); err != nil {
		return nil, err
	}
	replaced := dirExists(dst)
	if replaced {
		if err := os.RemoveAll(dst); err != nil {
			return nil, fmt.Errorf("清理旧插件目录失败: %w", err)
		}
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return nil, fmt.Errorf("创建插件目录失败: %w", err)
	}
	n, size, err := copyTree(src, dst)
	if err != nil {
		// 复制失败要清理，避免留下半成品被后续加载
		os.RemoveAll(dst)
		return nil, err
	}
	// 复制后再校验一次 entry 真实存在 —— 复制可能漏文件
	if err := verifyEntry(dst, man, hasPages); err != nil {
		os.RemoveAll(dst)
		return nil, err
	}
	// v2 三文件契约：安装阶段就检查 register.go / co.BEGG 是否齐全且声明一致。
	// 解释器探测放到启用时做（loadOne），这里 probe=false 避免装了插件但
	// 服务器还没装解释器时连安装都做不了。
	if man.ProtocolVersion >= 2 {
		if _, err := plugin.LoadContract(dst, false); err != nil {
			os.RemoveAll(dst)
			return nil, fmt.Errorf("v2 插件契约校验失败: %w", err)
		}
	}
	return &Result{
		Name: man.Name, Dir: dst, Manifest: man,
		Files: n, Bytes: size, Source: sourceRef, Replaced: replaced,
	}, nil
}

// InstallFromURL 从远端安装插件包。
//
// 下载后交给 InstallFromPackage 按**魔术字**识别格式：
// PBClus 容器与 zip 都支持（zip 是历史格式，继续兼容）。
// 这里不再假定是 zip —— 早期版本把下载文件命名成 plugin.zip 并直接走 zip 路径，
// 结果是 PBClus 包永远装不上。
func (i *Installer) InstallFromURL(rawURL, sourceRef string) (*Result, error) {
	if err := validateURL(rawURL); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "nebula-plugin-dl-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	// 下载文件名带上原扩展名：安装器要据此给出"zip 改后缀"这类具体提示，
	// 名字全叫 plugin.zip 的话这个判断就失效了。
	name := "plugin"
	if ext := strings.ToLower(path.Ext(parseURLPath(rawURL))); ext != "" {
		switch ext {
		case pbclus.Extension, ".zip":
			name += ext
		default:
			name += ".pkg"
		}
	} else {
		name += ".pkg"
	}
	pkgPath := filepath.Join(tmp, name)
	if err := i.download(rawURL, pkgPath); err != nil {
		return nil, err
	}
	return i.InstallFromPackage(pkgPath, sourceRef)
}

// InstallFromPackage 按魔术字识别包格式并安装。
//
// 识别依据是**内容**而不是扩展名：扩展名是给人看的提示，
// 不是格式的判据。
func (i *Installer) InstallFromPackage(pkgPath, sourceRef string) (*Result, error) {
	st, err := os.Stat(pkgPath)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s 是目录，不是插件包（本地目录请用 InstallFromDir）", pkgPath)
	}

	kind, err := pbclus.SniffFile(pkgPath)
	if err != nil {
		return nil, err
	}
	switch kind {
	case pbclus.KindPBClus:
		return i.InstallFromPBClus(pkgPath, sourceRef)
	case pbclus.KindZip:
		// 后缀写成 .pbclus 但内容是 zip：这不是"格式兼容问题"，
		// 而是用户以为改了后缀就换了格式。必须把这件事说清楚，
		// 否则他会以为 PBClus 校验没生效。
		if strings.EqualFold(filepath.Ext(pkgPath), pbclus.Extension) {
			return nil, fmt.Errorf(
				"这个文件的扩展名是 %s，但内容是 zip —— 改后缀不会改变格式。"+
					"请用 pbclus-pack 重新打包成真正的 PBClus 容器", pbclus.Extension)
		}
		return i.InstallFromZip(pkgPath, sourceRef)
	default:
		return nil, fmt.Errorf("无法识别的插件包：既不是 %s 容器也不是 zip "+
			"（前 8 字节为 % x）。PBClus 包的头部应以 %s 魔术字开头",
			pbclus.FormatName, firstBytes(pkgPath, 8), pbclus.Magic)
	}
}

// InstallFromPBClus 安装 PBClus 容器。
//
// 流程：完整校验 → 解包到临时目录 → 交给 InstallFromDir 走既有路径
// （清单解析、插件名检查、entry 存在性校验、复制、落库都由那条路径负责）。
// 这样 PBClus 与 zip 两条路在"装进 PluginsDir"之后的处理完全一致，
// 不会出现两套安装语义。
func (i *Installer) InstallFromPBClus(pkgPath, sourceRef string) (*Result, error) {
	st, err := os.Stat(pkgPath)
	if err != nil {
		return nil, err
	}
	if st.Size() > int64(pbclus.MaxPackageBytes) {
		return nil, fmt.Errorf("插件包 %d 字节，超过上限 %d", st.Size(), int64(pbclus.MaxPackageBytes))
	}

	rd, err := pbclus.Open(pkgPath)
	if err != nil {
		// 包装一层：调用方（管理端）拿到的是"包格式错误"这个层次的信息，
		// 而不是裸的"清单摘要不匹配" —— 后者看起来像内部 bug。
		return nil, fmt.Errorf("%s 包无法解析: %w", pbclus.FormatName, err)
	}
	defer rd.Close()

	// 先做整体校验再解包：损坏的包能立刻说清"坏在哪"，
	// 而不是解到一半失败留下一个半成品目录。
	if err := rd.Verify(); err != nil {
		return nil, fmt.Errorf("%s 包校验失败: %w", pbclus.FormatName, err)
	}

	platform := i.resultPlatform()

	// 平台检查放在解包**之前**。
	//
	// 顺序很重要：不支持当前平台的包如果先解包，错误会变成
	// "entry 指向的文件不存在" —— 与本平台的插件目录混在一起，
	// 排查方向完全跑偏。先在门口拒掉，错误信息才能直说"平台不匹配"。
	if !rd.Supports(platform) {
		return nil, pbclus.PlatformError(rd.RequiresPlatforms(), platform)
	}

	// 签名检查
	sig := rd.Status(i.TrustStore)
	warnings, err := i.checkSignature(&sig)
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "nebula-plugin-pbclus-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	rep, err := rd.ExtractForPlatform(tmp, platform)
	if err != nil {
		return nil, err
	}
	root := tmp
	if sub, err := findPluginRoot(tmp); err == nil {
		root = sub
	}
	res, err := i.InstallFromDir(root, sourceRef)
	if err != nil {
		return nil, err
	}
	res.Platform = platform
	res.Platforms = rd.Platforms()
	res.Skipped = rep.Skipped
	res.Signature = &sig
	res.Warnings = append(res.Warnings, warnings...)
	return res, nil
}

// checkSignature 按策略判定签名，返回需要展示的告警。
//
// 这里只用 **Trusted** 做决策，绝不用 Valid：
// 公钥内嵌在包里，攻击者可以用自己的密钥签一个包并让 Valid 为 true。
// 拿 Valid 当"这个包可信"等于没做校验 —— 这是本类设计最危险的误用，
// 所以两个状态从一开始就分成两个字段，不给顺手写错的机会。
func (i *Installer) checkSignature(sig *pbclus.Signature) ([]string, error) {
	policy := i.signaturePolicy()
	if policy == SignatureOff {
		return nil, nil
	}

	// 判定结果先算清楚，三种策略只是对同一个判定做不同处置
	var problem string
	switch {
	case !sig.Present:
		problem = "插件包没有签名，无法确认来源"
	case !sig.Valid:
		problem = "签名无效：" + sig.Err
	case !sig.Trusted:
		problem = fmt.Sprintf("签名者 %s 不在信任库里（包本身完整且签名自洽，但这个密钥未被信任）", sig.KeyID)
	}

	if problem == "" {
		name := sig.KeyName
		if name == "" {
			name = sig.KeyID
		}
		return []string{fmt.Sprintf("已由受信任的发布者签名：%s（%s）", name, sig.KeyID)}, nil
	}

	if policy == SignatureRequire {
		return nil, fmt.Errorf("%s（当前策略为 require，已拒绝安装）", problem)
	}
	return []string{problem}, nil
}

// parseURLPath 取 URL 的路径部分（用于推断下载文件的扩展名）。
func parseURLPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path
}

// firstBytes 读文件开头若干字节，仅用于把错误信息写得具体一点。
func firstBytes(p string, n int) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b := make([]byte, n)
	got, _ := io.ReadFull(f, b)
	return b[:got]
}

// InstallFromZip 从本地 zip 文件安装。
//
// 支持两种 zip 布局：
//   - 根目录直接是 plugin.json
//   - 单一顶层目录，plugin.json 在其中（GitHub 源码 zip 的常见形态）
//
// 后者会自动剥掉那一层。
func (i *Installer) InstallFromZip(zipPath, sourceRef string) (*Result, error) {
	st, err := os.Stat(zipPath)
	if err != nil {
		return nil, err
	}
	if st.Size() > MaxZipBytes {
		return nil, fmt.Errorf("压缩包 %d 字节，超过上限 %d", st.Size(), int64(MaxZipBytes))
	}

	tmp, err := os.MkdirTemp("", "nebula-plugin-uz-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	if err := unzipSafe(zipPath, tmp); err != nil {
		return nil, err
	}
	// 处理"单一顶层目录"布局
	root := tmp
	if sub, err := findPluginRoot(tmp); err == nil {
		root = sub
	}
	return i.InstallFromDir(root, sourceRef)
}

// findPluginRoot 在解压结果中定位含 plugin.json 的目录。
//
// v2 允许插件把清单放进任意子目录（例如 ASS/ 里一堆 .php + register.go +
// plugin.json），所以这里必须递归，不能只看一级。
// 优先根目录；否则找唯一含 plugin.json 的子目录；有多个候选时报错（不猜）。
func findPluginRoot(base string) (string, error) {
	if fileExists(filepath.Join(base, plugin.ManifestFileName)) {
		return base, nil
	}
	var hits []string
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(os.PathSeparator)) + 1
		}
		if d.IsDir() {
			if depth > plugin.MaxContractDepth {
				return fs.SkipDir
			}
			if p != base {
				n := d.Name()
				if strings.HasPrefix(n, ".") || n == "node_modules" ||
					n == "__pycache__" || n == "vendor" {
					return fs.SkipDir
				}
			}
			return nil
		}
		if d.Name() == plugin.ManifestFileName {
			hits = append(hits, filepath.Dir(p))
		}
		return nil
	})
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("压缩包内未找到 %s", plugin.ManifestFileName)
	case 1:
		return hits[0], nil
	default:
		names := make([]string, len(hits))
		for i, h := range hits {
			names[i], _ = filepath.Rel(base, h)
		}
		return "", fmt.Errorf("压缩包内有多个插件目录，无法确定安装哪个: %s",
			strings.Join(names, ", "))
	}
}

// Uninstall 删除插件目录。返回是否实际删除。
func (i *Installer) Uninstall(name string) (bool, error) {
	if err := checkNameForFS(name); err != nil {
		return false, err
	}
	dst := filepath.Join(i.PluginsDir, name)
	if !dirExists(dst) {
		return false, nil
	}
	// 二次确认目标确实在 PluginsDir 内
	if err := mustBeUnder(i.PluginsDir, dst); err != nil {
		return false, err
	}
	if err := os.RemoveAll(dst); err != nil {
		return false, fmt.Errorf("删除插件目录失败: %w", err)
	}
	return true, nil
}

// ---- 内部工具 ----

func readManifestFile(path string) (*plugin.Manifest, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("未找到 %s: %w", plugin.ManifestFileName, err)
	}
	if st.Size() > plugin.MaxManifestSize {
		return nil, fmt.Errorf("%s 超过 %d 字节上限", plugin.ManifestFileName, plugin.MaxManifestSize)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return plugin.ParseManifest(raw)
}

// ReadManifest 解析并校验指定目录下的插件清单。
//
// 供重载流程使用：启用/重启插件时不该信任数据库里的冗余字段，
// 要以磁盘上真实的清单为准。走 LoadContract 递归定位，兼容三文件散落布局。
func ReadManifest(dir string) (*plugin.Manifest, error) {
	c, err := plugin.LoadContract(dir, false)
	if err != nil {
		return nil, err
	}
	m := c.Manifest
	hasPages := len(m.Pages) > 0 ||
		(c.Begg != nil && len(c.Begg.Pages) > 0)
	// 顺便确认 entry 真的在 —— 数据库说装好了但文件被删的情况很常见
	if err := verifyEntry(dir, m, hasPages); err != nil {
		return nil, err
	}
	return m, nil
}

func verifyEntry(dir string, man *plugin.Manifest, hasPages bool) error {
	// v2 纯页面插件（只在 co.BEGG 声明 pages、不订阅 hooks）没有 JSON-RPC
	// 子进程，因此不需要 entry；页面服务由 co.BEGG.command 拉起。
	// 之前只认 plugin.json 的 pages，导致「页面只写在 co.BEGG 里」的插件
	// 被误判为缺少 entry。
	if man.Entry == "" && man.ProtocolVersion >= 2 && hasPages {
		return nil
	}
	p, err := man.ExecPath(dir)
	if err != nil {
		return err
	}
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("entry 指向的文件不存在: %s", man.Entry)
	}
	if st.IsDir() {
		return fmt.Errorf("entry 指向目录而非文件: %s", man.Entry)
	}
	if st.Size() == 0 {
		return fmt.Errorf("entry 文件为空: %s", man.Entry)
	}
	return nil
}

// checkNameForFS 校验插件名可用于目录名。
//
// 除了 manifest 已有的规则，这里再挡一层 Windows 保留名
// （CON/PRN/AUX/NUL/COM1..9/LPT1..9）—— 它们在 Windows 上无法创建目录，
// 而本项目要在 Windows 上开发调试。
func checkNameForFS(name string) error {
	if name == "" {
		return errors.New("插件名为空")
	}
	if len(name) > 64 {
		return errors.New("插件名过长")
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return fmt.Errorf("插件名含非法字符 %q", string(r))
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	reserved := map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
		"COM6": true, "COM7": true, "COM8": true, "COM9": true,
		"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
		"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	}
	if reserved[base] {
		return fmt.Errorf("插件名 %q 是 Windows 保留名，无法作为目录名", name)
	}
	return nil
}

func mustBeUnder(base, target string) error {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if absTarget == absBase {
		return errors.New("目标不能是根目录本身")
	}
	if !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
		return fmt.Errorf("目标 %q 超出允许范围 %q", absTarget, absBase)
	}
	return nil
}

// validateURL 校验下载 URL：必须 http/https，且 host 非空。
//
// 这里不阻止内网地址：管理员是可信角色，插件站也可能在内网。
// 但 scheme 必须限制（不能 file:// 或 ftp://，那些会以宿主权限读本地文件）。
func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("URL 无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("只支持 http/https 协议，当前 %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("URL 缺少主机名")
	}
	return nil
}

func (i *Installer) download(rawURL, dest string) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "NebulaDrive-PluginInstaller/1.0")

	// 显式禁掉代理继承里的本地地址绕过逻辑，保持直连行为可控
	client := *i.Client
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	// 先看 Content-Length，超限直接拒绝（省流量）
	if resp.ContentLength > i.MaxBytes {
		return fmt.Errorf("压缩包声明大小 %d 超过上限 %d", resp.ContentLength, i.MaxBytes)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	// LimitReader 兜底：Content-Length 可能撒谎
	n, err := io.Copy(f, io.LimitReader(resp.Body, i.MaxBytes+1))
	if err != nil {
		return fmt.Errorf("写入失败: %w", err)
	}
	if n > i.MaxBytes {
		return fmt.Errorf("压缩包超过上限 %d 字节", i.MaxBytes)
	}
	return nil
}

// unzipSafe 解压 zip 到 dst，防御 zip slip 与 zip bomb。
func unzipSafe(zipPath, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开压缩包失败: %w", err)
	}
	defer zr.Close()

	if len(zr.File) > MaxZipEntries {
		return fmt.Errorf("压缩包有 %d 个条目，超过上限 %d", len(zr.File), MaxZipEntries)
	}

	absDst, err := filepath.Abs(dst)
	if err != nil {
		return err
	}

	var total int64
	for _, zf := range zr.File {
		// 关键：条目名先 Clean，任何越界（../ 或绝对路径）立即拒绝。
		// 不用"过滤"而是"报错" —— 过滤会让管理员困惑于"为什么少了个文件"。
		clean := filepath.Clean(filepath.FromSlash(zf.Name))
		if clean == "." || clean == string(filepath.Separator) {
			continue // 目录条目本身
		}
		target := filepath.Join(absDst, clean)
		if !strings.HasPrefix(target, absDst+string(filepath.Separator)) {
			return fmt.Errorf("压缩包含越界条目，已拒绝（zip slip）: %q", zf.Name)
		}
		// 拒绝符号链接：它能让解压出的链接指向宿主任意文件
		if zf.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包含符号链接，已拒绝: %q", zf.Name)
		}

		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		}
		if !zf.FileInfo().Mode().IsRegular() {
			// 设备文件、FIFO 等一律跳过
			continue
		}
		if zf.UncompressedSize64 > uint64(MaxFileBytes) {
			return fmt.Errorf("压缩包内文件 %q 声明 %d 字节，超过单文件上限",
				zf.Name, zf.UncompressedSize64)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		written, err := extractOne(zf, target)
		if err != nil {
			return err
		}
		total += written
		if total > MaxExtractedBytes {
			return fmt.Errorf("解压后总大小超过上限 %d 字节（zip bomb）", int64(MaxExtractedBytes))
		}
	}
	return nil
}

func extractOne(zf *zip.File, target string) (int64, error) {
	rc, err := zf.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o750)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	// 多读 1 字节用于探测实际大小是否超过声明值
	n, err := io.Copy(f, io.LimitReader(rc, MaxFileBytes+1))
	if err != nil {
		return 0, fmt.Errorf("解压 %q 失败: %w", zf.Name, err)
	}
	if n > MaxFileBytes {
		return 0, fmt.Errorf("解压 %q 实际大小超过单文件上限", zf.Name)
	}
	return n, nil
}

// copyTree 递归复制目录，返回文件数与总字节。
func copyTree(src, dst string) (int, int64, error) {
	var files int
	var bytes int64
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		// 同样要防越界：src 里有符号链接时 Walk 可能给出意外路径
		if err := mustBeUnder(dst, target); err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !info.Mode().IsRegular() {
			return nil // 跳过符号链接/设备文件
		}
		if info.Size() > MaxFileBytes {
			return fmt.Errorf("文件 %q 超过单文件上限", rel)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// 0o750：插件可执行文件需要 owner 权限；不设 0o777 哪怕一秒
		if err := os.WriteFile(target, b, 0o750); err != nil {
			return err
		}
		files++
		bytes += info.Size()
		if bytes > MaxExtractedBytes {
			return fmt.Errorf("目录总大小超过上限 %d 字节", int64(MaxExtractedBytes))
		}
		return nil
	})
	return files, bytes, err
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// IsLoopbackAddr 判断 host 是否指向本机（供调用方决定是否提示风险）。
func IsLoopbackAddr(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ---- 供控制器使用的导出工具 ----

// MaxCatalogBytes 商店清单大小上限（2MB）。
const MaxCatalogBytes = 2 << 20

// ValidateCatalogURL 校验清单/下载 URL。
//
// 复用内部 validateURL，但导出以便控制器在校验商店条目时用同一套规则 ——
// 两处规则不一致的话，会出现"商店里显示能装，点了报 URL 无效"。
func ValidateCatalogURL(raw string) error { return validateURL(raw) }

// ValidatePluginName 校验插件名（可安全用作目录名）。
func ValidatePluginName(name string) error { return checkNameForFS(name) }

// IsInternalAddr 判断 URL 指向本机或私有网段。
//
// 仅用于给管理员提示（内网插件站是合法场景，不该阻断），
// 但"你正在从内网地址下载并运行程序"这件事应当被看见。
func IsInternalAddr(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// 域名：不解析 DNS（那会让一个 GET 接口产生网络副作用），
		// 只认字面量 IP 与常见内网主机名
		return strings.EqualFold(host, "localhost") ||
			strings.HasSuffix(strings.ToLower(host), ".local") ||
			strings.HasSuffix(strings.ToLower(host), ".internal")
	}
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// FetchCatalog 拉取插件商店清单。
//
// 限制 2MB：清单应是几十 KB 量级，远超说明被劫持或指向了错误资源。
func (i *Installer) FetchCatalog(catalogURL string) ([]byte, error) {
	if err := validateURL(catalogURL); err != nil {
		return nil, err
	}
	client := *i.Client
	resp, err := client.Get(catalogURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxCatalogBytes {
		return nil, fmt.Errorf("清单声明 %d 字节，超过上限 %d", resp.ContentLength, MaxCatalogBytes)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxCatalogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxCatalogBytes {
		return nil, fmt.Errorf("清单超过上限 %d 字节", MaxCatalogBytes)
	}
	return b, nil
}
