package p_nd_share_policy

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-share-policy",
    Title:           "分享链接策略",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "强制分享链接设置密码与有效期，禁止无保护的永久公开链接。",
    ProtocolVersion: 2,
    Hooks:           []string{"onShareCreateBefore"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
