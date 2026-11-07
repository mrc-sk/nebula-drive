# 04 · 钩子清单

> 源码：`backend/pkg/plugin/hooks.go`（`Hooks` 元信息表、`HookModeOf`、`MetaOf`）
> 钩子是宿主「在某某时机调用插件」的固定点。插件只在 `plugin.json` 的 `hooks` 里**声明**的钩子才会被派发。

## 总览（以当前代码为准）

| 钩子 | 模式 | 可拦截 | 已接线 | 用途 |
|---|---|---|---|---|
| `onAntiLeech` | sync | ✅ | ✅ | 下载/预览前触发，**唯一下载拦截点**，做防盗链必须用它 |
| `onCollabOpen` | sync | ❌ | ✅ | 协作打开时触发；写 `url` 则 302 跳外部编辑器 |
| `onCollabSave` | async | ❌ | ✅ | 协作内容保存时触发，纯通知 |
| `onEmail` | async | ❌ | ✅ | 创建站内通知时触发，纯通知。**系统本身不含发信实现，此钩子是邮件外发唯一入口** |
| `onDBMigrate` | async | ❌ | ✅ | AutoMigrate + 版本迁移全部完成后触发，**每次启动都触发**（不只升级） |
| `onRateLimit` | async | ❌ | ✅ | 高频操作前触发（登录/上传/下载/分享增删/全局限流中间件）；拦截走 `RateLimitGate` |
| `onBackup` | async | ❌ | ✅ | 管理端创建备份**之后**触发，纯通知 |
| `onRestore` | async | ❌ | ✅ | 管理端提交恢复**之前**触发，纯通知（暂存制，恢复发生在下次启动） |
| `onApiAuth` | sync | ✅ | ✅ | 内置鉴权（PAT/OAuth2）**失败后**触发，扩展鉴权方案 |
| `onCLI` | sync | ✅ | ✅ | `nebula plugin <cmd> [args...]` 时触发，同步拿输出 |

> ⚠️ 旧文档（`插件系统完全指南.md`）曾把 `onBackup`/`onRestore`/`onApiAuth`/`onCLI` 标为「预留未接入」——**那已过时**。当前代码 `Hooks` 表里这 4 个均为 `Wired: true`，且已有对应调用点（`backend/controllers/backup.go`、`middleware/api_auth.go`、`runPluginCLI`）和示例插件。**以本表与代码为准。**

## 拦截的统一定义

全项目**唯一**的拦截判定：`strings.Contains(err.Error(), "blocked")`（区分大小写）。`middleware/rate_limit.go`、`controllers/file.go` 与进程外派发门都走 `plugin.IsBlockedError`。想拦截就必须让返回的 error 文本含小写 `blocked`。

---

## `onAntiLeech` · 防盗链（sync / 可拦截）✅已接线

下载/预览前触发，覆盖两条路径：

1. **登录下载** `GET /api/files/:id/download`
2. **公开分享** `GET /api/shares/:id/download` 与 `/preview`

- 登录下载 `ctx`：`{userId, userName, ip, ua, fileId, fileName, referer}`
- 公开分享 `ctx`：`{userId(恒为0), userName(恒为空), ip, ua, fileId, fileName, referer, shareId, shareKind, shareOwnerId}`
- `shareKind` 取值 `download` 或 `preview`——**两者都是完整文件内容的出口**，宿主一视同仁过闸；只想保护下载、放行本站分享页内联预览的插件，请自行按 `shareKind` 放行。
- 返回 error 且文本含 `blocked` → 403。**做防盗链必须用它。**
- ⚠️ 分享访问者是匿名的：`userId` 恒为 0，插件不能假设它有值，也不会像登录下载那样天然有「所有者」可比。

## `onRateLimit` 的特殊处理

它在 **7 处**被调用，但只有限流中间件那一处需要读返回值做拦截决策。整个钩子设为 `ModeSync` 会让另外 6 处（登录/上传/下载/分享增删）每次白等 IPC 往返；保持 `ModeAsync` 又会让中间件拦截失效。

**解决**：钩子保持异步（不拖累热路径），限流中间件改调 `plugin.RateLimitGate(ctx)`——它走 `Dispatcher.DispatchSync` **强制同步等待**。代价是最多一个 `FireTimeout`（默认 2s），且**只在有插件声明了 `onRateLimit` 时才有实际等待**；没有插件时立即返回空。

## `onCollabOpen` · 协作打开（sync / 不可拦截）✅已接线

`ctx = {userId, userName, fileId, fileName, mimeType, ip}`。在 `ctx` 写入 `url` 字段则 302 跳转到该地址（OnlyOffice 等外部编辑器）。注意 `ctx` 是引用类型，异步派发时业务读 `ctx` 可能早于插件写入 → 竞态，故强制 sync。

## `onCollabSave` · 协作保存（async / 不可拦截）✅已接线

`ctx = {userId, userName, fileId, fileName, content}`；`content` 是完整文件明文，大文件会显著增加内存占用。仅事件通知。

## `onEmail` · 邮件外发（async / 不可拦截）✅已接线

`ctx = {to, subject, body}`，用户邮箱为空则不触发。系统本身不含任何发信实现，**此钩子是邮件外发的唯一入口**（示例：`smtp-email`、`notify-webhook`）。

## `onDBMigrate` · 数据库迁移（async / 不可拦截）✅已接线

`ctx = {fromVersion, toVersion}`，两者相等表示本次无迁移。**每次启动都会触发**，不是「首次升级才触发」。插件建表必须幂等（`CREATE TABLE IF NOT EXISTS`），否则重复执行会报错。

## `onBackup` · 备份后（async / 不可拦截）✅已接线

管理端创建备份**之后**触发。`ctx = {kind, filename, size, sha256, dbPath, fileCount, createdAt, operator}`。宿主负责生成备份包（SQLite 库 + 文件清单 manifest.json），插件可把它复制到 S3/异地或落审计。**异步派发拿不到返回值**，别指望用它否决备份。

## `onRestore` · 恢复前（async / 不可拦截）✅已接线

管理端提交恢复**之前**触发。`ctx = {filename, size, stagedPath, operator, currentDb}`。用途是校验/审计/通知。⚠️ 恢复是**暂存制**：真正的库替换发生在下次启动（热替换正在使用的 SQLite 库不安全），所以插件在此拦不住这次恢复——需要否决请在管理端权限层解决。

## `onApiAuth` · 开放 API 鉴权（sync / 可拦截）✅已接线

`/api/v1/*` 的内置鉴权（PAT/OAuth2）**失败之后**触发，用于扩展鉴权方案（HMAC 签名、IP 白名单、第三方令牌等）。

- `ctx = {method, path, query, ip, ua, headers, hasAuthorization, contentType, contentLength, bodyHash, bodyTruncated}`
- `headers` **不含 `Authorization`**（内置凭据留在宿主，插件请用自己的头）。
- **放行**：在 `ctx` 写入 `principalUserId`（>0），宿主据此认定调用者身份。
- **拒绝**：返回含 `blocked` 的 error，宿主返回 403。
- 两者都没有则视为未通过（fail closed）。
- ⚠️ 内置鉴权成功时**不会**触发本钩子：既保证热路径零 IPC 开销，也意味着插件无法否决一个已有效的令牌——内置凭据始终优先。示例：`hmac-auth`。

## `onCLI` · 命令行子命令（sync / 可拦截）✅已接线

执行 `nebula plugin <cmd> [args...]` 时触发，**同步**等待以便拿到输出。`ctx = {command, args, argsRaw}`；插件可写入 `cliOutput`（字符串，会被打印）与 `cliExitCode`（数字）。返回含 `blocked` 的 error 会让 CLI 以非 0 退出码结束。多个插件同时响应时 `cliOutput` 是**后写覆盖**，请自行加前缀区分。
