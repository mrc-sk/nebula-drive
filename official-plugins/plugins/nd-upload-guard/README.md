# 上传安全守卫

拦截可执行文件、脚本与路径穿越文件名，并可限制单文件大小。

| 项目 | 值 |
| --- | --- |
| 插件名 | `nd-upload-guard` |
| 版本 | 1.0.0 |
| 作者 | NebulaDrive Official |
| 协议 | AGPL-3.0-only |
| 订阅钩子 | `onFileUploadBefore` |

## 功能

在 `onFileUploadBefore` 决策钩子上做三件事：按扩展名黑名单拒绝可执行文件/脚本、
拒绝含 `..` 或路径分隔符的文件名、按 `maxBytes` 限制单文件上限。默认只启用
前两项，大小限制需显式配置才生效。

## 配置

安装后在后台「插件 → 配置」里填写 JSON（对应 `plugins.config`）。**不填任何配置时插件保持宽松/关闭状态，不会一装上就改变站点行为。

```json
{"blockedExtensions": [".exe", ".sh", ".php"], "maxBytes": 524288000}
```
`blockedExtensions` 不填时使用内置黑名单（.exe/.bat/.cmd/.scr/.vbs/.ps1/.sh/.php/.jsp/.asp/.dll/.so/.msi/.com/.pif）；
`maxBytes` 为 0 或省略表示不限制。

## 启用前须知

本插件以子进程方式运行，拥有宿主同等的文件与网络权限。请先在测试环境验证，确认策略符合预期后再在生产环境启用。

官方插件使用 Ed25519 签名，站点信任库中已内置官方公钥；安装时会在后台显示「已由受信任的发布者签名」。
