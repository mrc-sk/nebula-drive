# 邀请码管理面板（nd-invite-panel）

NebulaDrive 官方插件 · 协议 v2 · 纯页面插件（`hooks=[]`）。

## 它做什么

在**后台侧边栏**加一个「邀请码管理」入口，用来：

- 随机生成注册邀请码（`NB-XXXX-XXXX-XXXX`），可设**生成数量**、**每个码可用次数**（0=不限）、**有效期**（天，0=永久）、**备注**；
- 按**邀请码类型（套餐档位）**生成，不同类型决定注册用户的初始配额与套餐有效期；
- 查看全部邀请码（次数、状态、有效期、备注），支持**停用 / 删除 / 复制**；
- 一键复制批量生成的码。

配合 V26-11.2 的注册闭环：即使站点关闭自由注册（`security.allow_register=false`），
访客在登录页「注册」标签填入一个有效、未失效的邀请码，即可完成注册，注册后自动按该码的
套餐类型分配配额/订阅，并累计使用次数（次数用尽自动失效）。

## 安全设计

- 页面与主站**同源**（`/ext/nd-invite-panel/*` 由宿主反代，浏览器 origin 仍是站点域名）；
- 页面 JS 读取 `localStorage.nebula_token`，所有读写都调用宿主 `/api/admin/*` 接口，
  由宿主 `Auth(true) + AdminOnly()` 中间件做鉴权；
- 页面加载时先调 `/api/auth/me` 校验 `isAdmin`，**非管理员不渲染任何管理界面**；
- 插件自身是静态 HTTP 服务，只暴露 `index.html`，不落库、不存密钥、不提供任何
  **未鉴权的配置/写入口**；没有管理员登录态时，直接访问页面也看不到任何数据。

## 契约

- `plugin.json` / `register.go` / `co.BEGG` 三文件契约，`protocolVersion: 2`；
- `runtime: python`，`command: python3 {entry}`，`entry: server.py`；
- `pages`: `area=admin, role=admin` 的侧边栏页面；
- 纯页面插件不订阅任何钩子，宿主不会拉起 JSON-RPC 子进程。

## 依赖

仅需 `python3`（标准库 `http.server`）。无第三方包。

## 许可

AGPL-3.0-only
