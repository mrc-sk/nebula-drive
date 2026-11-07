# smtp-email —— 通过 SMTP 投递站内通知

> 状态：**已实现真实发信**（`net/smtp`，三种 TLS 模式，中文主题与正文编码齐全）。

## 为什么需要它

宿主本身**不含任何发信实现** —— 全仓 `grep -r smtp` 零命中。
`onEmail`（在 `controllers/ws.go` 创建站内通知时触发）是邮件外发的
**唯一入口**，所以没有这个插件，站内通知永远到不了邮箱。

## 能力

| 能力 | 说明 |
|---|---|
| `init` / `fire` / `ping` / `shutdown` 协议 | 进程外插件，宿主热加载 |
| 三种 TLS 模式 | `starttls`(587) / `implicit`(465) / `none`(25) |
| SMTP AUTH | 配了 `username` 才认证；服务器没声明 `AUTH` 时**明确报错**，不静默匿名发送 |
| 中文主题 | RFC 2047 编码，长主题折行后解码仍与原文一致（有单测） |
| 中文正文 | `charset=UTF-8` + base64；ASCII 正文走 7bit |
| 头注入防护 | `Subject` / `To` 里的 CR/LF 一律剥离，防止伪造 `Bcc` |
| 密码脱敏 | 日志里只出现 `<set:N chars>`，`redacted()` 与 `maskSecret()` |

**没有任何"跳过证书校验"的开关。** `tlsConfigForHost` 是个可注入的变量，
但它只在测试里被替换；生产默认固定 `MinVersion: TLS1.2` 且按系统根证书校验。

## 失败时的行为（刻意设计成"不假装成功"）

投递失败会记一条 warn（**带上服务端原始响应**）并把 `smtpEmail=failed`
写回 ctx。配上插件却收不到邮件时，日志里能看到具体原因，而不是安静地
什么都不做 —— 后者会让管理员对着 SMTP 配置白排查几小时。

几种状态：

| `ctxPatch.smtpEmail` | 含义 |
|---|---|
| `sent` | 投递成功 |
| `failed` | 投递失败（日志里有原因） |
| `skipped-no-recipient` | 收件人为空，未尝试投递 |

`fire` **永远不返回 error**：`onEmail` 是异步、不可拦截的钩子，
邮件发不出去不该让站内通知创建失败；返回含 `blocked` 的错误更会误伤
（宿主会当成拦截语义）。

## 配置

```json
{
  "host": "smtp.example.com",
  "port": 587,
  "username": "noreply@example.com",
  "password": "********",
  "from": "noreply@example.com",
  "fromName": "NebulaDrive",
  "tls": "starttls",
  "timeoutSeconds": 10
}
```

- `port`：587 = STARTTLS，465 = implicit TLS，25 = 明文。默认 587。
- `tls`：`none` / `starttls` / `implicit`。默认 `starttls`。
- `timeoutSeconds`：默认 10。`onEmail` 是异步钩子，宿主不等结果，
  所以不必压到宿主的 2s fire 超时以内。连接与每个往返都有 deadline ——
  没有 deadline 的 SMTP 往返会把插件的 goroutine 永久挂住。
- **配置不完整的处理**：`init` 阶段只警告（管理员可能在逐步补），
  但真要投递时缺 `host` / `from` **直接失败**，不会发出去一封注定被拒的信。
- `password` 在日志里被脱敏。注意：**插件配置在数据库里仍是明文**
  （宿主侧的加密存储还排在计划里）。

## 测试

`main_test.go` 用一个**最小可用的假 SMTP 服务器**做真实协议往返
（不是 mock）：客户端走完整的 `net/smtp`，服务端逐条应答，最后断言收到的报文。

| 用例 | 断言 |
|---|---|
| `TestSendChineseSubjectAndBody` | 中文主题 RFC 2047 编码且**解码回原文**（含折行）；正文 base64 解码无损；MAIL FROM / RCPT TO 正确 |
| `TestSendASCIIBodyUses7bit` | ASCII 正文走 7bit，且换行已归一化为 CRLF |
| `TestSendStripsHeaderInjection` | 注入的 `Bcc:` / `X-Evil:` 不成为独立邮件头 |
| `TestSendFailsClosedWithoutConfig` | 缺 host / 缺 from / tls 非法 均失败 |
| `TestSendRejectsMissingStartTLS` | 服务器未声明 STARTTLS 时给出可操作报错 |
| `TestSendRejectsAuthWhenNotAdvertised` | 配了 username 但服务器无 AUTH 时明确报错 |
| `TestSendImplicitTLS` | 465 路径走通（测试接缝跳过自签证书校验） |
| `TestSendNeverBlockedError` | 任何失败路径的 error 都**不含 `blocked`** |

```bash
cd examples/plugins/smtp-email && go test ./...
```

> 已接进 `.github/workflows/smoke.yml`。示例插件是**独立 module**，
> `backend` 的 `go test ./...` 覆盖不到它 —— 不接进 CI 就等于白写。

## 已知的脚手架重复

`main.go` 里的协议结构（`request` / `response` / `initParams` …）与
`referer-guard`、`hmac-auth` 是重复的。这是刻意的：插件不 import 宿主包，
以免把整个后端拖进依赖图并造成版本耦合。若重复再增多，值得抽一个
独立 `plugin-sdk` module。
