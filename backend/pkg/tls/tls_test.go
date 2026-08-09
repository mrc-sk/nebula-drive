package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSSLDir(t *testing.T) {
	if SSLDir() == "" {
		t.Fatal("empty ssl dir")
	}
}

func TestStartACMEMissingDomain(t *testing.T) {
	if _, _, err := StartACME(""); err == nil {
		t.Fatal("expected error for empty domain")
	}
}

func TestStartACMESuccess(t *testing.T) {
	cfg, h, err := StartACME("example.com")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg == nil || cfg.GetCertificate == nil {
		t.Fatal("invalid tls config (expected GetCertificate set)")
	}
	if h == nil {
		t.Fatal("nil challenge handler")
	}
}

func TestLoadManualMissingFiles(t *testing.T) {
	if _, err := LoadManual("/nonexistent/cert.pem", "/nonexistent/key.pem"); err == nil {
		t.Fatal("expected error for missing cert files")
	}
}

func TestLoadManualValid(t *testing.T) {
	certPath, keyPath := generateSelfSigned(t)
	cfg, err := LoadManual(certPath, keyPath)
	if err != nil {
		t.Fatalf("load manual: %v", err)
	}
	if cfg == nil || len(cfg.Certificates) != 1 {
		t.Fatal("invalid manual config")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("min version = %v", cfg.MinVersion)
	}
	if !HasManualConfig() {
		t.Fatal("HasManualConfig should be true")
	}
}

func TestGetTLSConfigOffWhenNoDB(t *testing.T) {
	// db 未初始化时 settingStr 返回默认 "off" → nil
	if cfg := GetTLSConfig(); cfg != nil {
		t.Fatal("expected nil config when db nil")
	}
}

// generateSelfSigned 生成自签 ECC 证书与私钥到临时目录，返回两个文件路径
func generateSelfSigned(t *testing.T) (string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	certOut, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		certOut.Close()
		t.Fatalf("encode cert: %v", err)
	}
	certOut.Close()
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyOut, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		keyOut.Close()
		t.Fatalf("encode key: %v", err)
	}
	keyOut.Close()
	return certPath, keyPath
}
