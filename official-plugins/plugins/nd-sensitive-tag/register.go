package p_nd_sensitive_tag

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-sensitive-tag",
    Title:           "敏感文件标记",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "按关键词扫描文件名，命中后自动打上「需复核」标签，不拦截上传。",
    ProtocolVersion: 2,
    Hooks:           []string{"onContentScan", "onFileUploadAfter"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
