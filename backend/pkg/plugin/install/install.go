// Package install 负责插件的安装、升级与卸载。
//
// 安装来源三种：
//   - 本地目录（管理员把插件目录放到服务器上，或从共享盘拷入）
//   - 远端 zip URL
//   - 插件商店（远端清单里的条目，最终也是 URL）
//
// 安全要点（这一层是防"解压一个 zip 就跑任意代码"的唯一屏障）：
//   - zip slip：所有条目路径必须落在目标目录内，否则拒绝
//   - 解压大小/条目数上限：防 zip bomb
//   - 不保留可执行位与符号链接：跨平台 zip 语义混乱，符号链接尤其危险
//   - manifest 必须先校验通过才落库，entry 必须真实存在
package install

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
)

// 解压安全上限。
const (
	// MaxZipBytes 压缩包最大字节数（64MB）。
	MaxZipBytes = 64 << 20
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
	Name     string             `json:"name"`
	Dir      string             `json:"dir"`
	Manifest *plugin.Manifest   `json:"manifest"`
	Files    int                `json:"files"`
	Bytes    int64              `json:"bytes"`
	Source   string             `json:"source"`
	// Replaced 覆盖了同名旧插件时为 true。
	Replaced bool `json:"replaced"`
}

// Installer 插件安装器。
type Installer struct {
	// PluginsDir 插件安装根目录。
	PluginsDir string
	// DownloadURL 从远端安装时的 URL（也可由 Resolve 提供）。
	Client *http.Client
	// MaxBytes 单次下载上限。
	MaxBytes int64
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
// src 会被递归复制。src 内的 plugin.json 是必需的。
func (i *Installer) InstallFromDir(src, sourceRef string) (*Result, error) {
	man, err := readManifestFile(filepath.Join(src, plugin.ManifestFileName))
	if err != nil {
		return nil, err
	}
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
	if err := verifyEntry(dst, man); err != nil {
		os.RemoveAll(dst)
		return nil, err
	}
	return &Result{
		Name: man.Name, Dir: dst, Manifest: man,
		Files: n, Bytes: size, Source: sourceRef, Replaced: replaced,
	}, nil
}

// InstallFromURL 从远端 zip 安装。
func (i *Installer) InstallFromURL(rawURL, sourceRef string) (*Result, error) {
	if err := validateURL(rawURL); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "nebula-plugin-dl-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	zipPath := filepath.Join(tmp, "plugin.zip")
	if err := i.download(rawURL, zipPath); err != nil {
		return nil, err
	}
	return i.InstallFromZip(zipPath, sourceRef)
}

// InstallFromZip 从本地 zip 文件安装。
//
// 支持两种 zip 布局：
//   - 根目录直接是 plugin.json
//   - 单一顶层目录，plugin.json 在其中（GitHub 源码 zip 的常见形态）
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
// 优先根目录；否则找唯一含 plugin.json 的子目录；
// 有多个候选时报错（不猜）。
func findPluginRoot(base string) (string, error) {
	if fileExists(filepath.Join(base, plugin.ManifestFileName)) {
		return base, nil
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", err
	}
	var hits []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(base, e.Name())
		if fileExists(filepath.Join(p, plugin.ManifestFileName)) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("压缩包内未找到 %s", plugin.ManifestFileName)
	case 1:
		return hits[0], nil
	default:
		names := make([]string, len(hits))
		for i, h := range hits {
			names[i] = filepath.Base(h)
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

// ReadManifest 解析并校验指定目录下的 plugin.json。
//
// 供重载流程使用：启用/重启插件时不该信任数据库里的冗余字段，
// 要以磁盘上真实的清单为准。
func ReadManifest(dir string) (*plugin.Manifest, error) {
	m, err := readManifestFile(filepath.Join(dir, plugin.ManifestFileName))
	if err != nil {
		return nil, err
	}
	// 顺便确认 entry 真的在 —— 数据库说装好了但文件被删的情况很常见
	if err := verifyEntry(dir, m); err != nil {
		return nil, err
	}
	return m, nil
}

func verifyEntry(dir string, man *plugin.Manifest) error {
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
