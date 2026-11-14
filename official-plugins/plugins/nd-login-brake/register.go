package p_nd_login_brake

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-login-brake",
    Title:           "登录爆破抑制",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "同一 IP / 账号短时间连续登录失败后临时锁定，抑制暴力破解。",
    ProtocolVersion: 2,
    Hooks:           []string{"onAuthBeforeLogin", "onAuthLoginFailed", "onAuthLoginSuccess"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
