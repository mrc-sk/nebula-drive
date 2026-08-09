// Package aria2 提供对 aria2c 子进程及 JSON-RPC 的封装。
// 若系统未安装 aria2c，所有方法返回 ErrNotInstalled，调用方可回退到其他下载逻辑。
package aria2

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// ErrNotInstalled 系统未安装 aria2c 时返回。
var ErrNotInstalled = errors.New("aria2 not installed, please install aria2 first")

// RPCPort aria2 RPC 默认端口
const RPCPort = 6800

// Aria2Manager 管理 aria2c 子进程 + JSON-RPC 通讯
type Aria2Manager struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	secret string
	rpcURL string
	ready  bool
}

var (
	manager *Aria2Manager
	once    sync.Once
)

// GetManager 获取全局单例
func GetManager() *Aria2Manager {
	once.Do(func() {
		manager = &Aria2Manager{rpcURL: fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", RPCPort)}
	})
	return manager
}

// IsReady 是否可用（已内嵌启动或已连接外部实例）
func (m *Aria2Manager) IsReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready
}

// Secret 返回当前 RPC secret（调试用）
func (m *Aria2Manager) Secret() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secret
}

// findAria2c 在 PATH 中查找 aria2c
func findAria2c() string {
	p, err := exec.LookPath("aria2c")
	if err != nil {
		return ""
	}
	return p
}

// randomSecret 生成随机 secret
func randomSecret() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartEmbedded 检查系统是否已安装 aria2c，若有就启动子进程。
// 失败（未安装/启动失败）返回错误，调用方应静默处理。
func (m *Aria2Manager) StartEmbedded() error {
	bin := findAria2c()
	if bin == "" {
		return ErrNotInstalled
	}
	dir := filepath.Join(os.TempDir(), "nebula-aria2")
	secret := randomSecret()
	cmd := exec.Command(bin,
		"--enable-rpc",
		"--rpc-listen-port="+strconv.Itoa(RPCPort),
		"--rpc-secret="+secret,
		"--dir="+dir,
		"--continue=true",
		"--max-concurrent-downloads=5",
		"--rpc-allow-origin-all=true",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	m.mu.Lock()
	m.cmd = cmd
	m.secret = secret
	m.rpcURL = fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", RPCPort)
	m.mu.Unlock()

	// 轮询等待 RPC 就绪
	for i := 0; i < 30; i++ {
		if err := m.probe(); err == nil {
			m.mu.Lock()
			m.ready = true
			m.mu.Unlock()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("aria2c started but RPC not reachable")
}

// TryConnectExisting 尝试连接已运行的 aria2 RPC
func (m *Aria2Manager) TryConnectExisting(rpcURL, secret string) error {
	if rpcURL == "" {
		return errors.New("empty rpc url")
	}
	m.mu.Lock()
	m.rpcURL = rpcURL
	m.secret = secret
	m.mu.Unlock()
	if err := m.probe(); err != nil {
		m.mu.Lock()
		m.ready = false
		m.mu.Unlock()
		return err
	}
	m.mu.Lock()
	m.ready = true
	m.mu.Unlock()
	return nil
}

// probe 不检查 ready，直接探测 RPC
func (m *Aria2Manager) probe() error {
	m.mu.Lock()
	rpcURL := m.rpcURL
	secret := m.secret
	m.mu.Unlock()
	_, err := m.doCall(rpcURL, secret, "aria2.getVersion")
	return err
}

// ---- JSON-RPC ----

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// call 检查 ready 后发起 JSON-RPC
func (m *Aria2Manager) call(method string, params ...any) (json.RawMessage, error) {
	m.mu.Lock()
	ready := m.ready
	secret := m.secret
	rpcURL := m.rpcURL
	m.mu.Unlock()
	if !ready {
		return nil, ErrNotInstalled
	}
	return m.doCall(rpcURL, secret, method, params...)
}

// doCall 发起实际的 HTTP POST 到 aria2 JSON-RPC
func (m *Aria2Manager) doCall(rpcURL, secret, method string, params ...any) (json.RawMessage, error) {
	full := make([]any, 0, len(params)+1)
	if secret != "" {
		full = append(full, "token:"+secret)
	}
	full = append(full, params...)
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: "1", Method: method, Params: full})
	resp, err := http.Post(rpcURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("aria2 rpc call failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var r rpcResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("aria2 rpc invalid response: %s", truncate(raw))
	}
	if r.Error != nil {
		return nil, fmt.Errorf("aria2 error %d: %s", r.Error.Code, r.Error.Message)
	}
	return r.Result, nil
}

func truncate(b []byte) string {
	if len(b) > 256 {
		return string(b[:256])
	}
	return string(b)
}

func normOpts(opts map[string]any) map[string]any {
	if opts == nil {
		return map[string]any{}
	}
	return opts
}

// AddURI 调用 aria2.addUri
func (m *Aria2Manager) AddURI(uris []string, options map[string]any) (string, error) {
	res, err := m.call("aria2.addUri", uris, normOpts(options))
	if err != nil {
		return "", err
	}
	var gid string
	if err := json.Unmarshal(res, &gid); err != nil {
		return "", err
	}
	return gid, nil
}

// AddTorrent 调用 aria2.addTorrent，torrentData 会被 base64 编码
func (m *Aria2Manager) AddTorrent(torrentData []byte, options map[string]any) (string, error) {
	enc := base64.StdEncoding.EncodeToString(torrentData)
	res, err := m.call("aria2.addTorrent", enc, []string{}, normOpts(options))
	if err != nil {
		return "", err
	}
	var gid string
	if err := json.Unmarshal(res, &gid); err != nil {
		return "", err
	}
	return gid, nil
}

// AddMetalink 调用 aria2.addMetalink，返回 gid 列表
func (m *Aria2Manager) AddMetalink(metalinkData []byte, options map[string]any) ([]string, error) {
	enc := base64.StdEncoding.EncodeToString(metalinkData)
	res, err := m.call("aria2.addMetalink", enc, normOpts(options))
	if err != nil {
		return nil, err
	}
	var gids []string
	if err := json.Unmarshal(res, &gids); err != nil {
		return nil, err
	}
	return gids, nil
}

// TellStatus 调用 aria2.tellStatus
func (m *Aria2Manager) TellStatus(gid string) (map[string]any, error) {
	res, err := m.call("aria2.tellStatus", gid)
	if err != nil {
		return nil, err
	}
	var status map[string]any
	if err := json.Unmarshal(res, &status); err != nil {
		return nil, err
	}
	return status, nil
}

// Remove 调用 aria2.remove
func (m *Aria2Manager) Remove(gid string) error {
	_, err := m.call("aria2.remove", gid)
	return err
}

// Pause 调用 aria2.pause
func (m *Aria2Manager) Pause(gid string) error {
	_, err := m.call("aria2.pause", gid)
	return err
}

// Unpause 调用 aria2.unpause
func (m *Aria2Manager) Unpause(gid string) error {
	_, err := m.call("aria2.unpause", gid)
	return err
}

// GetVersion 调用 aria2.getVersion
func (m *Aria2Manager) GetVersion() (map[string]any, error) {
	res, err := m.call("aria2.getVersion")
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err := json.Unmarshal(res, &v); err != nil {
		return nil, err
	}
	return v, nil
}
