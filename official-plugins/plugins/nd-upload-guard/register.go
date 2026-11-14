package p_nd_upload_guard

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-upload-guard",
    Title:           "上传安全守卫",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "拦截可执行文件、脚本与路径穿越文件名，并可限制单文件大小。",
    ProtocolVersion: 2,
    Hooks:           []string{"onFileUploadBefore"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
