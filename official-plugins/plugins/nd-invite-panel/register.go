package p_nd_invite_panel

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-invite-panel",
    Title:           "邀请码管理面板",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "后台侧边栏入口：随机生成注册邀请码、设置使用次数与有效期、按码类型（套餐）分配配额，可停用/删除/复制。",
    ProtocolVersion: 2,
    Hooks:           []string{},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "server.py", "index.html", "README.md", "store-meta.json"},
    Runtime:         "python",
    Home:            "index.html",
}
