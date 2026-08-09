package filesystem

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
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

type Local struct {
	Root string
}

func (l *Local) Put(src io.Reader, name string, size int64) error {
	path := filepath.Join(l.Root, name)
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
	return os.Open(filepath.Join(l.Root, name))
}

func (l *Local) GetRange(name string, offset, length int64) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(l.Root, name))
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
	if err := os.Remove(filepath.Join(l.Root, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Local) Size(name string) (int64, error) {
	st, err := os.Stat(filepath.Join(l.Root, name))
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
	srcPath := filepath.Join(l.Root, src)
	dstPath := filepath.Join(l.Root, dst)
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
		_ = json.Unmarshal([]byte(configJSON), &cfg)
		if cfg.Path == "" {
			cfg.Path = "uploads"
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
	}
	return nil, errors.New("unsupported policy type: " + policyType)
}
