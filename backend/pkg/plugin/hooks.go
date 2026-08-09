package plugin

import "sync"

type HookName string

const (
	HookEmail      HookName = "onEmail"
	HookBackup     HookName = "onBackup"
	HookRestore    HookName = "onRestore"
	HookApiAuth    HookName = "onApiAuth"
	HookDBMigrate  HookName = "onDBMigrate"
	HookRateLimit  HookName = "onRateLimit"
	HookAntiLeech  HookName = "onAntiLeech"
	HookCLI        HookName = "onCLI"
	HookCollabOpen HookName = "onCollabOpen"
	HookCollabSave HookName = "onCollabSave"
)

type HandlerFunc func(ctx map[string]any) (map[string]any, error)

var registry = map[HookName][]HandlerFunc{}
var mu sync.RWMutex

func Register(h HookName, fn HandlerFunc) {
	mu.Lock()
	defer mu.Unlock()
	registry[h] = append(registry[h], fn)
}

func Fire(h HookName, ctx map[string]any) []error {
	mu.RLock()
	defer mu.RUnlock()
	var errs []error
	for _, fn := range registry[h] {
		if _, err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func List() map[HookName]int {
	mu.RLock()
	defer mu.RUnlock()
	out := map[HookName]int{}
	for k, v := range registry {
		out[k] = len(v)
	}
	return out
}

// HookDocs 每个钩子的文档描述（硬编码供前端展示）
var HookDocs = map[HookName]string{
	HookEmail:      "邮件发送钩子：可拦截注册激活、找回密码等邮件，替换为自建 SMTP 或第三方服务",
	HookBackup:     "数据备份钩子：在管理员触发备份前/后执行，可对接异地备份对象存储",
	HookRestore:    "数据恢复钩子：恢复备份前调用，可校验备份来源与完整性",
	HookApiAuth:    "API 鉴权钩子：登录或 Token 校验时触发，可接入 LDAP、SSO、OAuth 等身份源",
	HookDBMigrate:  "数据库迁移钩子：AutoMigrate 完成后触发，可自定义字段、索引或升级脚本",
	HookRateLimit:  "速率限制钩子：登录、上传、下载、分享创建/删除等高频操作前调用，可实现限流",
	HookAntiLeech:  "防盗链钩子：文件下载前触发，可校验 Referer、IP、签名等，返回含 blocked 的 error 即 403",
	HookCLI:        "命令行钩子：启动时或自定义子命令调用，可注入运维级扩展点",
	HookCollabOpen: "协作编辑打开钩子：打开文件协作编辑时触发，可在 ctx 中设置 url 字段以 302 跳转到 OnlyOffice 等外部编辑器",
	HookCollabSave: "协作编辑保存钩子：保存协作内容时触发，可对接外部编辑器回写或审计",
}
