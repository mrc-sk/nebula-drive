package p_nd_invite_code

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-invite-code",
    Title:           "注册邀请码",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "为开放注册补上注册码校验：未填或填错注册码一律拒绝，码表由管理员维护。",
    ProtocolVersion: 2,
    Hooks:           []string{"onAuthRegisterBefore"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
