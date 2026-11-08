// Package ai 实现「智能时代」的三档 AI 能力：
//
//   - L0 本地算法：不配任何模型也能用的联想、近期常用、词法补全、摘要。
//     见 local.go，零依赖、零出域、零成本。
//   - L1 AI 网关：后台可配 OpenAI 兼容端点（OpenAI / 智谱 / DeepSeek /
//     Ollama / 任意自建网关），统一超时、配额、审计。见 gateway.go。
//   - L3 协议开放：社区插件订阅 onAIProviderResolve 或注册 ai Provider
//     即可完全接管模型调用，宿主不写死任何厂商。见 gateway.go。
//
// 三档顺序：插件接管 → 内置网关 → 本地兜底。任一档不可用自动往下降，
// 因此「没填 key」不会让智能功能报错，只会退化到 L0。
package ai

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

// Setting key 前缀统一为 ai_，与站点其它设置共用 models.Setting KV 表。
const (
	KeyEnabled      = "ai_enabled"
	KeyBaseURL      = "ai_base_url"
	KeyAPIKey       = "ai_api_key"
	KeyModel        = "ai_model"
	KeyTimeoutSec   = "ai_timeout_sec"
	KeyMaxTokens    = "ai_max_tokens"
	KeyTemperature  = "ai_temperature"
	KeyProvider     = "ai_provider"
	KeyCodeSpace    = "ai_code_space_enabled"
	KeyScenes       = "ai_scenes"
	KeyRateLimit    = "ai_rate_limit"
	KeyAutoSummary  = "ai_auto_summary"
	KeyAutoTag      = "ai_auto_tag"
	MaskPlaceholder = "******"
)

// Scene 场景名，用于场景开关与审计分类。
const (
	SceneSearch    = "search"    // 自然语言搜文件
	SceneRecommend = "recommend" // 搜索联想 / 近期常用 / 热门
	SceneComplete  = "complete"  // 代码补全
	SceneSummary   = "summary"   // 摘要与自动标签
	SceneAsk       = "ask"       // AI 助手问答
)

// AllScenes 全部可开关场景。
var AllScenes = []string{SceneSearch, SceneRecommend, SceneComplete, SceneSummary, SceneAsk}

// Config 后台可配的 AI 设置。
type Config struct {
	Enabled         bool            `json:"enabled"`
	BaseURL         string          `json:"baseUrl"`
	APIKey          string          `json:"apiKey"`
	Model           string          `json:"model"`
	TimeoutSec      int             `json:"timeoutSec"`
	MaxTokens       int             `json:"maxTokens"`
	Temperature     float64         `json:"temperature"`
	Provider        string          `json:"provider"`
	CodeSpaceEnable bool            `json:"codeSpaceEnabled"`
	AutoSummary     bool            `json:"autoSummary"`
	AutoTag         bool            `json:"autoTag"`
	RateLimit       int             `json:"rateLimit"`
	Scenes          map[string]bool `json:"scenes"`
}

// DefaultConfig 出厂值：AI 与代码空间都默认关闭，场景全开（等总开关打开后生效）。
func DefaultConfig() Config {
	scenes := map[string]bool{}
	for _, s := range AllScenes {
		scenes[s] = true
	}
	return Config{
		Enabled:     false,
		BaseURL:     "https://api.openai.com/v1",
		Model:       "gpt-4o-mini",
		TimeoutSec:  30,
		MaxTokens:   1024,
		Temperature: 0.3,
		RateLimit:   30,
		Scenes:      scenes,
	}
}

func (c Config) normalize() Config {
	d := DefaultConfig()
	if c.TimeoutSec <= 0 || c.TimeoutSec > 300 {
		c.TimeoutSec = d.TimeoutSec
	}
	if c.MaxTokens <= 0 || c.MaxTokens > 8192 {
		c.MaxTokens = d.MaxTokens
	}
	if c.Temperature < 0 || c.Temperature > 2 {
		c.Temperature = d.Temperature
	}
	if c.RateLimit <= 0 || c.RateLimit > 1000 {
		c.RateLimit = d.RateLimit
	}
	if c.BaseURL == "" {
		c.BaseURL = d.BaseURL
	}
	if c.Model == "" {
		c.Model = d.Model
	}
	if c.Scenes == nil {
		c.Scenes = d.Scenes
	} else {
		merged := map[string]bool{}
		for _, s := range AllScenes {
			if v, ok := c.Scenes[s]; ok {
				merged[s] = v
			} else {
				merged[s] = true
			}
		}
		c.Scenes = merged
	}
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.APIKey = strings.TrimSpace(c.APIKey)
	return c
}

// SceneEnabled 某场景是否允许调用模型（总开关 + 场景开关）。
func (c Config) SceneEnabled(scene string) bool {
	if !c.Enabled {
		return false
	}
	if c.Scenes == nil {
		return true
	}
	v, ok := c.Scenes[scene]
	return !ok || v
}

var (
	cacheMu  sync.RWMutex
	cache    Config
	cacheAt  time.Time
	cacheTTL = 3 * time.Second
)

func invalidate() {
	cacheMu.Lock()
	cacheAt = time.Time{}
	cacheMu.Unlock()
}

// InvalidateCache 供后台保存配置与测试调用，强制下次 LoadConfig 重新读库。
func InvalidateCache() { invalidate() }

func readAllSettings() map[string]string {
	out := map[string]string{}
	d := db.Get()
	if d == nil {
		return out
	}
	var rows []models.Setting
	if err := d.Find(&rows).Error; err != nil {
		return out
	}
	for _, s := range rows {
		out[s.Key] = s.Value
	}
	return out
}

func boolOf(s string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(s))
	return b
}

// LoadConfig 读取配置，带 3 秒缓存（AI 请求是热路径，不能每次查库）。
func LoadConfig() Config {
	cacheMu.RLock()
	if !cacheAt.IsZero() && time.Since(cacheAt) < cacheTTL {
		c := cache
		cacheMu.RUnlock()
		return c
	}
	cacheMu.RUnlock()

	m := readAllSettings()
	c := DefaultConfig()
	if v, ok := m[KeyEnabled]; ok {
		c.Enabled = boolOf(v)
	}
	if v, ok := m[KeyBaseURL]; ok && v != "" {
		c.BaseURL = v
	}
	if v, ok := m[KeyAPIKey]; ok {
		c.APIKey = v
	}
	if v, ok := m[KeyModel]; ok && v != "" {
		c.Model = v
	}
	if v, ok := m[KeyTimeoutSec]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.TimeoutSec = n
		}
	}
	if v, ok := m[KeyMaxTokens]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxTokens = n
		}
	}
	if v, ok := m[KeyTemperature]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Temperature = f
		}
	}
	if v, ok := m[KeyProvider]; ok {
		c.Provider = v
	}
	if v, ok := m[KeyCodeSpace]; ok {
		c.CodeSpaceEnable = boolOf(v)
	}
	if v, ok := m[KeyAutoSummary]; ok {
		c.AutoSummary = boolOf(v)
	}
	if v, ok := m[KeyAutoTag]; ok {
		c.AutoTag = boolOf(v)
	}
	if v, ok := m[KeyRateLimit]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.RateLimit = n
		}
	}
	if v, ok := m[KeyScenes]; ok && v != "" {
		sc := map[string]bool{}
		if json.Unmarshal([]byte(v), &sc) == nil {
			for _, s := range AllScenes {
				if x, ok := sc[s]; ok {
					c.Scenes[s] = x
				}
			}
		}
	}
	c = c.normalize()

	cacheMu.Lock()
	cache, cacheAt = c, time.Now()
	cacheMu.Unlock()
	return c
}

var errDBNotReady = errors.New("AI 配置：数据库尚未初始化")

// SaveConfig 保存配置。apiKey 传 MaskPlaceholder 表示「不修改原密钥」。
func SaveConfig(c Config) error {
	d := db.Get()
	if d == nil {
		return errDBNotReady
	}
	old := LoadConfig()
	if c.APIKey == MaskPlaceholder || c.APIKey == "" {
		c.APIKey = old.APIKey
	}
	c = c.normalize()
	scenesJSON, _ := json.Marshal(c.Scenes)
	values := map[string]string{
		KeyEnabled:     strconv.FormatBool(c.Enabled),
		KeyBaseURL:     c.BaseURL,
		KeyAPIKey:      c.APIKey,
		KeyModel:       c.Model,
		KeyTimeoutSec:  strconv.Itoa(c.TimeoutSec),
		KeyMaxTokens:   strconv.Itoa(c.MaxTokens),
		KeyTemperature: strconv.FormatFloat(c.Temperature, 'f', -1, 64),
		KeyProvider:    c.Provider,
		KeyCodeSpace:   strconv.FormatBool(c.CodeSpaceEnable),
		KeyAutoSummary: strconv.FormatBool(c.AutoSummary),
		KeyAutoTag:     strconv.FormatBool(c.AutoTag),
		KeyRateLimit:   strconv.Itoa(c.RateLimit),
		KeyScenes:      string(scenesJSON),
	}
	for k, v := range values {
		// 用 Find 而不是 First：First 查不到会打一条 "record not found" 日志，
		// 15 个 key 首次保存就刷 15 行噪音（看起来像报错，其实是正常路径）。
		var rows []models.Setting
		if err := d.Where("`key` = ?", k).Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			if err := d.Create(&models.Setting{Key: k, Value: v}).Error; err != nil {
				return err
			}
		} else if err := d.Model(&rows[0]).Update("value", v).Error; err != nil {
			return err
		}
	}
	invalidate()
	return nil
}

// PublicConfig 返回给后台页面的配置（密钥脱敏）。
func PublicConfig() map[string]any {
	c := LoadConfig()
	return map[string]any{
		"enabled":          c.Enabled,
		"baseUrl":          c.BaseURL,
		"apiKey":           MaskPlaceholder,
		"apiKeySet":        c.APIKey != "",
		"model":            c.Model,
		"timeoutSec":       c.TimeoutSec,
		"maxTokens":        c.MaxTokens,
		"temperature":      c.Temperature,
		"provider":         c.Provider,
		"codeSpaceEnabled": c.CodeSpaceEnable,
		"autoSummary":      c.AutoSummary,
		"autoTag":          c.AutoTag,
		"rateLimit":        c.RateLimit,
		"scenes":           c.Scenes,
		"available":        c.Enabled && (c.APIKey != "" || c.Provider != ""),
		"mode":             ModeOf(c),
	}
}

// ModeOf 描述当前生效档位，用于后台展示。
func ModeOf(c Config) string {
	if !c.Enabled {
		return "local"
	}
	if c.Provider != "" {
		return "plugin"
	}
	if c.APIKey != "" {
		return "gateway"
	}
	return "local"
}
