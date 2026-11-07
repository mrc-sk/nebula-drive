# 07 · 官方示例

> 全部位于 `examples/plugins/`，每个目录含 `plugin.json` + `main.go` + `go.mod`（+ 多数有 `README.md` / `main_test.go`）。版本均为 `1.0.0`，可直接 `go build` 后本地安装。

| 示例 | 钩子 | 做什么 |
|---|---|---|
| [referer-guard](../../examples/plugins/referer-guard/) | `onAntiLeech` | 下载前校验 Referer，不在白名单内返回 403。支持通配符与正则模式，记录下载审计。**最完整的「拦截型」范例** |
| [download-limiter](../../examples/plugins/download-limiter/) | `onAntiLeech`, `onCLI` | 按客户端 IP 做滑动窗口下载限流（1 分钟/1 小时双阈值），超出 403。支持登录用户豁免、分享预览豁免 |
| [hmac-auth](../../examples/plugins/hmac-auth/) | `onApiAuth` | 为 `/api/v1/*` 提供 PAT/OAuth2 之外的 HMAC-SHA256 签名鉴权；校验通过认定调用者身份，签名不符或时间戳超窗一律拒绝 |
| [smtp-email](../../examples/plugins/smtp-email/) | `onEmail` | 把站内通知通过 SMTP 投递到用户邮箱；支持 starttls/implicit/none 三种 TLS，中文主题正文正确编码 |
| [notify-webhook](../../examples/plugins/notify-webhook/) | `onEmail`, `onCLI` | 把站内通知转发到 Slack、飞书、钉钉、Discord 或任意通用 webhook；URL 日志脱敏，超时控制，5xx 自动重试一次 |
| [backup-mirror](../../examples/plugins/backup-mirror/) | `onBackup`, `onRestore`, `onCLI` | 把宿主生成的备份包流式镜像到多个异地目录，按保留数轮转，并把恢复操作追加进本地审计日志 |
| [collab-redirect](../../examples/plugins/collab-redirect/) | `onCollabOpen`, `onCLI` | 按扩展名或 MIME 前缀判断文件是否交外部编辑器，命中时把协作打开 302 跳转到模板化编辑器地址（只做跳转，不代理文件、不做鉴权） |

## 推荐阅读顺序

1. **想写拦截型插件** → `referer-guard`（看 `init`/`fire`/`shutdown` 循环 + `blocked` 用法 + ctx 数字字段的 `float64` 类型断言坑）。
2. **想写异步事件型插件** → `smtp-email` / `notify-webhook`（看 `onEmail` 的 ctx 用法、网络失败重试）。
3. **想写扩展鉴权** → `hmac-auth`（看 `onApiAuth` 的 `principalUserId` 放行 + 时间窗防重放 + 常量时间比较）。
4. **想写 CLI 子命令** → 任意带 `onCLI` 的示例（看 `cliOutput`/`cliExitCode` 回写）。
