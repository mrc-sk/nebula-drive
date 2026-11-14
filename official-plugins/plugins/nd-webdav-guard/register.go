package p_nd_webdav_guard

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-webdav-guard",
    Title:           "WebDAV 写保护",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "按需把 WebDAV 切成只读，或保护指定目录不被 WebDAV 写入/删除。",
    ProtocolVersion: 2,
    Hooks:           []string{"onWebDAVPut", "onWebDAVDelete"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
