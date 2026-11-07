// Package pluginset 把插件宿主接进启动流程。
//
// 单独成包而不写在 main.go 里，是为了能写测试：
// main.go 的 bootstrap() 依赖 conf/db 全局状态，测试里搭不起来。
package pluginset

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/internal/pluginhttp"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/host"
)

// InitPlugins 初始化插件宿主，并拉起已启用的插件。
//
// 必须在 db.Init 成功之后调用（要读 plugins 表），且应在
// migrations.Run 之前 —— 因为 onDBMigrate 钩子在迁移内部触发。
//
// **双层默认关闭**（按站长的选择）：
//  1. 全局插件总开关默认 false，站长需在后台显式开启并确认免责；
//  2. 单个插件安装后默认 disabled，需手动启用（启用时再弹一次风险提示）。
//
// 关闭时宿主完全不拉起任何插件子进程，也不注册可访问的插件页面。
var currentHostVersion string

func InitPlugins(hostVersion string) {
	currentHostVersion = hostVersion
	dataDir := conf.DataDir()
	pluginsDir := filepath.Join(dataDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o750); err != nil {
		log.Printf("[WARN] 创建插件目录失败: %v", err)
	}

	enabled := readGlobalEnabled()
	pluginhttp.Global().SetGlobalEnabled(enabled)
	if !enabled {
		log.Printf("[plugin] 插件系统默认关闭：后台「插件总开关」开启后才会拉起插件。" +
			"开启即表示站长已知悉风险（插件可注册 HTTP 页面、宿主透传鉴权）")
		return
	}
	startHost(hostVersion)
}

// EnableGlobal 站长在后台开启总开关后补初始化宿主。
// InitPlugins 在开关关闭时会提前 return，此时进程仍在运行，需要这个入口。
func EnableGlobal() {
	if !readGlobalEnabled() {
		return
	}
	pluginhttp.Global().SetGlobalEnabled(true)
	if host.Global() == nil {
		startHost(currentHostVersion)
		return
	}
	LoadEnabledPlugins()
}

func startHost(hostVersion string) {
	dataDir := conf.DataDir()
	pluginsDir := filepath.Join(dataDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o750); err != nil {
		log.Printf("[WARN] 创建插件目录失败: %v", err)
	}
	m := host.InitGlobal(host.Config{
		PluginsDir:  pluginsDir,
		DataDir:     dataDir,
		HostVersion: hostVersion,
		SiteURL:     siteURL(),
		AutoRestart: true,
		Log:         func(format string, args ...any) { log.Printf("[plugin] "+format, args...) },
		ConfigFor:   pluginConfig,
	})
	if m == nil {
		log.Printf("[WARN] 插件宿主初始化失败，插件功能不可用")
		return
	}
	log.Printf("[INFO] 插件宿主就绪，目录: %s（协议版本 v%d，兼容 v%d）",
		pluginsDir, plugin.ProtocolVersion, plugin.MinSupportedProtocolVersion)

	LoadEnabledPlugins()
}

// readGlobalEnabled 读取全局开关，缺省 false（默认关闭）。
func readGlobalEnabled() bool {
	if db.Get() == nil {
		return false
	}
	var s models.Setting
	if err := db.Get().Where("`key` = ?", "plugin.global_enabled").First(&s).Error; err != nil {
		return false
	}
	return s.Value == "1" || strings.EqualFold(s.Value, "true")
}

// LoadEnabledPlugins 拉起数据库中标记为启用的插件。
func LoadEnabledPlugins() {
	m := host.Global()
	if m == nil || db.Get() == nil {
		return
	}
	var list []models.Plugin
	if err := db.Get().Where("enabled = ?", true).Order("id asc").Find(&list).Error; err != nil {
		log.Printf("[WARN] 读取插件列表失败: %v", err)
		return
	}
	if len(list) == 0 {
		return
	}

	okCount := 0
	for _, p := range list {
		if err := loadOne(p); err != nil {
			log.Printf("[WARN] 插件 %s 启动失败: %v", p.Name, err)
			recordError(p.Name, err.Error())
			continue
		}
		okCount++
	}
	log.Printf("[INFO] 已启用插件 %d/%d 载入成功", okCount, len(list))
}

// loadOne 加载单个插件：先校验三文件契约（含解释器探测），
// 再启动 HTTP 页面服务，最后拉起 JSON-RPC 子进程。
//
// v1 插件自动走老路径（不要求 register.go / co.BEGG）。
// 纯页面插件（hooks 为空、pages 非空）不拉起 JSON-RPC 子进程。
func loadOne(p models.Plugin) error {
	dir := filepath.Join(dirOf(), p.Name)
	contract, err := plugin.LoadContract(dir, true)
	if err != nil {
		return err
	}

	// 登记 UI 扩展点并拉起插件自己的 HTTP 页面服务（PHP/Python/Node 均适用）
	if !contract.V1 && contract.Begg != nil {
		if err := pluginhttp.Global().Start(contract,
			siteURL(), domainOf(), boundIPOf(), listenAddrOf()); err != nil {
			return err
		}
	}

	// 纯页面插件没有 JSON-RPC 子进程
	if len(contract.Manifest.Hooks) == 0 {
		return nil
	}
	m := host.Global()
	return m.Load(host.LoadSpec{Name: p.Name, Dir: dir, Manifest: contract.Manifest})
}

// StopPlugins 停止全部插件进程（含页面服务）。
func StopPlugins() {
	pluginhttp.Global().StopAll()
	if m := host.Global(); m != nil {
		m.StopAll()
	}
}

// pluginConfig 提供某插件的配置（init 握手下发给插件）。
func pluginConfig(name string) json.RawMessage {
	if db.Get() == nil {
		return nil
	}
	var p models.Plugin
	if err := db.Get().Where("name = ?", name).First(&p).Error; err != nil {
		return nil
	}
	cfg := p.Config
	if cfg == "" {
		return nil
	}
	if !json.Valid([]byte(cfg)) {
		log.Printf("[WARN] 插件 %s 的配置不是合法 JSON，已忽略", name)
		return nil
	}
	return json.RawMessage(cfg)
}

func dirOf() string { return filepath.Join(conf.DataDir(), "plugins") }

func recordError(name, msg string) {
	if db.Get() == nil {
		return
	}
	r := []rune(msg)
	if len(r) > 2000 {
		msg = string(r[:2000]) + "..."
	}
	db.Get().Model(&models.Plugin{}).Where("name = ?", name).
		Update("last_error", msg)
}

// siteURL 推导站点根 URL。
func siteURL() string {
	c := conf.Current()
	if c == nil {
		return ""
	}
	if c.System.Domain != "" {
		scheme := "https"
		if c.System.TLSMode == "off" || c.System.Protocol == "http" {
			scheme = "http"
		}
		return scheme + "://" + c.System.Domain
	}
	if c.System.Listen != "" {
		return "http://127.0.0.1" + normalizePort(c.System.Listen)
	}
	return ""
}

func domainOf() string {
	c := conf.Current()
	if c == nil {
		return ""
	}
	return c.System.Domain
}

// boundIPOf 从监听地址取绑定的 IP（0.0.0.0 / :: 视为未绑定具体 IP）。
func boundIPOf() string {
	c := conf.Current()
	if c == nil || c.System.Listen == "" {
		return ""
	}
	hostPart, _, err := net.SplitHostPort(c.System.Listen)
	if err != nil {
		return ""
	}
	if hostPart == "" || hostPart == "0.0.0.0" || hostPart == "::" {
		return ""
	}
	return hostPart
}

func listenAddrOf() string {
	c := conf.Current()
	if c == nil {
		return ""
	}
	return c.System.Listen
}

func normalizePort(listen string) string {
	for i := len(listen) - 1; i >= 0; i-- {
		if listen[i] == ':' {
			return listen[i:]
		}
	}
	return ""
}
