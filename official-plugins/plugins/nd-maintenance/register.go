package p_nd_maintenance

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-maintenance",
    Title:           "维护模式与站点公告",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "放置标志文件即进入维护模式（管理员仍可进），并可向页面注入公告。",
    ProtocolVersion: 2,
    Hooks:           []string{"onRequestBefore", "onUIPageOpen"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
