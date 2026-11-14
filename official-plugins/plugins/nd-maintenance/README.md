# 维护模式与站点公告

放置标志文件即进入维护模式（管理员仍可进），并可向页面注入公告。

| 项目 | 值 |
| --- | --- |
| 插件名 | `nd-maintenance` |
| 版本 | 1.0.0 |
| 作者 | NebulaDrive Official |
| 协议 | AGPL-3.0-only |
| 订阅钩子 | `onRequestBefore`, `onUIPageOpen` |

## 功能

默认完全不生效。管理员在插件数据目录创建 `maintenance.flag`（文件内容即公告文案）后：
`onRequestBefore` 拒绝普通用户请求但放行后台 `/api/admin` 路径；
`onUIPageOpen` 通过 `ctxPatch.siteNotice` 注入公告。删除文件即恢复。

## 配置

安装后在后台「插件 → 配置」里填写 JSON（对应 `plugins.config`）。**不填任何配置时插件保持宽松/关闭状态，不会一装上就改变站点行为。

```json
{"maintenanceEnabled": true}
```
标志文件路径：插件数据目录 `maintenance.flag`。
**务必保留管理员放行规则**，否则会把自己也挡在外面。

## 启用前须知

本插件以子进程方式运行，拥有宿主同等的文件与网络权限。请先在测试环境验证，确认策略符合预期后再在生产环境启用。

官方插件使用 Ed25519 签名，站点信任库中已内置官方公钥；安装时会在后台显示「已由受信任的发布者签名」。
