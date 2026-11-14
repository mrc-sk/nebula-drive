package p_nd_audit_trail

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-audit-trail",
    Title:           "关键操作审计",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "把登录、上传、删除、分享等关键事件以 JSONL 追加写入独立审计日志。",
    ProtocolVersion: 2,
    Hooks:           []string{"onFileUploadAfter", "onFileDelete", "onAuthLoginSuccess", "onShareCreateAfter", "onUserDelete"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
