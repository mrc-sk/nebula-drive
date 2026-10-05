package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/host"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
	"gorm.io/gorm"
)

// 插件管理。
//
// 与旧实现的根本区别：**enabled 字段真正接线了**。
// 启用 = 拉起子进程，禁用 = 杀掉子进程，主服务不重启（热加载）。
// 旧实现只翻转数据库布尔值，插件代码根本不读它。

// pluginService 组装插件相关的依赖。便于测试注入。
type pluginService struct {
	host *host.Manager
	inst *install.Installer
}

func pluginSvc() *pluginService {
	return &pluginService{
		host: host.Global(),
		inst: install.New(pluginsRootDir()),
	}
}

// pluginsRootDir 插件安装根目录（数据目录下的 plugins）。
func pluginsRootDir() string {
	return filepath.Join(dataDirOrCwd(), "plugins")
}

func dataDirOrCwd() string {
	if d := os.Getenv("NEBULA_DATA_DIR"); d != "" {
		return d
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// requireHost 返回全局 Manager，未初始化时报明确错误。
func (s *pluginService) requireHost(c *gin.Context) bool {
	if s.host == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code": 503, "message": "插件宿主未初始化",
		})
		return false
	}
	return true
}

// ---- 协议同意 ----

// GetPluginAgreement 返回插件协议全文与当前同意状态。
func GetPluginAgreement(c *gin.Context) {
	ag := plugin.CurrentAgreement()
	acc := readAgreement()
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"agreement": ag,
			"accepted":  acc != nil && acc.Version >= ag.Version,
			"record":    acc,
			"version":   ag.Version,
		},
	})
}

// AcceptPluginAgreement 记录管理员对插件协议的同意。
func AcceptPluginAgreement(c *gin.Context) {
	var body struct {
		Version int `json:"version"`
		// 必须显式确认：防止前端"打开弹窗就自动同意"
		Accept bool `json:"accept"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if !body.Accept {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "必须显式确认同意（accept=true）",
		})
		return
	}
	cur := plugin.CurrentAgreement()
	if body.Version != cur.Version {
		// 管理员看到的是另一个版本的协议（可能刚升级），
		// 静默按当前版记录会让他"同意"一份没读过的内容。
		c.JSON(http.StatusConflict, gin.H{
			"code": 409,
			"message": fmt.Sprintf("协议版本不匹配：你提交的是 v%d，当前是 v%d，请刷新后重新阅读",
				body.Version, cur.Version),
		})
		return
	}

	u := middleware.CurrentUser(c)
	rec := plugin.AgreementAccepted{
		Version:    cur.Version,
		AcceptedAt: time.Now().Format(time.RFC3339),
		AcceptedBy: u.ID,
		UserName:   u.UserName,
		IP:         c.ClientIP(),
	}
	raw, _ := json.Marshal(rec)
	saveSetting(plugin.AgreementKey, string(raw))

	auditPlugin(c, "plugin_agreement_accept", cur.Title,
		fmt.Sprintf("agreementVersion=%d", cur.Version))

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": rec})
}

func readAgreement() *plugin.AgreementAccepted {
	raw, ok := loadSetting(plugin.AgreementKey)
	if !ok || raw == "" {
		return nil
	}
	var acc plugin.AgreementAccepted
	if err := json.Unmarshal([]byte(raw), &acc); err != nil {
		return nil
	}
	return &acc
}

// agreementOK 校验当前用户是否已同意现行协议。
func agreementOK() bool {
	acc := readAgreement()
	return acc != nil && acc.Version >= plugin.AgreementVersion
}

// requireAgreement 是安装/启用路径的强制关卡。
//
// 必须在**后端**拦：只靠前端弹窗的话，直接调 API 就能装插件。
func requireAgreement(c *gin.Context) bool {
	if agreementOK() {
		return true
	}
	cur := plugin.CurrentAgreement()
	c.JSON(http.StatusForbidden, gin.H{
		"code": 403,
		"message": "尚未同意插件协议",
		"data": gin.H{
			"needAgreement": true,
			"version":       cur.Version,
			"title":         cur.Title,
		},
	})
	return false
}

// ---- 列表与状态 ----

// PluginView 列表项（数据库记录 + 实时运行状态）。
type PluginView struct {
	models.Plugin
	// Status 实时状态：running/stopped/crashed/failed/...
	Status string `json:"status"`
	// PID 当前进程号，0 表示未运行。
	PID int `json:"pid"`
	// RuntimeError 运行时错误（来自宿主，优先于 LastError）。
	RuntimeError string `json:"runtimeError,omitempty"`
	// RestartsNow 宿主记录的本次运行重启次数。
	RestartsNow int `json:"restartsNow"`
	// HooksList 钩子列表（切片形式，便于前端渲染）。
	HooksList []string `json:"hooksList"`
	// Permissions 权限声明（从 manifest 读，不落库）。
	Permissions []string `json:"permissions,omitempty"`
	// Dir 插件安装目录。
	Dir string `json:"dir,omitempty"`
	// ManifestValid 磁盘上的清单是否仍可解析。
	// 为 false 说明文件被手工删了/损坏了，启用会失败。
	ManifestValid bool `json:"manifestValid"`
	// ManifestError 清单无效的原因。
	ManifestError string `json:"manifestError,omitempty"`
}

// ListPlugins 插件列表（含实时运行状态）。
func ListPlugins(c *gin.Context) {
	s := pluginSvc()
	var plugins []models.Plugin
	if err := db.Get().Order("id asc").Find(&plugins).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	views := make([]PluginView, 0, len(plugins))
	for _, p := range plugins {
		v := PluginView{Plugin: p, Status: host.StatusStopped}
		v.HooksList = p.PluginHookList()
		if s.inst != nil {
			v.Dir = filepath.Join(s.inst.PluginsDir, p.Name)
			// 以磁盘上的清单为准，不信任数据库里的冗余字段
			if man, err := install.ReadManifest(v.Dir); err == nil {
				v.ManifestValid = true
				v.Permissions = man.Permissions
				if v.Version == "" {
					v.Version = man.Version
				}
			} else {
				v.ManifestValid = false
				v.ManifestError = manifestErrBrief(err)
			}
		}
		if s.host != nil {
			if in := s.host.Get(p.Name); in != nil {
				st := in.Status()
				v.Status = st.Status
				v.PID = st.PID
				v.RestartsNow = st.Restarts
				if st.LastErr != "" {
					v.RuntimeError = st.LastErr
				}
			}
		}
		if p.LastError != "" && v.RuntimeError == "" {
			v.RuntimeError = p.LastError
		}
		// 数据库说启用但进程没跑 —— 明确暴露这个矛盾，
		// 否则管理员会看到"已启用"却毫无效果
		if p.Enabled && v.Status == host.StatusStopped {
			v.RuntimeError = orDefault(v.RuntimeError,
				"已标记启用但进程未运行，可能启动失败或已被手动结束")
		}
		views = append(views, v)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": views,
		"meta": gin.H{
			"hostReady": s.host != nil,
			"pluginsDir": func() string {
				if s.inst != nil {
					return s.inst.PluginsDir
				}
				return ""
			}(),
		},
	})
}

func manifestErrBrief(err error) string {
	m := err.Error()
	if i := strings.Index(m, ": "); i > 0 && i < 40 {
		return m[:i] // 只取第一段，太长不适合表格展示
	}
	return m
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}

// ---- 安装 / 卸载 ----

// InstallPlugin 安装插件。
//
// body.source 决定来源：
//   - {"source":"local","path":"/abs/path"}   本地目录
//   - {"source":"url","url":"https://..."}    远端 zip
//
// 装完**不自动启用**：先让管理员确认协议与权限，再显式启用。
// 自动启用会让"同意协议"这一步形同虚设。
func InstallPlugin(c *gin.Context) {
	if !requireAgreement(c) {
		return
	}
	s := pluginSvc()
	var body struct {
		Source string `json:"source"`
		Path   string `json:"path"`
		URL    string `json:"url"`
		Enable bool   `json:"enable"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if s.inst == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "插件安装器未就绪"})
		return
	}

	var (
		res *install.Result
		err error
	)
	ref := ""
	switch body.Source {
	case "local":
		if body.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "缺少 path"})
			return
		}
		ref = models.PluginSourceLocal + ":" + body.Path
		res, err = s.inst.InstallFromDir(body.Path, ref)
	case "url", "store":
		if body.URL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "缺少 url"})
			return
		}
		ref = models.PluginSourceURL + ":" + body.URL
		res, err = s.inst.InstallFromURL(body.URL, ref)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "source 必须是 local 或 url",
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "安装失败: " + err.Error()})
		return
	}

	// 落库（覆盖同名的旧记录）
	u := middleware.CurrentUser(c)
	now := time.Now()
	hooksJoined := strings.Join(res.Manifest.Hooks, ",")
	var p models.Plugin
	err = db.Get().Where("name = ?", res.Name).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		p = models.Plugin{
			Name:             res.Name,
			InstalledBy:      u.ID,
			InstalledAt:      now,
			AgreementVersion: plugin.AgreementVersion,
		}
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	p.Title = res.Manifest.Title
	p.Version = res.Manifest.Version
	p.Author = res.Manifest.Author
	p.Description = res.Manifest.Description
	p.License = res.Manifest.License
	p.Homepage = res.Manifest.Homepage
	p.Hooks = hooksJoined
	p.InstallSource = ref
	p.AgreementVersion = plugin.AgreementVersion
	p.InstalledAt = now
	p.LastError = ""
	p.Restarts = 0
	rawMan, _ := json.Marshal(res.Manifest)
	p.Manifest = string(rawMan)
	// 注意：不改 p.Enabled —— 启用要走显式的 enable 接口

	if err := db.Get().Save(&p).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	auditPlugin(c, "plugin_install", p.Name,
		fmt.Sprintf("source=%s files=%d bytes=%d replaced=%v",
			ref, res.Files, res.Bytes, res.Replaced))

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"plugin": p,
		"result": res,
		"message": fmt.Sprintf("已安装 %s（%d 个文件）。需要点击「启用」才会真正加载。",
			res.Name, res.Files),
	}})
}

// UninstallPlugin 卸载插件：停进程 + 删目录 + 删记录。
func UninstallPlugin(c *gin.Context) {
	if !requireAgreement(c) {
		return
	}
	s := pluginSvc()
	name := c.Param("name")
	var p models.Plugin
	if err := db.Get().Where("name = ?", name).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "plugin not found"})
		return
	}
	// 先停进程再删目录：反过来的话子进程仍持有可执行文件句柄
	if s.host != nil {
		s.host.Unload(name)
	}
	removed := false
	if s.inst != nil {
		ok, err := s.inst.Uninstall(name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1, "message": "删除插件目录失败: " + err.Error(),
			})
			return
		}
		removed = ok
	}
	if err := db.Get().Where("id = ?", p.ID).Delete(&models.Plugin{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	auditPlugin(c, "plugin_uninstall", name, fmt.Sprintf("dirRemoved=%v", removed))
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"removed": removed, "name": name,
	}})
}

// ---- 启停（热加载核心）----

// EnablePlugin 启用插件：拉起子进程。
//
// 成功与否立即反映到 HTTP 返回值 —— 管理员能看到"启动失败：缺少配置 x"
// 而不是开关变绿却毫无作用。
func EnablePlugin(c *gin.Context) {
	if !requireAgreement(c) {
		return
	}
	s := pluginSvc()
	if !s.requireHost(c) {
		return
	}
	name := c.Param("name")
	var p models.Plugin
	if err := db.Get().Where("name = ?", name).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "plugin not found"})
		return
	}
	// 协议版本变了必须重新同意 —— 旧同意不能覆盖新协议
	if p.AgreementVersion < plugin.AgreementVersion {
		c.JSON(http.StatusForbidden, gin.H{
			"code": 403, "message": "插件协议已更新，请重新阅读并同意后再启用",
			"data": gin.H{"needAgreement": true, "version": plugin.AgreementVersion},
		})
		return
	}

	dir := filepath.Join(s.inst.PluginsDir, name)
	man, err := install.ReadManifest(dir)
	if err != nil {
		recordPluginError(name, "读取清单失败: "+err.Error())
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "读取插件清单失败: " + err.Error(),
		})
		return
	}

	// 允许在启用时一并改配置（装完再填配置是常见流程）
	if raw := c.PostForm("config"); raw != "" {
		if !json.Valid([]byte(raw)) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "config 不是合法 JSON"})
			return
		}
		p.Config = raw
		if err := db.Get().Model(&p).Update("config", raw).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
			return
		}
	}

	if err := s.host.Load(host.LoadSpec{Name: name, Dir: dir, Manifest: man}); err != nil {
		recordPluginError(name, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "启动插件失败: " + err.Error(),
		})
		return
	}

	db.Get().Model(&p).Updates(map[string]any{
		"enabled": true, "last_error": "", "version": man.Version,
		"hooks": strings.Join(man.Hooks, ","),
	})
	auditPlugin(c, "plugin_enable", name, fmt.Sprintf("hooks=%v", man.Hooks))
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"name": name, "status": host.StatusRunning, "pid": s.host.Get(name).Status().PID,
	}})
}

// DisablePlugin 禁用插件：杀掉子进程。热加载，不需要重启主服务。
func DisablePlugin(c *gin.Context) {
	s := pluginSvc()
	name := c.Param("name")
	var p models.Plugin
	if err := db.Get().Where("name = ?", name).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "plugin not found"})
		return
	}
	if s.host != nil {
		// 先尝试优雅退出，让插件清理临时文件
		if in := s.host.Get(name); in != nil && in.IsLive() {
			in.Shutdown(2 * time.Second)
		}
		s.host.Unload(name)
	}
	db.Get().Model(&p).Updates(map[string]any{"enabled": false, "restarts": 0})
	auditPlugin(c, "plugin_disable", name, "")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"name": name, "status": host.StatusStopped,
	}})
}

// RestartPlugin 重启插件：重读 manifest + 重启进程。
func RestartPlugin(c *gin.Context) {
	// 重启属于启用行为，同样要过协议关卡
	if !requireAgreement(c) {
		return
	}
	s := pluginSvc()
	if !s.requireHost(c) {
		return
	}
	name := c.Param("name")
	var p models.Plugin
	if err := db.Get().Where("name = ?", name).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "plugin not found"})
		return
	}
	if !p.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "插件未启用，请先启用再重启",
		})
		return
	}
	dir := filepath.Join(s.inst.PluginsDir, name)
	man, err := install.ReadManifest(dir)
	if err != nil {
		recordPluginError(name, "读取清单失败: "+err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if err := s.host.Reload(host.LoadSpec{Name: name, Dir: dir, Manifest: man}); err != nil {
		recordPluginError(name, err.Error())
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "重启失败: " + err.Error(),
		})
		return
	}
	db.Get().Model(&p).Updates(map[string]any{"last_error": "", "version": man.Version})
	auditPlugin(c, "plugin_restart", name, "")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"name": name, "status": host.StatusRunning,
	}})
}

// UpdatePluginConfig 更新插件配置（下次启用/重启时生效）。
func UpdatePluginConfig(c *gin.Context) {
	name := c.Param("name")
	var body struct {
		Config string `json:"config"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if body.Config != "" && !json.Valid([]byte(body.Config)) {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400, "message": "config 不是合法 JSON",
		})
		return
	}
	var p models.Plugin
	if err := db.Get().Where("name = ?", name).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "plugin not found"})
		return
	}
	if err := db.Get().Model(&p).Update("config", body.Config).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	auditPlugin(c, "plugin_config_update", name, "")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"name": name,
		"hint": "配置已保存。点击「重启」让新配置生效。",
	}})
}

// PluginLogs 读取插件日志。
func PluginLogs(c *gin.Context) {
	s := pluginSvc()
	if !s.requireHost(c) {
		return
	}
	name := c.Query("name")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	entries := s.host.Logs(name, limit)
	// 倒序返回：最新在前，管理端直接渲染不用再 reverse
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": entries})
}

// ---- 钩子清单 ----

// ListPluginHooks 返回全部钩子及其描述。
//
// 数据源是真实注册表（plugin.List），因此 count 是真的
// —— 修复了此前前端拿不到该接口、只能显示 Math.random() 随机数的问题。
func ListPluginHooks(c *gin.Context) {
	inProc, outProc := plugin.ListSplit()
	metas := plugin.Hooks
	list := make([]gin.H, 0, len(metas))
	for _, m := range metas {
		h := plugin.HookName(m.Name)
		list = append(list, gin.H{
			"name":       m.Name,
			"count":      inProc[h] + outProc[h],
			"inProcess":  inProc[h],
			"outProcess": outProc[h],
			"doc":        m.Doc,
			"mode":       plugin.HookModeOf(h).String(),
			"blockable":  m.Blockable,
			"wired":      m.Wired,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": list,
		"meta": gin.H{
			"protocolVersion": plugin.ProtocolVersion,
			"outOfProcess":    outProcTotal(outProc),
		},
	})
}

func outProcTotal(m map[plugin.HookName]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// ---- 插件商店 ----

// StorePluginCatalog 拉取远端插件清单。
//
// 与旧实现的差异：
//   - 校验返回结构，坏数据不再原样透传给前端
//   - 出错时明确回 message（此前一律返回空数组，管理员无从判断是"商店为空"还是"拉取失败"）
//   - 对内网/本机地址给出提示（管理员权限下可控，但应当知情）
func StorePluginCatalog(c *gin.Context) {
	raw, _ := loadSetting("plugin.store.url")
	storeURL := strings.TrimSpace(raw)
	if storeURL == "" {
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "data": []any{},
			"meta": gin.H{"configured": false,
				"hint": "尚未配置插件商店地址。可在下方填入，形如 https://example.com/nebula/"},
		})
		return
	}
	if !strings.HasSuffix(storeURL, "/") {
		storeURL += "/"
	}
	catalogURL := storeURL + "com.json"

	if err := install.ValidateCatalogURL(catalogURL); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 1, "message": "插件商店地址无效: " + err.Error(), "data": []any{},
		})
		return
	}

	ins := install.New(pluginsRootDir())
	body, err := ins.FetchCatalog(catalogURL)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 1, "message": "拉取插件商店失败: " + err.Error(), "data": []any{},
			"meta": gin.H{"configured": true, "url": catalogURL},
		})
		return
	}

	entries, warns := parseCatalog(body)
	meta := gin.H{"configured": true, "url": catalogURL, "count": len(entries)}
	if len(warns) > 0 {
		// 条目被跳过时必须告诉管理员，不能静默丢
		meta["warnings"] = warns
	}
	// 标出哪些已安装
	var installed []string
	db.Get().Model(&models.Plugin{}).Pluck("name", &installed)
	set := map[string]bool{}
	for _, n := range installed {
		set[n] = true
	}
	for _, e := range entries {
		e["installed"] = set[e["name"].(string)]
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": entries, "meta": meta})
}

// parseCatalog 解析并校验商店清单。
//
// 期望格式：{"plugins":[{"name","title","version","description","url","author","license","tags"}]}
// 逐条校验，坏条目跳过并记录原因 —— 一个坏条目不该让整个商店打不开。
func parseCatalog(body []byte) ([]gin.H, []string) {
	var doc struct {
		Plugins []gin.H `json:"plugins"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		// 也接受顶层就是数组的格式
		var arr []gin.H
		if err2 := json.Unmarshal(body, &arr); err2 != nil {
			return nil, []string{"清单格式无法解析（既不是 {\"plugins\":[...]} 也不是数组）"}
		}
		doc.Plugins = arr
	}

	var warns []string
	out := make([]gin.H, 0, len(doc.Plugins))
	for i, e := range doc.Plugins {
		name, _ := e["name"].(string)
		if name == "" {
			warns = append(warns, indexMsg(i, "缺少 name"))
			continue
		}
		if err := install.ValidatePluginName(name); err != nil {
			warns = append(warns, indexMsg(i, "name 无效: "+err.Error()))
			continue
		}
		url, _ := e["url"].(string)
		if url == "" {
			warns = append(warns, indexMsg(i, name+" 缺少下载地址 url"))
			continue
		}
		if err := install.ValidateCatalogURL(url); err != nil {
			warns = append(warns, indexMsg(i, name+" 的 url 无效: "+err.Error()))
			continue
		}
		// 商店声称的版本/作者只作展示，不能作为信任依据
		e["name"] = name
		e["source"] = models.PluginSourceStore
		if _, ok := e["tags"]; !ok {
			e["tags"] = []string{}
		}
		out = append(out, e)
	}
	return out, warns
}

func indexMsg(i int, msg string) string {
	return "第 " + strconv.Itoa(i+1) + " 条：" + msg
}

// ---- 工具 ----

func recordPluginError(name, msg string) {
	db.Get().Model(&models.Plugin{}).Where("name = ?", name).
		Update("last_error", truncate(msg, 2000))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func auditPlugin(c *gin.Context, action, target, detail string) {
	u := middleware.CurrentUser(c)
	if u == nil {
		return
	}
	db.Get().Create(&models.AuditLog{
		UserID: u.ID, UserName: u.UserName,
		Action: action, Target: target,
		IP: c.ClientIP(), UA: c.Request.UserAgent(),
		Detail: detail,
	})
}

// loadSetting 读单个设置项。
//
// 与 GetSettings 的区别：后者是给前端铺出全量 map，这里是内部取单值。
// 没有复用 GetSettings 是因为它会 JSON 序列化整个 settings 表，
// 为一个键付出这个代价不划算。
func loadSetting(key string) (string, bool) {
	var s models.Setting
	if err := db.Get().Where("`key` = ?", key).First(&s).Error; err != nil {
		return "", false
	}
	return s.Value, true
}

// saveSetting upsert 单个设置项。语义与 SaveSettings 里的分支一致，
// 抽出来是因为插件协议同意状态需要原子地"读-改-写"。
func saveSetting(key, value string) {
	var s models.Setting
	if err := db.Get().Where("`key` = ?", key).First(&s).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		db.Get().Create(&models.Setting{Key: key, Value: value})
		return
	}
	db.Get().Model(&s).Update("value", value)
}
