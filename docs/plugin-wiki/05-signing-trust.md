# 05 · 签名与信任

> 源码：`backend/pkg/plugin/pbclus/pbclus_sign.go`、`backend/pkg/plugin/pbclus/pbclus.go`、`backend/controllers/plugin_trust.go`、`backend/pkg/plugin/install/install.go`
> 包格式逐字节规范见 [../PBClus-格式规范.md](../PBClus-格式规范.md)。

NebulaDrive 的插件分发格式叫 **PBClus**（v2）。一个 PBClus 包 = 头部 + 清单(`plugin.json`) + 载荷(目录) + 尾部(签名块)。安装器解包、校验签名，再交给 [02-清单](02-manifest.md) 解析。

## 一条必须先说清的事：签名 ≠ 可信

公钥**内嵌在包里**，所以**任何人都能签一个合法的包**：用自己的密钥签，再把公钥一起放进去，`Valid` 就是 true。「验证通过」只证明**内容与签名自洽**，不证明签名者是谁、也不证明你要信任它。

安全语义完全来自宿主侧的**信任库**（keyId 白名单）：只有 `Trusted` 才有意义。安装决策**只能用 `Trusted`**，用 `Valid` 做决策等于没校验——这是这类设计最常见也最危险的误用，所以代码把两个状态拆成两个字段，不给「顺手用错」留空间。

## 三种签名状态

| 字段 | 含义 |
|---|---|
| `present` | 包里是否有签名块 |
| `valid` | 签名与内容是否自洽（**不代表可信**） |
| `trusted` | 签名者的 keyId 是否在宿主的信任库里 |

`valid` 的判定：签名消息 = 域分隔前缀 `PBClus/v2 signed-package\n` ‖ 文件 SHA-256；用内嵌公钥做 Ed25519 验证。缺少 `Verify()` 步骤时攻击者可以改内容同时改 SHA，签名照样「通过」，所以安装前**必须**先 `Verify` 再读状态。

## KeyID 与密钥

- 算法：**Ed25519**（当前唯一，`SigAlgEd25519 = 1`）。
- **KeyID** = 公钥 SHA-256 的**前 8 字节**的十六进制（16 个字符）。用 8 字节而非 32：keyId 是给人看、给人抄的（管理员第一次信任发布者时要能一眼比对），在「插件发布者数量」量级上碰撞可忽略。
- `SigningKey` 结构：`name` / `privateKey`(64 字节 base64) / `publicKey`(32 字节 base64) / `keyId`。
- 生成：`pbclus.GenerateSigningKey(name)`；保存：`Save(path)` 权限 **0600**（这是能签出「被信任包」的私钥，泄漏等于别人可冒充该发布者；Windows 不认 POSIX 位，文档另提醒别把私钥提交进版本库）。

## 公钥分发文件 `.pub.json`

`keygen` 会额外写一个 `<名字>.pub.json`，专门交给管理员：

```json
{ "keyId": "...", "name": "NebulaDrive", "algorithm": "ed25519",
  "publicKey": "base64...", "notice": "这是公钥，可公开；私钥在另一个文件，绝不要发送或提交它。" }
```

为什么要单独一个只有公钥、名字里就写着 `pub` 的文件：私钥文件里**同时含私钥与公钥**，而「把你的密钥文件发给我」是极自然的表达——一旦照做就是私钥泄漏。给一个只有公钥的文件能把这条歧义堵住。

## 信任库（TrustStore）

信任库是 `keyId → 名称` 的白名单（`KeyRing`，字段 `keys: [{keyId, name, publicKey?, addedAt}]`）。建立信任的**唯一推荐入口**是 `TrustRecordFromPublicKey(pubBase64, name)`：keyId 由公钥推导，两者必然自洽，从根上排除「keyId 写的是可信发布者、公钥却是别人的」替换。也允许 `TrustRecordFromKeyID(keyId, name)`（发布者只公布了 keyId 时），但**不如给公钥安全**：keyId 是公钥摘要，无法反向校验，挡不住「同一 keyId 配了别人的公钥」。

信任库自身 `Validate()` 会校验：keyId 长度 16 且为十六进制、不重复、填了 `publicKey` 就必须与 keyId 自洽。

## 三种签名策略（trustMode）

| 模式 | 行为 |
|---|---|
| `off` | 关闭签名检查。任何包都能安装，界面也不再提示签名状态 |
| `warn`（默认） | 照常安装，但会提示签名状态 |
| `require` | 只接受受信任签名的包。**没有信任任何发布者时，所有 PBClus 包都会被拒绝** |

改 `require` 前的诚实提示：存量无签名插件会让**新安装**全部被拒（已安装的不受影响）。接口在「有存量无签名插件」时要求带 `confirm=true` 才放行。

## 管理接口（见 [08-管理端](08-admin.md)）

- `GET /api/admin/plugins/trust` — 查策略 + 信任库内容 + 已安装/已签名/已信任计数
- `POST /api/admin/plugins/trust/key` — 加一枚公钥（**只收 `publicKey` 或 `keyId`，明确拒绝私钥**）
- `DELETE /api/admin/plugins/trust/:keyId` — 移除一枚公钥（撤销的唯一手段；已安装的插件不会被自动卸载）
- `PUT /api/admin/plugins/trust/mode` — 改策略（`off`/`warn`/`require`，`require` 需 `confirm`）

## 安全与诚实边界

- **插件没有沙箱**：与主服务同权系统权限，隔离靠进程边界 + 权限自觉，不是容器。
- **`permissions` 声明不强制**：只展示给管理员看，宿主不校验。
- **`valid` 不等于 `trusted`**：安装决策只用 `trusted`。
- **商店条目 `version`/`author` 不可信**：真实清单以下载后的 `plugin.json` 为准。
- **别把私钥发到任何地方**：接口只收公钥；私钥文件权限 0600 且严禁提交版本库。
