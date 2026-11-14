# 关键操作审计

把登录、上传、删除、分享等关键事件以 JSONL 追加写入独立审计日志。

| 项目 | 值 |
| --- | --- |
| 插件名 | `nd-audit-trail` |
| 版本 | 1.0.0 |
| 作者 | NebulaDrive Official |
| 协议 | AGPL-3.0-only |
| 订阅钩子 | `onFileUploadAfter`, `onFileDelete`, `onAuthLoginSuccess`, `onShareCreateAfter`, `onUserDelete` |

## 功能

通知型插件：订阅 5 个关键事件，脱敏摘要后写入插件专属数据目录
`audit.log`（每行一条 JSON）。日志与宿主数据目录隔离，便于单独备份与外送。

## 配置

安装后在后台「插件 → 配置」里填写 JSON（对应 `plugins.config`）。**不填任何配置时插件保持宽松/关闭状态，不会一装上就改变站点行为。

无需配置。日志路径为插件数据目录下的 `audit.log`；
可用 `deskctl` 风格的本地命令或直接 `tail -f` 查看。

## 启用前须知

本插件以子进程方式运行，拥有宿主同等的文件与网络权限。请先在测试环境验证，确认策略符合预期后再在生产环境启用。

官方插件使用 Ed25519 签名，站点信任库中已内置官方公钥；安装时会在后台显示「已由受信任的发布者签名」。
