# collab-redirect —— 把协作编辑请求跳转到外部编辑器

> 状态：**已实现**。宿主在 `GET /api/files/:id/collab` 触发 `onCollabOpen`
> （同步钩子），本插件在 `ctxPatch.url` 里写地址，宿主随即 302 跳转。

## 用途

NebulaDrive 自带的在线协作只覆盖 md/txt（yjs）。Office 文档要在线编辑，
通常得接 OnlyOffice、Collabora 或自建的编辑页。本插件就是这两者之间的
**路由器**：

- 扩展名命中 `extensions`，**或** MIME 命中 `mimePrefixes` → 写入跳转地址；
- 其它情况（包括本站的 md/txt）→ **什么都不写**，宿主行为完全不变。

它只负责判断与拼 URL，不读文件、不代理下载、不签发 token、不发起任何网络请求。
`onCollabOpen` 是同步钩子，宿主会阻塞等它返回（默认 2 秒超时），
所以插件在钩子里只做内存里的字符串处理。

## 配置

安装后在插件配置里填写（JSON）：

```json
{
  "editorURL": "https://office.example.com/editor?fileId={{fileId}}&name={{fileName}}&user={{userName}}",
  "extensions": ["docx", "xlsx", "pptx", "odt", "ods", "odp", "doc", "xls", "ppt"],
  "mimePrefixes": ["application/vnd.openxmlformats", "application/vnd.oasis.opendocument"],
  "appendUserQuery": false,
  "openInNewWindow": true
}
```

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `editorURL` | 空 | 跳转地址模板。**为空时插件完全不生效**，等价于没装。 |
| `extensions` | 上面 9 个 | 小写、不含点。显式写 `[]` 表示关闭扩展名匹配。 |
| `mimePrefixes` | 上面 2 个 | 前缀匹配；显式写 `[]` 表示关闭 MIME 匹配。 |
| `appendUserQuery` | `false` | 为 `true` 时在 URL 后追加 `ndUser=<userName>&ndIp=<ip>`。 |
| `openInNewWindow` | `true` | 仅记录与 CLI 展示。宿主执行的是 302，无法强制新窗口（见“已知边界”）。 |

匹配规则是“或”：只要扩展名**或** MIME 前缀任一命中就跳转。
扩展名从 `fileName` 取（`Report.DOCX` 也认），MIME 比较不区分大小写。

### 占位符

| 占位符 | 来源 | 示例（转义后） |
| --- | --- | --- |
| `{{fileId}}` | 文件 ID | `42` |
| `{{fileName}}` | 文件名 | `%E6%8A%A5%E5%91%8A+%26+%E9%99%84%E8%A1%A8.docx` |
| `{{userName}}` | 当前用户名 | `%E5%BC%A0%E4%B8%89` |
| `{{userId}}` | 当前用户 ID | `7` |
| `{{mimeType}}` | 文件 MIME | `application%2Fvnd.openxmlformats...` |
| `{{ip}}` | 客户端 IP | `203.0.113.9` |

所有值都经 `url.QueryEscape` 转义，**只适合放在查询串里**。
不要拼进 host 或 path：空格会被编码成 `+`（path 里这是字面加号），
`/` 也会被编码成 `%2F`。

未知占位符（例如 `{{fileNamee}}`）会**原样保留**，并在 init 日志里告警，
不会静默替换成空串 —— 静默丢参数生成的 URL 看起来完全正常，极难排查。

## OnlyOffice 配置示例

OnlyOffice Document Server 需要一个 JSON 编辑器配置，其中 `document.url`
是它自己要去下载文件的地址，`editorConfig.callbackUrl` 是保存回调。
这些**不是**一个普通查询串能表达的。因此 `editorURL` 应指向你自己的
一个薄适配页/连接器，由它用 `fileId` 换出带签名的配置：

```json
{
  "editorURL": "https://drive.example.com/office/launch?fileId={{fileId}}&fileName={{fileName}}&userId={{userId}}&userName={{userName}}&ip={{ip}}",
  "extensions": ["docx", "xlsx", "pptx"],
  "mimePrefixes": ["application/vnd.openxmlformats"],
  "appendUserQuery": false
}
```

适配页要做的事（不在本插件范围内）：

1. 校验当前会话，确认用户确实有权访问该 `fileId`；
2. 生成一个短期、带签名的下载地址（OnlyOffice 服务器去拉文件，
   它带不了用户的登录 Cookie）；
3. 生成 `callbackUrl`，并校验回调来源；
4. 返回包含 `document` / `editorConfig` 的配置页。

如果你的 OnlyOffice 前面已经有网关能接受 `fileId` 等参数，也可以直接把
`editorURL` 指向网关。

## Collabora / WOPI 配置示例

Collabora 走 WOPI，`WOPISrc` 是 WOPI 主机上文件地址的 URL 编码形式：

```json
{
  "editorURL": "https://collabora.example.com/browser/dist/cool.html?WOPISrc=https%3A%2F%2Fdrive.example.com%2Fwopi%2Ffiles%2F{{fileId}}&lang=zh-CN",
  "extensions": ["docx", "xlsx", "pptx", "odt", "ods", "odp"],
  "mimePrefixes": ["application/vnd.openxmlformats", "application/vnd.oasis.opendocument"]
}
```

这里的静态前缀 `https%3A%2F%2F...%2F` 已经手工编码，`{{fileId}}` 是纯数字，
替换后仍是合法的 `WOPISrc`。宿主还需要实现 WOPI 的
`CheckFileInfo` / `GetFile` / `PutFile` 端点，并校验 Collabora 的访问令牌——
同样不属于本插件。

## 为什么必须用外部编辑器自己的下载 / 回调地址

本插件只返回一个 302。浏览器跳到编辑器后，**编辑器服务器**会自己去取文件：

- OnlyOffice：按 `document.url` 下载，按 `callbackUrl` 保存；
- Collabora：按 `WOPISrc` 调 WOPI 主机。

所以这个地址必须是**编辑器服务器能访问、且能通过其自身鉴权**的地址。
不能直接把 NebulaDrive 的 `/api/files/:id/download` 填进去：
那个端点依赖用户浏览器的登录 Cookie，编辑器服务器请求时没有 Cookie，必然 401；
就算你放开了匿名访问，也等于把文件公开。正确做法是签发短期、限定文件、
只读/读写权限明确的下载地址，或实现完整的 WOPI 主机。

## 安全说明

- **URL 转义**：文件名可能含 `&`、空格、中文、`#`。不转义时 `&` 会拆出额外
  查询参数（可被用来注入编辑器参数），`#` 会把后面的内容变成 fragment，
  服务端直接收不到。本插件对每个占位符值与追加参数都做 `url.QueryEscape`。
- **scheme 校验**：渲染结果必须是 `http`/`https` 绝对 URL 且 host 非空。
  否则不跳转并在 stderr 记 error。这条挡住了 `javascript:`、
  `data:text/html,...`、协议相对的 `//host/path` 等 —— 宿主拿到 url 会直接
  `http.Redirect`，不校验等于把“打开文档”变成 XSS/钓鱼入口。
- **日志脱敏**：stderr 只打印 `scheme://host/path`，查询串与 fragment 整体
  替换为 `<redacted>`。`editorURL` 里经常带 token/签名，不能整条落日志。
- **CLI 输出例外**：`nebula plugin collab-redirect` 会打印完整模板与渲染示例，
  否则管理员无法核对占位符与转义。这条命令需要能访问服务端配置，属于管理员操作。
- **配置明文**：宿主目前把插件配置明文存在数据库里。不要把长期密钥直接写进
  `editorURL`；用短期签名或让适配页去取密钥。
- **身份参数**：`appendUserQuery=true` 会把用户名与 IP 放进 URL，
  它们会进入浏览器历史、编辑器访问日志与 Referer。非必要不要开。

## 安装与命令行

把编译好的可执行文件与 `plugin.json` 放进插件目录，在管理端安装/启用。
改配置后重启插件即可生效（无需重启主服务）。

```sh
go build -o collab-redirect .

# 打印生效配置与一个渲染示例，用来核对模板：
nebula plugin collab-redirect
```

示例输出（节选）：

```
collab-redirect v1.0.0
生效配置：
  editorURL        : https://office.example.com/editor?fileId={{fileId}}&name={{fileName}}
  extensions       : docx, xlsx, ...
  ...
渲染示例（示例 ctx：fileId=42, userId=7, fileName=报告 & 附表.docx）：
  https://office.example.com/editor?fileId=42&name=%E6%8A%A5%E5%91%8A+%26+%E9%99%84%E8%A1%A8.docx
```

`editorURL` 为空时，CLI 会明确提示“插件不生效”。

## 已知边界

- **只做跳转，不做鉴权。** 本插件不验证访问者、不签发令牌、不校验外部编辑器
  的请求。宿主在触发钩子前已经检查了文件所有者/管理员，但**外部编辑器端点
  必须自己再校验一次权限**：否则任何人只要猜到 `fileId` 就能通过编辑器读到文件，
  等于把文件公开。这是部署时最容易忽略的一条。
- **不代理文件、不处理回调。** 下载地址、保存回调、WOPI 端点都要另行实现。
- **`openInNewWindow` 无法真正生效。** 宿主的实现是 HTTP 302，浏览器必然在
  当前标签页打开。字段被解析并展示，但插件不能改变宿主行为。
- **占位符只能放在查询串。** 见上文 `url.QueryEscape` 的说明。
- **不做 URL 长度/复杂度限制。** 极长的模板由管理员负责；异常配置会在 init
  阶段通过渲染示例暴露。
- **不劫持 md/txt。** 默认扩展名与 MIME 都不含它们；如果手动把 `md` 加进
  `extensions`，本站的 yjs 协作就会被跳转走 —— 这是显式配置的预期结果。
