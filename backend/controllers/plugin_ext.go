package controllers

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/internal/hooks"
	"github.com/nebula-drive/nebula/internal/pluginhttp"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/pluginset"
)

func middlewareUser(c *gin.Context) *models.User { return middleware.CurrentUser(c) }

func pluginDirOf(name string) string {
	return filepath.Join(conf.DataDir(), "plugins", name)
}

func saveGlobalEnabled(v bool) {
	s := "0"
	if v {
		s = "1"
	}
	saveSetting("plugin.global_enabled", s)
}

// 本文件承载 v2 新增的「注册型 / UI / 系统级」钩子接线，
// 这些钩子没有对应的既有业务函数，需要新的入口才能真实触发。
// 每个 handler 都有真实调用点，不是占位。

// ---- 健康检查 ----

// HealthCheck 带插件健康项的 /api/health。
func HealthCheck(c *gin.Context) {
	ctx := map[string]any{
		"healthy":   true,
		"checks":    []any{},
		"installed": conf.IsInstalled(),
	}
	_, _, out := hooks.Gate(plugin.HookHealthCheck, c, ctx)
	healthy := hooks.Bool(out, "healthy", true)
	if !healthy {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok": false, "installed": conf.IsInstalled(), "checks": out["checks"],
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "installed": conf.IsInstalled(), "checks": out["checks"],
	})
}

// ---- UI 扩展点 ----

// PluginUI 聚合所有插件声明的侧边栏 / 主题 / 设置面板。
//
// 前端 MainLayout 与 AdminLayout 拉这个接口动态渲染，
// 从而实现「插件能往后台/主界面侧边栏加页面点、能换主题」。
func PluginUI(c *gin.Context) {
	u := middlewareUser(c)
	role := "any"
	if u != nil && u.IsAdmin {
		role = "admin"
	}

	// 插件可在此动态注入 sidebar / theme / settings
	_, _, ctx := hooks.Gate(plugin.HookUIPageRegister, c, map[string]any{
		"role": role, "protocolVersion": plugin.ProtocolVersion,
	})
	// 主题应用钩子：插件可覆盖 CSS 变量
	_, _, themeCtx := hooks.Gate(plugin.HookUIThemeApply, c, map[string]any{
		"role": role, "variables": map[string]any{},
	})
	variables := map[string]any{}
	if v, ok := themeCtx["variables"].(map[string]any); ok {
		variables = v
	}

	reg := plugin.GlobalRegistry()
	data := gin.H{
		"protocolVersion": plugin.ProtocolVersion,
		"globalEnabled":   pluginhttp.Global().GlobalEnabled(),
		"sidebar": gin.H{
			"main":  reg.Sidebar("main", role),
			"admin": reg.Sidebar("admin", role),
		},
		"public":    reg.Sidebar("public", "any"),
		"themes":    reg.Themes(),
		"settings":  reg.Settings(),
		"providers": reg.AllProviders(),
		"variables": variables,
		"dynamic":   ctx, // 插件动态注入的原始内容，前端可自行消费
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": data})
}

// PluginUISettingsSave 插件设置面板保存。
func PluginUISettingsSave(c *gin.Context) {
	name := c.Param("plugin")
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	allowed, reason, ctx := hooks.Gate(plugin.HookUISettingsSave, c, map[string]any{
		"plugin": name, "values": body,
	})
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": reason})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": ctx["values"]})
}

// ---- 内容举报 ----

// CreateReport POST /api/reports：宿主只落事件，判定交给插件。
func CreateReport(c *gin.Context) {
	var req struct {
		ShareID uint   `json:"shareId"`
		FileID  uint   `json:"fileId"`
		Reason  string `json:"reason"`
		Contact string `json:"contact"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	hooks.Emit(plugin.HookContentReport, map[string]any{
		"shareId": req.ShareID, "fileId": req.FileID,
		"reason": req.Reason, "contact": req.Contact, "ip": c.ClientIP(),
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "已收到举报"})
}

// ---- 备份 / 恢复（v1 占位 → v2 真接线） ----

// Backup 触发注册型 backup 插件导出数据。
func Backup(c *gin.Context) {
	var req struct {
		Target       string `json:"target"`
		IncludeFiles bool   `json:"includeFiles"`
	}
	_ = c.ShouldBindJSON(&req)
	_, _, ctx := hooks.Gate(plugin.HookBackup, c, map[string]any{
		"target": req.Target, "includeFiles": req.IncludeFiles, "result": nil,
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": ctx["result"]})
}

// PluginRestore 触发注册型 restore 插件恢复数据（危险操作，需二次确认）。
func PluginRestore(c *gin.Context) {
	var req struct {
		Source string `json:"source"`
		DryRun bool   `json:"dryRun"`
	}
	_ = c.ShouldBindJSON(&req)
	allowed, reason, ctx := hooks.Gate(plugin.HookRestore, c, map[string]any{
		"source": req.Source, "dryRun": req.DryRun, "result": nil,
	})
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": reason})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": ctx["result"]})
}

// ---- 支付回调（注册型 payment） ----

// PaymentCallback 插件负责验签并给出开通信息，宿主不内置任何支付 SDK。
func PaymentCallback(c *gin.Context) {
	provider := c.Param("provider")
	var body map[string]any
	_ = c.ShouldBindJSON(&body)
	ctx := map[string]any{
		"provider": provider, "orderId": body["orderId"],
		"amount": body["amount"], "status": "", "userId": 0, "planId": 0,
		"raw": body,
	}
	allowed, reason, out := hooks.Gate(plugin.HookPaymentCallback, c, ctx)
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": reason})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"status": out["status"], "userId": out["userId"], "planId": out["planId"],
			// 开通动作由插件通过管理 API 完成；宿主不替插件写订阅，避免假实现
			"note": "订阅开通由插件自行调用 /api/admin/* 完成",
		},
	})
}

// ---- 全局插件开关（默认关闭，双层保险） ----

// PluginGlobalStatus 返回全局开关状态。
func PluginGlobalStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"enabled":         pluginhttp.Global().GlobalEnabled(),
			"protocolVersion": plugin.ProtocolVersion,
			"totalHooks":      len(plugin.Hooks),
			"wiredHooks":      wiredHookCount(),
		},
	})
}

// EnablePluginGlobal 开启全局插件系统。
//
// 按站长选择：代码里默认关闭，站长自己开，开了概不负责。
// 因此这里强制要求显式确认字段 acknowledge=true，前端必须弹免责弹窗。
func EnablePluginGlobal(c *gin.Context) {
	var req struct {
		Enabled     bool `json:"enabled"`
		Acknowledge bool `json:"acknowledge"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Enabled && !req.Acknowledge {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"message": "开启插件系统必须确认风险：插件以独立进程运行、宿主对 /ext/* 页面完全透传鉴权，" +
				"等同于把服务器执行权限交给插件作者。请显式传 acknowledge=true。",
		})
		return
	}
	saveGlobalEnabled(req.Enabled)
	if req.Enabled {
		pluginhttp.Global().SetGlobalEnabled(true)
		pluginset.EnableGlobal() // 进程已启动时补初始化宿主并拉起已启用插件
	} else {
		pluginset.StopPlugins()
		pluginhttp.Global().SetGlobalEnabled(false)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"enabled": req.Enabled}})
}

// PluginContractDiagnose 诊断某插件的三文件契约与解释器，供后台显示装载失败原因。
func PluginContractDiagnose(c *gin.Context) {
	name := c.Param("name")
	dir := pluginDirOf(name)
	missing := plugin.MissingFiles(dir)
	contract, err := plugin.LoadContract(dir, true)
	resp := gin.H{
		"name":         name,
		"dir":          dir,
		"missingFiles": missing,
		"required":     plugin.RequiredV2Files,
	}
	if err != nil {
		resp["ok"] = false
		resp["error"] = err.Error()
	} else {
		resp["ok"] = true
		resp["protocolVersion"] = contract.Manifest.ProtocolVersion
		resp["v1"] = contract.V1
		resp["hooks"] = contract.Manifest.Hooks
		resp["runtimeBins"] = contract.RuntimeBins
		if contract.Begg != nil {
			resp["runtime"] = contract.Begg.Runtime
			resp["pages"] = contract.Begg.Pages
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": resp})
}

// ---- 定时心跳与过期事件 ----

// StartPluginScheduler 启动插件定时心跳（每 5 分钟），并派发过期事件。
func StartPluginScheduler() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		var tick int64
		for range ticker.C {
			tick++
			hooks.Emit(plugin.HookScheduledTick, map[string]any{
				"tick": tick, "at": time.Now().Unix(),
			})
			emitExpirations()
		}
	}()
}

func emitExpirations() {
	if d := db.Get(); d != nil {
		var shares []models.Share
		if err := d.Where("expire_at IS NOT NULL AND expire_at < ?", time.Now()).
			Limit(200).Find(&shares).Error; err == nil {
			for _, s := range shares {
				hooks.Emit(plugin.HookShareExpire, map[string]any{
					"shareId": s.ID, "fileId": s.FileID, "expireAt": s.ExpireAt,
				})
			}
		}
		var subs []models.Subscription
		if err := d.Where("status = ? AND end_time < ?", "active", time.Now()).
			Limit(200).Find(&subs).Error; err == nil {
			for _, s := range subs {
				hooks.Emit(plugin.HookPlanExpire, map[string]any{
					"userId": s.UserID, "planId": s.PlanID, "expireAt": s.EndTime,
				})
			}
		}
	}
}

func wiredHookCount() int {
	n := 0
	for _, m := range plugin.Hooks {
		if m.Wired {
			n++
		}
	}
	return n
}
