// Package pluginset 把插件宿主接进启动流程。
//
// 单独成包而不写在 main.go 里，是为了能写测试：
// main.go 的 bootstrap() 依赖 conf/db 全局状态，测试里搭不起来。
package pluginset

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/host"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
)

// InitPlugins 初始化插件宿主，并拉起已启用的插件。
//
// 必须在 db.Init 成功之后调用（要读 plugins 表），且应在
// migrations.Run 之前 —— 因为 onDBMigrate 钩子在迁移内部触发，
// 晚于迁移初始化就会错过本次启动的唯一一次触发机会。
// （该钩子每次启动都触发，漏掉本次下轮启动也能补上，不算致命。）
//
// 单个插件启动失败只记日志不中断：插件是可选增强，
// 不该因为第三方插件坏了就让整个网盘起不来。
func InitPlugins(hostVersion string) {
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
	log.Printf("[INFO] 插件宿主就绪，目录: %s（协议版本 v%d）", pluginsDir, plugin.ProtocolVersion)

	LoadEnabledPlugins()
}

// LoadEnabledPlugins 拉起数据库中标记为启用的插件。
//
// 逐个独立处理：一个插件的清单损坏/二进制丢失，不该拖垮其他插件。
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

func loadOne(p models.Plugin) error {
	m := host.Global()
	dir := filepath.Join(dirOf(), p.Name)
	// 以磁盘上的清单为准：数据库里的冗余字段可能过期
	// （插件被手工删了、换了版本、或只改了库没改文件）。
	man, err := install.ReadManifest(dir)
	if err != nil {
		return err
	}
	return m.Load(host.LoadSpec{Name: p.Name, Dir: dir, Manifest: man})
}

// StopPlugins 停止全部插件进程。宿主退出前调用，避免留下孤儿进程。
func StopPlugins() {
	if m := host.Global(); m != nil {
		m.StopAll()
	}
}

// pluginConfig 提供某插件的配置（init 握手下发给插件）。
//
// 读失败返回 nil 而不是空 JSON：插件能据此判断"管理员没配过"，
// 便于它决定用默认值还是报错退出。
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

// dirOf 插件安装根目录。
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

// siteURL 推导站点根 URL，供需要回调宿主的插件使用。
//
// 优先用配置里的域名（对外可达），退回监听地址（本机调试）。
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

// normalizePort 从监听地址里取出端口部分。
// ":8080" → ":8080"；"0.0.0.0:8080" → ":8080"；无冒号则原样返回。
func normalizePort(listen string) string {
	for i := len(listen) - 1; i >= 0; i-- {
		if listen[i] == ':' {
			return listen[i:]
		}
	}
	return ""
}
