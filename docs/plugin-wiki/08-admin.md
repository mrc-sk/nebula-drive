# 08 · 管理端与 API

> 源码：`backend/controllers/plugins.go`、`backend/controllers/plugin_trust.go`
> 全部在 `/api/admin/plugins`，需管理员登录。

## 插件生命周期接口

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | ``（根） | 列表（含实时 `status` / `pid` / 运行 `error`） |
| GET | `/hooks` | 钩子清单 + 真实 handler 计数（进程内 + 进程外，分开展示） |
| GET | `/store` | 远端商店清单 |
| GET | `/agreement` | 协议全文 + 当前同意状态 |
| POST | `/agreement` | 记录同意（协议版本不匹配返回 409） |
| GET | `/logs?name=&limit=` | 插件日志（每插件 500 条环形） |
| POST | `/install` | 安装（`source`: `local` / `url`） |
| POST | `/:name/enable` | 启用（拉起子进程） |
| POST | `/:name/disable` | 禁用（杀子进程） |
| POST | `/:name/restart` | 重启（重读清单） |
| POST | `/:name/uninstall` | 卸载（**需 `X-Confirm-Password` 二次确认**） |
| PUT | `/:name/config` | 改配置（重启后生效） |

## 插件协议关卡（agreement）

- 首次进入插件页弹窗，要求阅读并同意。
- **必须滚动到底**才能勾选——一打开就能点同意等于没要求同意。
- **后端强制**（`requireAgreement`）：未同意时安装/启用接口直接 403。只靠前端弹窗的话，直接调 API 就能绕过。
- 协议正文硬编码在 `pkg/plugin/agreement.go`，不从配置文件读（避免部署者改掉管理员看到的内容）。协议升级后需重新同意。

## 安装安全（解压一个 zip 就跑起任意代码，这里是唯一屏障）

| 风险 | 防护 |
|---|---|
| zip slip（`../../etc/passwd`） | 每条目 `filepath.Clean` 后校验前缀，越界立即**报错**（不静默跳过） |
| zip bomb | 解压总量 256MB / 单文件 64MB / 条目数 2000 三重限额 |
| 符号链接指向宿主文件 | 直接拒绝 |
| Windows 保留名插件 | 拦截（`CON`/`NUL`/`COM1`…） |
| `entry` 越界执行 | 禁止路径分隔符 + 运行时二次前缀校验 |
| 下载非 http(s) | 只允许 http/https（挡掉 `file://`） |
| 卸载误操作 | `RequireConfirm` 中间件，需二次确认密码 |

逐条实现见 `backend/pkg/plugin/install/install.go` 与 [02-清单 · entry 约束](02-manifest.md)。

## 信任管理接口（签名/信任）

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/trust` | 查策略（`off`/`warn`/`require`）+ 信任库内容 + 已安装/已签名/已信任计数 |
| POST | `/trust/key` | 加一枚公钥（只收 `publicKey` 或 `keyId`；**明确拒绝私钥**） |
| DELETE | `/trust/:keyId` | 移除一枚公钥（撤销的唯一手段；已安装插件不自动卸载） |
| PUT | `/trust/mode` | 改策略；`require` 在「有存量无签名插件」时需 `confirm=true` |

> 加公钥接口只接受 `publicKey`（base64，推荐）或 `keyId`——**绝不接受私钥**（64 字节 base64，长度即可区分）。若有人把私钥贴进来，接口会明确回拒并提示改用 `.pub.json` 里的 `publicKey`。

## 最容易被忽略的坑

安装 / 启用成功后，列表会暴露「**数据库说启用但进程没跑**」的矛盾——管理员会看到「已启用」却毫无效果。先查列表的 `status`/`error`，再看 `/logs`；多半是 `entry` 拼错、init 返回 `ok=false`、或启动超 10s。
