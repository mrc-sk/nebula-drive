# notify-webhook

NebulaDrive 插件：把站内通知（`onEmail` 事件）转发到外部 webhook。支持
Slack、飞书、钉钉、Discord 和任意通用 JSON 接收端，并提供一个
`nebula plugin notify-webhook` 自测命令。

- 只用 Go 标准库，无第三方依赖
- 每个请求独立超时（默认 5 秒）
- 网络错误与 5xx 自动重试一次，4xx 不重试
- 日志中的 webhook URL 强制脱敏为 `scheme://host/…`
- 最多处理 10 个 webhook
- 发送失败不影响站内通知本身，也不会返回拦截错误

## 安装

把本目录（其中的 `plugin.json` 与编译出的 `notify-webhook` / `notify-webhook.exe`）
放进 NebulaDrive 的插件目录，在管理端启用后填写配置。

构建：

```bash
go build -o notify-webhook .
```

Windows：

```powershell
go build -o notify-webhook.exe .
```

## 配置

```json
{
  "webhooks": [
    {
      "url": "https://hooks.slack.com/services/T000/B000/XXXX",
      "format": "slack",
      "headers": {
        "X-Custom": "value"
      },
      "timeoutSeconds": 5
    },
    {
      "url": "https://example.com/generic-hook",
      "format": "generic"
    }
  ],
  "includeBody": true,
  "maxBodyChars": 2000,
  "ignoreSubjects": ["验证码"]
}
```

| 字段 | 说明 |
| --- | --- |
| `webhooks` | 目标列表。最多处理前 10 个，超出的会在日志与自测输出中提示。 |
| `webhooks[].url` | 必填，只允许 `http://` 或 `https://`。可以包含基本认证信息。 |
| `webhooks[].format` | `generic` / `slack` / `feishu` / `dingtalk` / `discord`。留空按 `generic` 处理，未知值只跳过该目标。 |
| `webhooks[].headers` | 可选，附加到该目标的请求头，例如 `Authorization`。值不会出现在日志中。 |
| `webhooks[].timeoutSeconds` | 单次 HTTP 尝试超时，默认 5 秒。重试一次时总耗时最多约为它的两倍。 |
| `includeBody` | 是否携带通知正文，默认 `true`。显式写 `false` 可关闭。 |
| `maxBodyChars` | 正文字符上限（Unicode 字符，不是字节），默认 2000。超长会在预算内截断并追加 `…（正文已截断）`。 |
| `ignoreSubjects` | 标题子串忽略列表，区分大小写；命中任意一项就不发送。空字符串会被忽略，避免误配后关闭全部通知。 |

### 各平台的 webhook 地址

- **Slack**：在 Slack App 中启用 Incoming Webhooks，添加后复制
  `https://hooks.slack.com/services/...`。**这个 URL 本身就是凭据**，
  路径里的 token 泄露等于任何人都能往频道发消息。
- **飞书**：群设置 → 群机器人 → 添加自定义机器人，复制
  `https://open.feishu.cn/open-apis/bot/v2/hook/...`。如果机器人开启了
  “签名校验”，本插件不实现签名，请改用关键词或 IP 白名单，或关闭签名校验。
- **钉钉**：群设置 → 智能群助手 → 添加机器人 → 自定义，复制
  `https://oapi.dingtalk.com/robot/send?access_token=...`。安全设置建议至少启用
  “自定义关键词”（例如 `NebulaDrive`），因为本插件不支持加签。
- **Discord**：频道设置 → 整合 → Webhook → 复制 Webhook URL，
  形如 `https://discord.com/api/webhooks/<id>/<token>`。
- **通用**：任何接受 `POST application/json` 的 HTTP(S) 端点。

### 各格式的请求体

`generic`：

```json
{
  "to": "user@example.com",
  "subject": "备份完成",
  "body": "文件已经备份。",
  "time": "2026-10-18T09:00:00Z"
}
```

`slack`：

```json
{"text": "备份完成\n文件已经备份。"}
```

`feishu`：

```json
{"msg_type": "text", "content": {"text": "备份完成\n文件已经备份。"}}
```

`dingtalk`：

```json
{"msgtype": "text", "text": {"content": "备份完成\n文件已经备份。"}}
```

`discord`：

```json
{"content": "备份完成\n文件已经备份。"}
```

`includeBody=false` 时上述正文为空，文本格式仍保留 `标题\n` 的结构。
自定义 `headers` 会覆盖同名内置头（包括 `Content-Type`）。

## 自测

启用插件并填好配置后运行：

```bash
nebula plugin notify-webhook
```

插件会向所有已配置 webhook 发一条测试消息，并逐个打印成功/失败：

```text
notify-webhook 自测：成功 1 个，失败 1 个
  [1] https://hooks.slack.com/…（slack）: 成功
  [2] https://oapi.dingtalk.com/…（dingtalk）: 失败：HTTP 400
```

- 任一目标失败（或一个 webhook 都没配）时，命令退出码为 `1`。
- 命令名不是 `notify-webhook` 时插件什么都不做，也不输出。
- `ignoreSubjects` 不作用于自测消息。

## 安全说明

- **只允许 HTTP(S)。** `ftp://`、`file://` 等 scheme 在构造请求前就会被拒绝，
  不会产生任何网络或文件访问。
- **URL 日志脱敏。** webhook URL 常含凭据：Slack/Discord 的 token 在路径里，
  通用端可能是 `https://user:pass@host/...`。插件只记录
  `scheme://host/…`，路径、查询串、用户名、密码都不会进入 stderr。
  网络错误文本也会二次清理，防止 `*url.Error` 把完整 URL 带出来。
- **headers 永不打印。** `Authorization` 之类的值不会出现在日志或自测输出里。
- **不跟随重定向。** 避免一个 302 把请求（和自定义认证头）带到未预期的地址，
  也避免绕过初始 URL 的 scheme 校验。3xx 会被当作失败。
- **请求有超时。** 使用独立的 `http.Client` 并显式设置 `Timeout`，
  不使用默认客户端的无限等待。TLS 证书按系统根证书正常校验，没有跳过校验的开关。
- **数量与正文有上限。** 最多 10 个目标；正文按 `maxBodyChars` 截断。
  响应体最多读 4 KiB 后丢弃，不记录内容。
- **失败不阻塞站内通知。** `onEmail` 是异步通知钩子，失败只写 `warn` 日志，
  不返回 `blocked`。成功、部分失败、全部失败分别回写
  `ctxPatch.notifyWebhook = sent / partial / failed`，被忽略或无配置时为
  `ignored / skipped-no-webhooks`（宿主不读取这些值，仅供日志/调试）。

## 已知边界

- **没有去重，也没有速率限制。** 同一 IP 或同一事件重复触发时，每次通知都会
  真实外发。通知洪水会等量打到 webhook，可能触发对方限流。需要聚合/节流时应在
  上游处理，本插件不做缓冲。
- **没有持久化队列。** 插件进程退出时，尚未完成的投递会丢失；重启后也不会补发。
- **投递在 fire 处理中同步完成**（各 webhook 之间并发）。因此一个很慢的端点会
  延迟该插件后续 RPC 的处理；宿主侧异步超时不会取消已经发出的 HTTP 请求。
- **不做平台签名。** 飞书/钉钉的加签、Slack 的签名校验都不实现；请使用关键词、
  IP 白名单或网络边界。自定义 `Authorization` 头可直接配置，但不会自动刷新 OAuth。
- **4xx 与 3xx 不重试**，包括 429。收到 429 时应调整上游发送频率。
- **只按前 10 个配置项发送**，超出的 10 个之后的目标既不发送也不出现在逐条结果里。
- **没有出站 IP 限制。** URL 由管理员配置，插件允许访问内网地址（自建 webhook
  常见需求）。scheme 白名单不构成 SSRF 防护；不要把插件配置权限交给不可信用户。
- **截断标记占用字符预算。** 当 `maxBodyChars` 小到连标记都放不下时只截断正文，
  不再追加说明，以保证不超过上限。
- **测试消息不套用 `ignoreSubjects`。** 自测的目的就是验证链路，不应被标题过滤拦截。

## 开发

```bash
go vet ./...
go test ./... -count=1
```

测试使用 `httptest.NewServer` 做真实 HTTP 往返，覆盖五种载荷逐字段断言、
自定义头、非 HTTP(S) 拒绝、标题忽略、正文截断、5xx 重试、4xx 不重试、
日志脱敏以及 `onCLI` 的命令分发与输出。
