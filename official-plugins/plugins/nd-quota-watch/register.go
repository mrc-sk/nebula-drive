package p_nd_quota_watch

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-quota-watch",
    Title:           "配额用量预警",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "当用户存储用量接近配额上限时写入预警信息，便于提前扩容或提醒。",
    ProtocolVersion: 2,
    Hooks:           []string{"onFileUploadAfter", "onUserQuotaChange"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
