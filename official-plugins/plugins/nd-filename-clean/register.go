package p_nd_filename_clean

// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。
// 由 NebulaDrive 官方插件发布工具生成。
var Spec = PluginSpec{
    Name:            "nd-filename-clean",
    Title:           "文件名规范化",
    Version:         "1.0.0",
    Author:          "NebulaDrive Official",
    Description:     "自动清洗上传与重命名时的非法字符、路径片段与过长文件名。",
    ProtocolVersion: 2,
    Hooks:           []string{"onFileUploadBefore", "onFileRename"},
    Provides:        []string{},
    Install:         []string{"plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"},
    Runtime:         "python",
}
