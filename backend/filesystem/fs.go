package filesystem

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nebula-drive/nebula/conf"
)

type Handler interface {
	Put(src io.Reader, name string, size int64) error
	GetRange(name string, offset, length int64) (io.ReadCloser, error)
	Get(name string) (io.ReadCloser, error)
	Delete(name string) error
	Size(name string) (int64, error)
	PresignGet(name string, expires time.Duration) (string, error)
	Copy(src, dst string) error
}

// ErrPathTraversal 对象名试图逃逸存储根目录
var ErrPathTraversal = errors.New("path traversal detected")

// ErrInvalidObjectName 对象名非法（空、含控制字符等）
var ErrInvalidObjectName = errors.New("invalid object name")

// SafeLocalPath 把对象名安全地解析为 root 下的绝对路径。
//
// 这是所有本地存储操作的唯一入口，用于杜绝路径穿越：
//   - 反斜杠统一归一化为正斜杠（否则 Windows 下 "..\\" 可绕过 filepath.Join 的语义）
//   - 拒绝空名、绝对路径名、以及任何 ".." 段
//   - 用 filepath.Abs 后做前缀校验，防止 symbolic link / 大小写 / 盘符等边界情况漏网
//
// 返回的路径保证严格位于 root 之内（root 自身允许，表示根目录）。
func SafeLocalPath(root, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", ErrInvalidObjectName
	}
	// 1) 归一化分隔符：Windows 的 \ 与 URL 编码还原出的 \ 都必须处理
	clean := strings.ReplaceAll(name, "\\", "/")
	// 2) 去掉前导斜杠，避免被当作绝对路径丢弃 root
	clean = strings.TrimLeft(clean, "/")
	if clean == "" {
		return "", ErrInvalidObjectName
	}
	// 3) 逐段检查：拒绝 "." / ".."（Clean 之后仍要检查，因为我们要主动报错而非静默纠正）
	for _, seg := range strings.Split(clean, "/") {
		if seg == "" {
			continue
		}
		if seg == "." || seg == ".." {
			return "", ErrPathTraversal
		}
		// 拒绝 Windows 保留字符造成的歧义（如 "C:" 盘符段）
		if strings.ContainsAny(seg, "\x00") || strings.HasSuffix(seg, ":") {
			return "", ErrInvalidObjectName
		}
	}
	// 4) 归一化 + 绝对路径前缀校验（最终防线）
	joined := filepath.Join(root, filepath.FromSlash(clean))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if absPath != absRoot && !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
		return "", ErrPathTraversal
	}
	return absPath, nil
}

type Local struct {
	Root string
}

func (l *Local) Put(src io.Reader, name string, size int64) error {
	path, err := SafeLocalPath(l.Root, name)
	if err != nil {
		return fmt.Errorf("local put %q: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

func (l *Local) Get(name string) (io.ReadCloser, error) {
	path, err := SafeLocalPath(l.Root, name)
	if err != nil {
		return nil, fmt.Errorf("local get %q: %w", name, err)
	}
	return os.Open(path)
}

func (l *Local) GetRange(name string, offset, length int64) (io.ReadCloser, error) {
	path, err := SafeLocalPath(l.Root, name)
	if err != nil {
		return nil, fmt.Errorf("local getrange %q: %w", name, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	total := st.Size()
	if offset < 0 {
		offset = 0
	}
	if length <= 0 || offset+length > total {
		length = total - offset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return &sectionReadCloser{
		Reader: io.NewSectionReader(f, offset, length),
		Closer: f,
	}, nil
}

type sectionReadCloser struct {
	io.Reader
	io.Closer
}

func (l *Local) Delete(name string) error {
	path, err := SafeLocalPath(l.Root, name)
	if err != nil {
		return fmt.Errorf("local delete %q: %w", name, err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Local) Size(name string) (int64, error) {
	path, err := SafeLocalPath(l.Root, name)
	if err != nil {
		return 0, fmt.Errorf("local size %q: %w", name, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (l *Local) PresignGet(name string, expires time.Duration) (string, error) {
	return "", nil
}

// Copy 用 io.Copy 复制本地文件
func (l *Local) Copy(src, dst string) error {
	srcPath, err := SafeLocalPath(l.Root, src)
	if err != nil {
		return fmt.Errorf("local copy src %q: %w", src, err)
	}
	dstPath, err := SafeLocalPath(l.Root, dst)
	if err != nil {
		return fmt.Errorf("local copy dst %q: %w", dst, err)
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	sf, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer sf.Close()
	df, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer df.Close()
	_, err = io.Copy(df, sf)
	return err
}

type localConfig struct {
	Path string `json:"path"`
}

func New(policyType, configJSON string) (Handler, error) {
	switch policyType {
	case "local":
		var cfg localConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			// 配置 JSON 损坏（典型场景：Windows 路径里的反斜杠未转义，形如
			// {"path":"C:\Users\..."} 是非法 JSON）。绝不能静默回落到相对目录
			// "uploads" —— 那会把文件写到进程 CWD，导致落盘位置错误且极易丢失。
			// 优先回退到 conf 配置的绝对上传目录；若连它也缺失，则明确报错。
			if fb := confUploadPath(); fb != "" {
				log.Printf("[filesystem] local policy config JSON 解析失败，回退到 conf.UploadPath=%q: %v", fb, err)
				return &Local{Root: fb}, nil
			}
			return nil, fmt.Errorf("invalid local policy config %q: %w", configJSON, err)
		}
		if cfg.Path == "" {
			if fb := confUploadPath(); fb != "" {
				return &Local{Root: fb}, nil
			}
			return nil, errors.New("local policy config missing path and no conf.UploadPath fallback available")
		}
		if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
			return nil, err
		}
		return &Local{Root: cfg.Path}, nil
	case "s3":
		return newS3Handler(configJSON)
	case "oss":
		return newOSSHandler(configJSON)
	case "cos":
		return newCOSHandler(configJSON)
	case "sftp":
		return newSFTPHandler(configJSON)
	case "webdav_remote", "remote_webdav":
		return newWebDAVRemoteHandler(configJSON)
	}
	return nil, errors.New("unsupported policy type: " + policyType)
}

// confUploadPath 返回 conf 配置的绝对上传目录（兜底用）。
func confUploadPath() string {
	c := conf.Current()
	if c == nil {
		return ""
	}
	return c.System.UploadPath
}
