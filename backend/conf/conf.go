package conf

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/ini.v1"
)

// 配置层：敏感字段（数据库密码、SMTP 密码、JWT 密钥等）加密存 env，
// 非敏感字段（站点名、端口、时区等）存 ini 做辅助。
// env 文件路径默认 data/conf.env，ini 路径默认 data/conf.ini。
// 加密密钥本身存 data/secret.key（首次安装生成），丢失则配置不可解密。

type DBConfig struct {
	Type     string `json:"type"` // sqlite | mysql | postgres
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"` // 加密存储
	File     string `json:"file"`     // sqlite 文件路径
}

type SystemConfig struct {
	SiteName     string `json:"siteName"`
	Domain       string `json:"domain"`
	Protocol     string `json:"protocol"` // http | https
	Listen       string `json:"listen"`   // 监听地址
	TimeZone     string `json:"timeZone"`
	Language     string `json:"language"`
	UploadPath   string `json:"uploadPath"`
	DefaultStore string `json:"defaultStore"` // local | s3 | oss | cos
	TLSMode      string `json:"tlsMode"`      // off/auto/manual
	TLSDomain    string `json:"tlsDomain"`    // ACME 用的域名
}

type AdminConfig struct {
	UserName string `json:"userName"`
	Password string `json:"password"` // 加密存储（仅安装时使用，之后只存哈希）
	Email    string `json:"email"`
}

type MailConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"` // 加密存储
	From     string `json:"from"`
}

type RedisConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"` // 加密存储
	DB       int    `json:"db"`
}

type Config struct {
	DB     DBConfig     `json:"db"`
	System SystemConfig `json:"system"`
	Admin  AdminConfig  `json:"admin"`
	Mail   MailConfig   `json:"mail"`
	Redis  RedisConfig  `json:"redis"`
}

var (
	current     *Config
	currentMu   sync.RWMutex
	secretKey   []byte
	dataDir     = "data"
	installed   bool
	installedMu sync.RWMutex
)

// DataDir 返回数据目录
func DataDir() string { return dataDir }

// SetDataDir 设置数据目录
func SetDataDir(d string) { dataDir = d }

// envPath / iniPath / keyPath
func envPath() string { return filepath.Join(dataDir, "conf.env") }
func iniPath() string { return filepath.Join(dataDir, "conf.ini") }
func keyPath() string { return filepath.Join(dataDir, "secret.key") }

// IsInstalled 是否已安装（data 目录下有 conf.env 且可解密）
func IsInstalled() bool {
	installedMu.RLock()
	defer installedMu.RUnlock()
	return installed
}

func markInstalled(v bool) {
	installedMu.Lock()
	installed = v
	installedMu.Unlock()
}

// Load 启动时加载配置
func Load() error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	key, err := loadOrCreateKey()
	if err != nil {
		return err
	}
	secretKey = key

	raw, err := os.ReadFile(envPath())
	if err != nil {
		if os.IsNotExist(err) {
			markInstalled(false)
			return nil
		}
		return err
	}
	dec, err := decrypt(secretKey, raw)
	if err != nil {
		markInstalled(false)
		return err
	}
	var c Config
	if err := json.Unmarshal(dec, &c); err != nil {
		return err
	}
	// ini 辅助覆盖非敏感字段
	if iniCfg, err := ini.Load(iniPath()); err == nil {
		applyIni(&c, iniCfg)
	}
	currentMu.Lock()
	current = &c
	currentMu.Unlock()
	markInstalled(true)
	return nil
}

// Current 获取当前配置
func Current() *Config {
	currentMu.RLock()
	defer currentMu.RUnlock()
	return current
}

// Save 安装向导写入配置：敏感字段加密到 env，非敏感到 ini
func Save(c *Config) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	if len(secretKey) == 0 {
		key, err := loadOrCreateKey()
		if err != nil {
			return err
		}
		secretKey = key
	}
	enc, err := encrypt(secretKey, mustJSON(c))
	if err != nil {
		return err
	}
	if err := os.WriteFile(envPath(), enc, 0o600); err != nil {
		return err
	}
	// 写 ini 辅助
	iniCfg := ini.Empty()
	writeIni(c, iniCfg)
	if err := iniCfg.SaveTo(iniPath()); err != nil {
		return err
	}
	currentMu.Lock()
	current = c
	currentMu.Unlock()
	markInstalled(true)
	return nil
}

func mustJSON(c *Config) []byte {
	b, _ := json.Marshal(c)
	return b
}

// ---- 加解密 ----

func loadOrCreateKey() ([]byte, error) {
	b, err := os.ReadFile(keyPath())
	if err == nil {
		return b[:32], nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath(), key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func encrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func decrypt(key, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ct) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], nil)
}

// EncryptString / DecryptString 供外部使用（如数据库密码单独加密）
func EncryptString(s string) (string, error) {
	if len(secretKey) == 0 {
		return "", errors.New("secret key not loaded")
	}
	b, err := encrypt(secretKey, []byte(s))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func DecryptString(s string) (string, error) {
	if len(secretKey) == 0 {
		return "", errors.New("secret key not loaded")
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	dec, err := decrypt(secretKey, b)
	if err != nil {
		return "", err
	}
	return string(dec), nil
}

// ---- ini 辅助 ----

func applyIni(c *Config, f *ini.File) {
	if s := f.Section("system"); s != nil {
		if v := s.Key("siteName").String(); v != "" {
			c.System.SiteName = v
		}
		if v := s.Key("domain").String(); v != "" {
			c.System.Domain = v
		}
		if v := s.Key("protocol").String(); v != "" {
			c.System.Protocol = v
		}
		if v := s.Key("listen").String(); v != "" {
			c.System.Listen = v
		}
		if v := s.Key("timeZone").String(); v != "" {
			c.System.TimeZone = v
		}
		if v := s.Key("language").String(); v != "" {
			c.System.Language = v
		}
		if v := s.Key("uploadPath").String(); v != "" {
			c.System.UploadPath = v
		}
		if v := s.Key("defaultStore").String(); v != "" {
			c.System.DefaultStore = v
		}
		if v := s.Key("tlsMode").String(); v != "" {
			c.System.TLSMode = v
		}
		if v := s.Key("tlsDomain").String(); v != "" {
			c.System.TLSDomain = v
		}
	}
}

func writeIni(c *Config, f *ini.File) {
	s := f.Section("system")
	s.Key("siteName").SetValue(c.System.SiteName)
	s.Key("domain").SetValue(c.System.Domain)
	s.Key("protocol").SetValue(c.System.Protocol)
	s.Key("listen").SetValue(c.System.Listen)
	s.Key("timeZone").SetValue(c.System.TimeZone)
	s.Key("language").SetValue(c.System.Language)
	s.Key("uploadPath").SetValue(c.System.UploadPath)
	s.Key("defaultStore").SetValue(c.System.DefaultStore)
	s.Key("tlsMode").SetValue(c.System.TLSMode)
	s.Key("tlsDomain").SetValue(c.System.TLSDomain)
}
