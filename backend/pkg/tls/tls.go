// Package tls 提供 HTTPS 双模式支持：ACME 自动申请 + 手动上传证书
package tls

import (
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"golang.org/x/crypto/acme/autocert"
)

const sslDir = "data/ssl"

var (
	mu        sync.Mutex
	manualCfg *tls.Config
	acmeMgr   *autocert.Manager
)

// SSLDir 返回证书存放目录
func SSLDir() string { return sslDir }

// StartACME 用 autocert 自动申请 Let's Encrypt 证书，缓存到 data/ssl/，
// 到期前自动续期（autocert 默认在证书到期前约 30 天续期）。
// 返回 TLS 配置与 80 端口 ACME challenge handler。
func StartACME(domain string) (*tls.Config, http.Handler, error) {
	if domain == "" {
		return nil, nil, errors.New("domain required for ACME")
	}
	if err := os.MkdirAll(sslDir, 0o700); err != nil {
		return nil, nil, err
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache(sslDir),
	}
	mu.Lock()
	acmeMgr = m
	mu.Unlock()
	return m.TLSConfig(), m.HTTPHandler(nil), nil
}

// LoadManual 从 certPath/keyPath 加载手动上传的证书。
// 路径为空时默认 data/ssl/cert.pem 与 data/ssl/key.pem。
func LoadManual(certPath, keyPath string) (*tls.Config, error) {
	if certPath == "" {
		certPath = filepath.Join(sslDir, "cert.pem")
	}
	if keyPath == "" {
		keyPath = filepath.Join(sslDir, "key.pem")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	mu.Lock()
	manualCfg = cfg
	mu.Unlock()
	return cfg, nil
}

// GetTLSConfig 根据 settings tls.mode (auto/manual/off) 返回配置，off 或未配置返回 nil
func GetTLSConfig() *tls.Config {
	mode := settingStr("tls.mode", "off")
	switch strings.ToLower(mode) {
	case "auto":
		mu.Lock()
		m := acmeMgr
		mu.Unlock()
		if m != nil {
			return m.TLSConfig()
		}
		return nil
	case "manual":
		mu.Lock()
		cfg := manualCfg
		mu.Unlock()
		return cfg
	default:
		return nil
	}
}

// HasManualConfig 是否已加载手动证书
func HasManualConfig() bool {
	mu.Lock()
	defer mu.Unlock()
	return manualCfg != nil
}

// settingStr 从 settings 表读取字符串配置
func settingStr(key, def string) string {
	if db.Get() == nil {
		return def
	}
	var s models.Setting
	if err := db.Get().Where("`key` = ?", key).First(&s).Error; err != nil {
		return def
	}
	return s.Value
}
