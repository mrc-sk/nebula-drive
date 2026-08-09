package filesystem

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SFTPHandler SFTP 远程存储
type SFTPHandler struct {
	cfg     sftpConfig
	root    string
	client  *sftp.Client
	sshConn *ssh.Client
}

type sftpConfig struct {
	Host     string `json:"host"`     // host:port
	User     string `json:"user"`
	Password string `json:"password"` // 密码认证
	Key      string `json:"key"`      // 私钥认证（PEM），与密码二选一
	Root     string `json:"root"`     // 远程根目录
}

func newSFTPHandler(configJSON string) (Handler, error) {
	var cfg sftpConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("sftp config: %w", err)
	}
	if cfg.Host == "" || cfg.User == "" {
		return nil, errors.New("sftp: host and user required")
	}
	if cfg.Root == "" {
		cfg.Root = "/"
	}

	var authMethods []ssh.AuthMethod
	if cfg.Key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.Key))
		if err != nil {
			return nil, fmt.Errorf("sftp parse key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}
	if len(authMethods) == 0 {
		return nil, errors.New("sftp: need password or key")
	}

	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	conn, err := net.DialTimeout("tcp", cfg.Host, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("sftp dial: %w", err)
	}
	sshConn, ch, reqs, err := ssh.NewClientConn(conn, cfg.Host, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("sftp ssh: %w", err)
	}
	sshClient := ssh.NewClient(sshConn, ch, reqs)
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("sftp client: %w", err)
	}

	// 确保根目录存在
	sftpClient.MkdirAll(cfg.Root)

	return &SFTPHandler{cfg: cfg, root: cfg.Root, client: sftpClient, sshConn: sshClient}, nil
}

func (h *SFTPHandler) remotePath(name string) string {
	return path.Join(h.root, name)
}

func (h *SFTPHandler) Put(src io.Reader, name string, size int64) error {
	rp := h.remotePath(name)
	dir := path.Dir(rp)
	if err := h.client.MkdirAll(dir); err != nil {
		return fmt.Errorf("sftp mkdir: %w", err)
	}
	f, err := h.client.Create(rp)
	if err != nil {
		return fmt.Errorf("sftp create: %w", err)
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

func (h *SFTPHandler) Get(name string) (io.ReadCloser, error) {
	f, err := h.client.Open(h.remotePath(name))
	if err != nil {
		return nil, fmt.Errorf("sftp get: %w", err)
	}
	return f, nil
}

func (h *SFTPHandler) GetRange(name string, offset, length int64) (io.ReadCloser, error) {
	f, err := h.client.Open(h.remotePath(name))
	if err != nil {
		return nil, fmt.Errorf("sftp getrange: %w", err)
	}
	if _, err := f.Seek(offset, 0); err != nil {
		f.Close()
		return nil, err
	}
	// 限制读取长度
	return &limitedReadCloser{r: f, n: length}, nil
}

type limitedReadCloser struct {
	r io.ReadCloser
	n int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

func (l *limitedReadCloser) Close() error { return l.r.Close() }

func (h *SFTPHandler) Delete(name string) error {
	err := h.client.Remove(h.remotePath(name))
	if err != nil && !strings.Contains(err.Error(), "not exist") {
		return err
	}
	return nil
}

func (h *SFTPHandler) Size(name string) (int64, error) {
	st, err := h.client.Stat(h.remotePath(name))
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func (h *SFTPHandler) PresignGet(name string, expires time.Duration) (string, error) {
	// SFTP 不支持预签名，返回空串（调用方走中转下载）
	return "", nil
}

func (h *SFTPHandler) Copy(src, dst string) error {
	// SFTP 无原生 copy，通过中转读写实现
	srcF, err := h.client.Open(h.remotePath(src))
	if err != nil {
		return err
	}
	defer srcF.Close()
	rp := h.remotePath(dst)
	h.client.MkdirAll(path.Dir(rp))
	dstF, err := h.client.Create(rp)
	if err != nil {
		return err
	}
	defer dstF.Close()
	_, err = io.Copy(dstF, srcF)
	return err
}

// 远程 WebDAV 存储
type WebDAVRemoteHandler struct {
	cfg    webdavRemoteConfig
	root   string
	baseURL string
}

type webdavRemoteConfig struct {
	URL      string `json:"url"`      // https://dav.example.com/path/
	User     string `json:"user"`
	Password string `json:"password"`
}

func newWebDAVRemoteHandler(configJSON string) (Handler, error) {
	var cfg webdavRemoteConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("webdav config: %w", err)
	}
	if cfg.URL == "" {
		return nil, errors.New("webdav: url required")
	}
	// 确保 URL 以 / 结尾
	if !strings.HasSuffix(cfg.URL, "/") {
		cfg.URL += "/"
	}
	return &WebDAVRemoteHandler{cfg: cfg, baseURL: cfg.URL}, nil
}

func (h *WebDAVRemoteHandler) fullPath(name string) string {
	return h.baseURL + name
}

func (h *WebDAVRemoteHandler) Put(src io.Reader, name string, size int64) error {
	url := h.fullPath(name)
	// 先 MKCOL 父目录
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		dir := h.baseURL + strings.Join(parts[:i], "/") + "/"
		h.mkcol(dir)
	}
	req, err := http.NewRequest("PUT", url, src)
	if err != nil {
		return err
	}
	if h.cfg.User != "" {
		req.SetBasicAuth(h.cfg.User, h.cfg.Password)
	}
	if size > 0 {
		req.ContentLength = size
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webdav put: %s", resp.Status)
	}
	return nil
}

func (h *WebDAVRemoteHandler) Get(name string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", h.fullPath(name), nil)
	if err != nil {
		return nil, err
	}
	if h.cfg.User != "" {
		req.SetBasicAuth(h.cfg.User, h.cfg.Password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("webdav get: %s", resp.Status)
	}
	return resp.Body, nil
}

func (h *WebDAVRemoteHandler) GetRange(name string, offset, length int64) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", h.fullPath(name), nil)
	if err != nil {
		return nil, err
	}
	if h.cfg.User != "" {
		req.SetBasicAuth(h.cfg.User, h.cfg.Password)
	}
	end := offset + length - 1
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("webdav getrange: %s", resp.Status)
	}
	return resp.Body, nil
}

func (h *WebDAVRemoteHandler) Delete(name string) error {
	req, err := http.NewRequest("DELETE", h.fullPath(name), nil)
	if err != nil {
		return err
	}
	if h.cfg.User != "" {
		req.SetBasicAuth(h.cfg.User, h.cfg.Password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		return fmt.Errorf("webdav delete: %s", resp.Status)
	}
	return nil
}

func (h *WebDAVRemoteHandler) Size(name string) (int64, error) {
	req, err := http.NewRequest("HEAD", h.fullPath(name), nil)
	if err != nil {
		return 0, err
	}
	if h.cfg.User != "" {
		req.SetBasicAuth(h.cfg.User, h.cfg.Password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("webdav head: %s", resp.Status)
	}
	return resp.ContentLength, nil
}

func (h *WebDAVRemoteHandler) PresignGet(name string, expires time.Duration) (string, error) {
	return "", nil
}

func (h *WebDAVRemoteHandler) Copy(src, dst string) error {
	// WebDAV COPY
	req, err := http.NewRequest("COPY", h.fullPath(src), nil)
	if err != nil {
		return err
	}
	if h.cfg.User != "" {
		req.SetBasicAuth(h.cfg.User, h.cfg.Password)
	}
	req.Header.Set("Destination", h.fullPath(dst))
	req.Header.Set("Overwrite", "T")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		// 降级：GET + PUT
		return h.copyViaGetPut(src, dst)
	}
	return nil
}

func (h *WebDAVRemoteHandler) copyViaGetPut(src, dst string) error {
	rc, err := h.Get(src)
	if err != nil {
		return err
	}
	defer rc.Close()
	return h.Put(rc, dst, 0)
}

func (h *WebDAVRemoteHandler) mkcol(url string) {
	req, err := http.NewRequest("MKCOL", url, nil)
	if err != nil {
		return
	}
	if h.cfg.User != "" {
		req.SetBasicAuth(h.cfg.User, h.cfg.Password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
