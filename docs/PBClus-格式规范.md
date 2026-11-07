# PBClus 容器格式规范 v2

> **状态**：v2 是当前**写出**版本（`Version = 2`）；读取方必须接受 **v1 ~ v2**（`MinReadVersion = 1`）。
> **权威实现**：`backend/pkg/plugin/pbclus/`（`pbclus.go` 布局与校验、
> `pbclus_platform.go` 平台、`pbclus_sign.go` 签名与信任），NebulaDrive。
> **打包/解包工具**：`pbclus-pack`（源码位于 `backend/cmd/pbclus-pack`）。
>
> 本规范与实现不一致时，**以实现为准**。实现是唯一的事实来源，本文档只是把它写清楚。

---

## 0. 范围

本文规定 PBClus 容器（扩展名 `.pbclus`）的：

- 字节级布局与字段语义（含 **v2 的可选签名块**）；
- 清单（Manifest）JSON 结构（含 **v2 的多平台字段**）；
- 完整性校验的覆盖范围与校验顺序；
- 平台标签的语法、清单声明与解包时的平台选择规则；
- 签名块的构造、验证与信任语义；
- 条目路径规则；
- 资源限额；
- 条目压缩编解码；
- 读取、校验、解包的安全要求；
- 写入（打包）的确定性与可复现要求；
- 格式识别（魔术字嗅探）规则；
- 版本与扩展策略。

本文面向**实现者**：你要用别的语言写一个兼容的打包器或校验器，只需要这份文档。

不规定的内容：宿主的安装流程、插件清单（`plugin.json`）的字段语义、目录布局的约定、
签名策略与信任管理的具体界面。那些属于宿主侧策略，见 `docs/插件制作指南.html`
与 `backend/pkg/plugin/install/`。

> **v1 读者请注意**：v2 相对 v1 增加了「条目平台标签」与「可选签名块」。
> v1 的解析方**不认识平台字段**，会把多平台包里的各平台二进制解到同一个路径上、
> 最后写入者胜出 —— 结果是**静默装上一个架构不对的二进制**。这正是版本号存在的意义：
> 老解析方遇到 v2 必须**明确拒绝**，而不是尽力解析。见 §4。

---

## 1. 设计目标

PBClus 不是「zip 改个后缀」，而是重新定义的容器。改后缀只是换名字：解析规则、
攻击面、允许的结构全都照旧，代价是「看到 `.pbclus` 就知道该怎么处理」这件事不再可靠。
格式层要解决的八件事：

1. **路径必须在格式层就规范化。** 条目路径必须是相对路径、纯正斜杠、规范化形式。
   `../`、绝对路径、`a//b`、`./a`、反斜杠在**打包时**就被拒绝，解包时再校验一次。
   zip 把这件事留给解压方，于是每个解压方各防一遍，防不住的就有洞。
2. **只有普通文件与目录。** 格式里不存在符号链接、设备文件、FIFO 这些条目类型，
   直接消灭一整类攻击（zip 的 symlink 条目能让解压结果指向宿主的任意文件）。
3. **拒绝重复条目。** zip 允许同名条目，解压结果取决于实现顺序。v2 起唯一性按
   **(路径, 平台)** 判定：同一路径可以有多个平台的版本，同一平台下同名出现两次仍然拒绝。
4. **拒绝文件/目录同名。** 同时存在 `lib` 与 `lib/x.so` 时，解压结果同样取决于顺序。
5. **可整体校验。** 头部带清单与载荷摘要，尾部带整文件摘要与总长，每个条目还有自己的
   SHA-256。损坏或截断能被精确定位到某个文件。
6. **能明确说「这不是 PBClus」。** 靠魔术字判断，不靠扩展名。
7. **一个容器可以装多个平台。** 条目带平台标签，包声明自己支持哪些平台，
   安装器据此**在解包之前**就能回答「这个包能不能装在这台机器上」，
   而不是解到一半报「入口文件不存在」。
8. **可选签名，但把边界说清楚。** 签名证明「内容与签名自洽」，
   **不**证明「可信」—— 公钥内嵌在包里，任何人都能签一个验证通过的包。
   安全语义完全来自宿主侧的**信任库**。见 §3.6 与 §12。

同时必须明确**不**做的事，见 §12.5。

---

## 2. 术语与约定

| 约定 | 取值 |
| --- | --- |
| 字节序 | 所有整数均为**小端**（little-endian） |
| 偏移 | 均从文件起点算起，单位字节 |
| `SHA-256` | FIPS 180-4，输出 32 字节 |
| `CRC-32` | CRC-32/IEEE（多项式 `0xEDB88320`，Go 的 `hash/crc32.ChecksumIEEE`），输出 4 字节 |
| `Ed25519` | RFC 8032。公钥 32 字节，签名 64 字节，私钥 64 字节（seed+public） |
| `DEFLATE` | RFC 1951 原始流，**没有** zlib（RFC 1950）或 gzip（RFC 1952）外层头 |
| 路径分隔符 | 只允许 `/`（U+002F），**禁止** `\` |
| 平台标签 | `<goos>/<goarch>`，如 `linux/amd64`、`windows/amd64`、`darwin/arm64`；`any` 是特殊值 |
| 平台目录名 | 平台标签把 `/` 换成 `-`：`linux/amd64` → `linux-amd64`（目录名不能含斜杠） |
| `keyId` | 公钥 SHA-256 的**前 8 字节**，小写十六进制，**定长 16 字符** |
| 域分隔前缀 | 签名消息的前缀 `"PBClus/v2 signed-package\n"` |
| 十六进制 | 小写，无前缀，摘要定长 64 字符 |
| 时间 | RFC 3339，**UTC**（例：`2026-10-07T05:51:00Z`） |
| `MiB` | 1 MiB = 1048576 字节（本文的「MB」一律指 MiB） |

「载荷偏移（entry offset）」指相对**载荷区起点**的偏移，不是相对文件起点。
`contentEnd` 指「头部 + 清单 + 载荷」的结束偏移，即 `payloadOff + payloadLen`（见 §3.1）。

---

## 3. 文件布局

### 3.1 总览

```
文件起点                                                                文件末尾
│                                                                             │
├──────────────┬─────────────────┬───────────┬───────────────┬────────────────┤
│ Header 128 B │ Manifest (JSON) │ Payload   │ Signature ?   │ Footer 48 B    │
└──────────────┴─────────────────┴───────────┴───────────────┴────────────────┘
```

`Signature` 区块**仅在**头部 `flags` 的 `FlagSigned` 位置位时存在，恒为 108 字节
（见 §3.6）。五个区块**顺序固定、无填充、无对齐要求**：

| 区块 | 起始偏移 | 长度 |
| --- | --- | --- |
| Header | `0` | 128（固定） |
| Manifest | `128`（固定） | `manifestLen`（JSON 字节数，> 0） |
| Payload | `128 + manifestLen`（固定） | `payloadLen` |
| Signature | `contentEnd`（`FlagSigned` 置位时） | 108（固定） |
| Footer | `文件长度 - 48` | 48（固定） |

由此得到四条硬不变量：

```
manifestOff == 128
payloadOff  == manifestOff + manifestLen
contentEnd  == payloadOff + payloadLen
contentEnd + sigLen + 48 == 文件实际长度        # sigLen ∈ {0, 108}
```

第四条同时挡住了「截断」与「尾部追加内容」两种损坏，
也把「声明有签名但实际没有」这种拼接挡在门外。

**`contentEnd` 为什么重要。** 它既是整文件摘要（`fileSHA`）的右边界，
也是签名覆盖范围的右边界。签名块**不在**被覆盖范围内 —— 它包含签名本身，算进去就自指了。
因此摘要的右边界不是「文件末尾 − 48」：带签名时两者之间还夹着 108 字节。

### 3.2 Header（128 字节）

| 偏移 | 长度 | 类型 | 字段 | 说明 |
| --- | --- | --- | --- | --- |
| 0 | 8 | `[8]byte` | `magic` | `50 42 43 6C 75 73 1A 0A`，即 ASCII `PBClus` 后跟 `0x1A`、`0x0A` |
| 8 | 2 | `uint16` | `version` | 写出时为 `2`；读取方接受 `1 ~ 2` |
| 10 | 2 | `uint16` | `headerSize` | `128` |
| 12 | 4 | `uint32` | `flags` | 位标志，见下 |
| 16 | 4 | `uint32` | `entryCount` | 条目数 |
| 20 | 8 | `uint64` | `manifestOff` | 恒为 `128` |
| 28 | 8 | `uint64` | `manifestLen` | 清单字节数 |
| 36 | 8 | `uint64` | `payloadOff` | 恒为 `manifestOff + manifestLen` |
| 44 | 8 | `uint64` | `payloadLen` | 载荷字节数 |
| 52 | 32 | `[32]byte` | `manifestSHA` | `SHA-256(Manifest 字节)` |
| 84 | 32 | `[32]byte` | `payloadSHA` | `SHA-256(Payload 字节)` |
| 116 | 4 | `uint32` | `headerCRC` | `CRC-32/IEEE(Header[0:116])` |
| 120 | 8 | `[8]byte` | `reserved` | 当前全 `0x00` |

**魔术字为什么带 `0x1A 0x0A`。** 这两个字节是从 PNG 学来的老办法：`0x1A` 是 DOS 的
EOF 字符，`0x0A` 是换行。某些工具用文本模式处理文件时会在 EOF 处截断，或在传输时改写
换行。带这两个字节能让「文件被文本模式糟蹋过」立刻暴露，而不是解出一堆乱码。

**flags 位定义。**

| 位 | 名称 | 含义 |
| --- | --- | --- |
| bit0 | `FlagDeflate` | 容器内**至少有一个**条目的 `codec` 为 `deflate` |
| bit1 | `FlagSigned` | 文件带 108 字节签名块（紧随载荷之后、尾部之前） |
| bit2–31 | — | 保留，写入时必须为 0 |

`FlagDeflate` 只作**提示**（例如工具据此显示「容器使用了压缩」）。参考实现不校验它与实际
条目是否一致，实现者也**不得**把它当作安全判据或正确性判据。

`FlagSigned` 则**必须**与「文件里是否真的有签名块」以及清单里的 `signed` 字段一致（§8.1）。
长度不变量已经把「声明有签名但没有」挡住，「声明没有签名但多出 108 字节」则由长度不变量挡住。

**version 的处理。** 读取方必须检查 `MinReadVersion ≤ version ≤ Version`，
即 `1 ≤ version ≤ 2`；**更高版本必须直接拒绝**，并给出实际读到的版本与本程序支持的范围。
不能「尽力解析」：v2 引入平台标签之后，一个 v1 解析方硬读 v3 包完全可能做出错误的事
（例如把多个平台的二进制解到同一路径），而静默做错事比报错危险得多。

**headerCRC 的覆盖范围。** `[0, 116)`，即除保留区以外的**全部**头部字段，
包括 `manifestSHA` 与 `payloadSHA`。

**保留区为什么不参与 CRC。** `[120, 128)` 这 8 字节留给将来的扩展。因为它不在 CRC 覆盖
范围内，将来在其中追加字段时，老解析方仍能通过头部校验并以「忽略未知保留字节」的方式
继续工作。写入方必须写 0，读取方必须忽略其内容。

### 3.3 Manifest

清单是一段 **UTF-8 编码的 JSON 对象**，紧接头部，无 BOM，无前后填充。
`manifestLen` 是它的精确字节数。

```json
{
  "format": "PBClus",
  "version": 2,
  "platforms": ["linux/amd64", "windows/amd64"],
  "createdAt": "2026-10-07T05:51:00Z",
  "tool": "pbclus-pack",
  "signed": true,
  "plugin": {
    "name": "myplugin",
    "title": "我的插件",
    "version": "1.0.0",
    "author": "你的名字",
    "description": "一句话说明",
    "entry": "myplugin",
    "hooks": ["onAntiLeech"],
    "protocolVersion": 1
  },
  "entryCount": 3,
  "totalSize": 8192,
  "entries": [
    {
      "path": "plugin.json",
      "size": 1024,
      "offset": 0,
      "packedSize": 1024,
      "codec": "raw",
      "sha256": "…64 个小写十六进制字符…"
    },
    {
      "path": "myplugin",
      "platform": "linux/amd64",
      "size": 3072,
      "offset": 1024,
      "packedSize": 1024,
      "codec": "deflate",
      "sha256": "…64 个小写十六进制字符…"
    },
    {
      "path": "myplugin.exe",
      "platform": "windows/amd64",
      "size": 4096,
      "offset": 2048,
      "packedSize": 2048,
      "codec": "deflate",
      "sha256": "…64 个小写十六进制字符…"
    }
  ]
}
```

顶层字段：

| 字段 | 类型 | 必需 | 说明 |
| --- | --- | --- | --- |
| `format` | string | 是 | 必须精确等于 `"PBClus"` |
| `version` | number | 是 | v2 为 `2`；必须与头部 `version` 一致 |
| `platforms` | string[] | 否 | 这个包支持哪些平台。`["any"]` = 通用包；否则是具体平台列表。**缺失时按 `["any"]` 处理**（v1 包天然与平台无关） |
| `createdAt` | string | 是 | RFC 3339，UTC |
| `tool` | string | 是 | 打包工具标识，**仅供展示**，读取方不得依赖 |
| `signed` | bool | 否 | 是否有签名块。**出现时必须与头部 `FlagSigned` 一致**；无签名时写入方应省略它 |
| `plugin` | object | 否 | 插件元数据摘要，便于不开包看到关键信息 |
| `entryCount` | number | 是 | 必须等于 `entries` 的长度 |
| `totalSize` | number | 是 | 所有条目 `size` 之和（解压总量，**含所有平台**） |
| `entries` | array | 是 | 条目列表，见下 |

**`platforms` 的自洽性要求**（读取方必须检查，见 §8.2）：

- 元素必须是合法平台标签（§3.3.1），不得重复；
- 数量 ≤ 8（`MaxTargets`）；
- **不得同时出现 `"any"` 与具体平台** —— 那是自相矛盾的声明；
- 声明了某个平台，就**必须至少有一个**该平台的条目，否则包是坏的
  （正常打包器不会这么写）。

#### 3.3.1 平台标签

格式是 `<goos>/<goarch>`，两段，与 Go 的 `GOOS`/`GOARCH` 一一对应，
也与 Docker 的写法一致。例：`linux/amd64`、`windows/amd64`、`darwin/arm64`、`linux/arm64`。

语法（形状）要求：

- 非空，长度 ≤ 40 字节；
- 恰好一段 `/`，两侧都非空；
- 每段只允许 `a-z`、`0-9`、`_`、`.`。

**规范刻意不做「合法 GOOS 白名单」。** 白名单会让格式随 Go 版本更新而失效：
Go 每新增一个 `GOOS`，老解析方就会开始拒绝新包。形状校验挡住的是注入与拼写错误，
那才是这里的职责。

`any` 是保留的特殊值，表示「与平台无关」。它只出现在清单的 `platforms` 里，
**不得**作为条目 `platform` 的值（条目用空字符串表示通用）。

`plugin` 对象（全部可选，缺失即省略）：

| 字段 | 类型 |
| --- | --- |
| `name` | string |
| `title` | string |
| `version` | string |
| `author` | string |
| `description` | string |
| `entry` | string |
| `hooks` | string[] |
| `protocolVersion` | number |

> ⚠️ `plugin` 里的字段**只用于展示**。安装时的权威来源始终是包内的 `plugin.json` ——
> 清单可以被重新打包的人随便填，和商店条目的元数据一样不可信。

条目对象：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `path` | string | 容器内相对路径，必须通过 §5 的全部规则 |
| `platform` | string | **可选**。空/缺失 = **通用**（任何平台都解出来：`plugin.json`、脚本、素材）；否则是平台标签，只给该平台，且必须出现在清单 `platforms` 里 |
| `size` | number | 解压后字节数，`0 ≤ size ≤ 64 MiB` |
| `offset` | number | 相对**载荷**起点的偏移，`≥ 0` |
| `packedSize` | number | 在载荷中占用的字节数，`≥ 0`；`codec == "raw"` 时必须等于 `size` |
| `codec` | string | `"raw"` 或 `"deflate"`，其它值一律拒绝 |
| `sha256` | string | `SHA-256(解压后内容)` 的小写十六进制，**定长 64 字符** |

**唯一性按 `(path, platform)` 判定。** 同一条路径可以有多份不同平台的条目 ——
这正是「一个包装多平台」的实现方式（例如三个 `dist/` 子目录里各有一个 `myplugin`）。
但同一平台下同名条目出现两次仍然拒绝：那会让解包结果取决于顺序。
`platform` 缺失与 `platform` 为空串视为同一件事。

**JSON 序列化约定。**

- 必须输出 UTF-8。
- 读取方**不得**依赖键顺序、空白、数字格式或转义方式；任何合法 JSON 编码都应接受。
- 读取方**必须**拒绝无法解析为 JSON 的清单，以及 `format` / `version` 不匹配的清单。
- 参考实现用 Go 的 `encoding/json` 序列化，因此可复现的字节级输出还受它的默认转义策略
  影响（例如 `<`、`>`、`&` 会被转义成 `\u003c` 等）。跨语言实现**不要求**字节级相同，
  只要求语义等价 —— 但若要追求可复现的字节布局，请照抄这些约定。
- 可选字段（`platforms` / `signed` / 条目 `platform`）为空时**省略**，而不是写 `null`。

### 3.4 Payload

载荷是所有条目**压缩后字节的顺序拼接**，无索引、无对齐、无填充。

- 条目按清单中的顺序紧密排列，不允许空洞（gap）也不允许重叠（overlap）。
- 第 *i* 个条目的 `offset` 必须等于前 *i-1* 个条目 `packedSize` 之和（第 0 个为 `0`）。
- 所有 `packedSize` 之和必须精确等于 `payloadLen`。
- 载荷的 SHA-256 必须等于头部 `payloadSHA`。
- 平台不改变载荷布局：多平台包的各平台条目也是同一段载荷里的普通条目。

### 3.5 Footer（48 字节）

| 偏移 | 长度 | 类型 | 字段 | 说明 |
| --- | --- | --- | --- | --- |
| 0 | 32 | `[32]byte` | `fileSHA` | `SHA-256(Header ‖ Manifest ‖ Payload)`，即 `SHA-256(文件[0:contentEnd])` |
| 32 | 8 | `[8]byte` | `magic` | 与头部相同的魔术字 |
| 40 | 8 | `uint64` | `totalLen` | 整个文件的长度 |

`fileSHA` **不覆盖尾部自身**，也**不覆盖签名块** —— 否则摘要是自指的，永远填不进去。
带签名时的右边界是 `contentEnd`，不是 `文件长度 - 48`。

写入顺序建议：边写 头部 → 清单 → 载荷 边更新一个 SHA-256 上下文，写完载荷后取出
`fileSHA`，再写（可选的）签名块与尾部。这样不需要回填（seek）任何字节，打包是单向流式的。

### 3.6 Signature（可选，108 字节）

物理位置：**紧随载荷之后、尾部之前**。是否存在由头部 `flags` 的 `FlagSigned` 位决定，
且必须与清单的 `signed` 字段一致。

**定长是刻意的。** Ed25519 的签名恒为 64 字节、公钥恒为 32 字节，所以「有没有签名」
只需要 `flags` 的一个位就能判断，不必在头部再开一个长度字段 ——
那会让头部布局为一个可选特性而改动，代价是每个解析方都要跟着改。

| 偏移 | 长度 | 类型 | 字段 | 说明 |
| --- | --- | --- | --- | --- |
| 0 | 1 | `uint8` | `alg` | 签名算法。`1` = Ed25519（`SigAlgEd25519`），当前唯一定义；其它值一律拒绝 |
| 1 | 3 | `[3]byte` | `reserved` | 保留，写入时必须全 `0`，读取方忽略 |
| 4 | 8 | `[8]byte` | `keyId` | `SHA-256(publicKey)[0:8]`，展示为 **16 个小写十六进制字符** |
| 12 | 64 | `[64]byte` | `signature` | Ed25519 签名 |
| 76 | 32 | `[32]byte` | `publicKey` | Ed25519 公钥，**内嵌在包里** |

**`keyId` 必须与内嵌公钥自洽**：读取方必须验证 `keyId == hex(SHA-256(publicKey)[0:8])`，
不一致即拒绝。原因很直接：不一致意味着「这个包是谁签的」有两处互相矛盾的说法，
攻击者可以把一个**受信任的 keyId** 贴到自己的公钥前面来冒充。

**签名消息的构造。**

```
签名消息 = "PBClus/v2 signed-package\n" ‖ fileSHA
```

其中：

- `fileSHA` 是**尾部里那个值**，即 `SHA-256(文件[0:contentEnd])`。
- 前缀是**域分隔**（domain separation）：保证这个签名不会被挪用到别的协议里去，
  即使另一个协议也用 Ed25519 签一段 32 字节摘要。
- 签名块自身**不在**被签内容里（否则自指）。所以「先算 `fileSHA`、再签名、
  再写签名块」是单向的，不构成循环依赖。

**用 8 字节 keyId 而不是完整公钥摘要**：`keyId` 是给人看、给人抄的
（管理员第一次信任某个发布者时要能一眼比对），64 个字符太长。
8 字节的碰撞概率在「插件发布者数量」这个量级上可忽略。

> ⚠️ **本节只描述字节。** 签名「有效」不等于签名者「可信」——
> 公钥内嵌在包里，任何人都能签一个验证通过的包。信任语义见 §8.5 与 §12。

---

## 4. 版本与扩展策略

| 常量 | 值 | 含义 |
| --- | --- | --- |
| `MinReadVersion` | `1` | 能读取的最低版本 |
| `Version` | `2` | **写出**时使用的版本 |

规则：

1. **写出**一律用 `Version = 2`。
2. **读取**接受 `MinReadVersion ≤ version ≤ Version`，即 `1` 和 `2`。
3. `version > 2`（或 `< 1`）必须**明确拒绝**，报错要包含实际读到的版本与本程序支持的范围。
   下限设 1 而不是「忽略版本」，是因为低于 1 的号没有定义过。
4. 头部 `version` 与清单 `version` **必须一致**：不一致说明文件被拼过，拒绝。
5. 头部 `FlagSigned` 与清单 `signed` **必须一致**，同样拒绝。

**为什么 v1 → v2 必须升版本，而不是「加个可选字段」。**
v1 的解析方不认识 `platforms` 与条目 `platform`。若把多平台包交给 v1 解析方，
它会按路径把各平台的二进制**解到同一个目标路径上**，最后写入的那个胜出 ——
表现为「插件启用后立刻崩」，而错误信息里没有任何「平台」字样。
升版本让老解析方在第一步就拒绝，而不是做错事。**这正是版本字段存在的意义。**

v1 包继续支持：它的条目天然都是通用文件（无平台标签、无签名），
v2 的读法完整覆盖 v1 的语义，所以不需要保留两套解析逻辑。

**扩展策略。**

- 新增**可选**字段：优先放进头部保留区 `[120, 128)`（不破坏 CRC 语义）或清单 JSON
  （读取方容忍未知键 —— 但注意，宿主侧的**插件清单** `plugin.json` 是严格模式，
  会拒绝未知字段；PBClus 容器的清单**不是**严格模式）。
- 新增会让**老解析方做错事**的语义（多平台、签名这类）：**必须**提升 `version`。
  判断标准是「老解析方忽略它之后会不会静默产生错误结果」，而不是「格式上能不能塞进去」。

---

## 5. 路径规则

打包与解包两侧**必须调用同一个校验函数**，避免两套规则漂移。校验顺序是规范的一部分：
它决定了报错措辞，也决定了「一次目录穿越」会不会被轻描淡写成格式问题。

按顺序执行以下检查，任一失败即拒绝：

| # | 规则 | 失败原因（参考措辞要点） |
| --- | --- | --- |
| 1 | 路径非空 | 「路径为空」 |
| 2 | 字节长度 ≤ 512（按字节，不是字符数） | 「路径过长」 |
| 3 | 不含 NUL（`0x00`） | 「含 NUL 字符」 |
| 4 | 不含控制字符：任意 `r < 0x20` 或 `r == 0x7F` | 「含控制字符」 |
| 5 | 不含反斜杠 `\` | 「含反斜杠：只允许正斜杠」 |
| 6 | 不以 `/` 开头 | 「不能是绝对路径」 |
| 7 | 不含冒号 `:` | 「含冒号：不允许盘符或 NTFS 数据流」 |
| 8 | 逐个路径段（以 `/` 切分）：不得为空串 | 「含空路径段（连续斜杠或结尾斜杠）」 |
| 9 | 路径段不得为 `.` | 「含非法路径段 `.`」 |
| 10 | 路径段不得为 `..` | 「含非法路径段 `..`（不允许向上跳）」 |
| 11 | 路径段不得是 Windows 保留设备名 | 「含 Windows 保留设备名，装到 Windows 上会出问题」 |
| 12 | `path.Clean(p) == p`（规范化后必须等于原串） | 「路径未规范化：`p` 应写成 `clean`」 |

**第 8–10 条排在第 12 条之前是刻意的。** 把 `a/../x` 报成「含 `..` 路径段」比报成
「应写成 `x`」有用得多：后者等于把一次目录穿越轻描淡写成格式问题。

**第 11 条的细节。** 保留设备名集合（比较时大小写不敏感）：

```
CON  PRN  AUX  NUL
COM1 COM2 COM3 COM4 COM5 COM6 COM7 COM8 COM9
LPT1 LPT2 LPT3 LPT4 LPT5 LPT6 LPT7 LPT8 LPT9
```

比较对象是路径段中**第一个 `.` 之前**的部分，并把结尾空格去掉。原因：`NUL.txt` 在
Windows 上同样是设备，只比整个段名会漏掉这种写法。因此 `con.txt`、`dir/COM1.txt`、
`nul .dat` 都要拒绝。

**允许的写法**（示例，均通过）：

```
plugin.json
demo.exe
lib/x.so
a/b/c/d.txt
assets/图片 名字.png
dir/with-dash_and.dot
```

非 ASCII、空格、Unicode 字符都允许；规则只关心分隔符、控制字符、段语义与保留名。
条目路径与 `platform` 字段互不影响：平台标签**不参与**路径，也不出现在路径里。

**明确拒绝的写法**：

| 路径 | 原因 |
| --- | --- |
| `../escape.txt` | `..` 段 |
| `a/b/../../../x.txt` | `..` 段 |
| `/etc/passwd` | 绝对路径 |
| `C:/x` | 冒号 |
| `..\..\x.txt` | 反斜杠 + `..` 段 |
| `a//b.txt` | 空路径段 |
| `a/` | 结尾斜杠 → 空路径段 |
| `./a.txt` | `.` 段 |
| `a/./b` | `.` 段 |
| `NUL` / `con.txt` / `a/CON` | Windows 保留设备名 |
| `a\x00b` | NUL 字符 |
| `a\nb` | 控制字符 |
| 长度 > 512 字节 | 过长 |

---

## 6. 限额

| 项 | 常量 | 值 | 字节数 |
| --- | --- | --- | --- |
| 单条目解压后大小 | `MaxFileBytes` | 64 MiB | 67108864 |
| 解压总量 | `MaxExtractedBytes` | 256 MiB | 268435456 |
| 条目数 | `MaxEntries` | 2000 | — |
| 容器文件大小 | `MaxPackageBytes` | 64 MiB | 67108864 |
| 单条路径长度 | `MaxPathLen` | 512 | 512 |
| 平台标签长度 | （硬编码，无独立常量） | 40 | 40 |
| 一个包的平台数 | `MaxTargets` | 8 | — |
| 签名块长度 | `SigBlockSize` | 108 | 108 |

容器大小与宿主既有的 zip 安装路径（`MaxZipBytes` / `MaxPackageBytes`）取**同一个值**：
格式层与安装层的上限不同，会让人反复确认「到底哪个说了算」。
对插件包来说 64 MiB 很宽裕 —— Go 编译出的插件二进制通常只有几 MB。

限额的施加点：

- **打包**：单文件超限、条目数超限、累计解压总量超限、平台数超限、路径超长，均直接失败。
- **结构校验**：容器文件大小超限直接拒绝；`manifestLen` 也必须 ≤ `MaxPackageBytes`。
- **清单校验**：条目数、单条目 `size`、解压总量、平台数四项逐一检查。
- **读取**：每个条目的解压流被限制为 `MaxFileBytes + 1` 字节；解包时更紧，限制为
  `size + 1` 字节。即使清单里的 `size` 撒谎，也不可能靠一个条目撑爆内存或磁盘。
- **解包**：累计写出总量超过 `MaxExtractedBytes` 立即失败。

---

## 7. 编解码

| `codec` | 含义 | 不变量 |
| --- | --- | --- |
| `"raw"` | 载荷中就是文件原字节 | `packedSize == size` |
| `"deflate"` | 载荷中是 RFC 1951 原始 DEFLATE 流 | `packedSize` 一般 `< size`，但规范不强制 |

- `deflate` **不带** zlib 或 gzip 外层头。压缩级别由写入方决定，读取方无需知道。
- 参考打包器只有在「压缩结果严格小于原大小」时才用 `deflate`，否则**回退为 `raw`**。
  打一个已经压缩过的二进制（`.exe` / `.so` / `.png`）时 DEFLATE 经常略微变大，硬压只会
  让包更大还更慢。因此「包里有 deflate」不是必然。
- 读取方遇到未知 `codec` 必须拒绝，不得当作 `raw` 处理。
- 解压必须以「实际读出的字节数」为准，并与 `size` 比较；不一致即失败。

---

## 8. 读取方校验流程

校验分三层，职责不同。**结构校验很轻，完整校验很重** —— 打开一个 300 MiB 的包不应该
先卡一遍全文件读取。签名验证单独作为第四层，因为它依赖前三层的结果。

### 8.1 结构校验（打开）

按以下顺序执行，任一步失败即拒绝：

1. 读文件开头 8 字节做魔术字探测。
   - **探测要先于长度检查。** 一个 144 字节的 zip 改名成 `.pbclus` 时，应该告诉用户
     「这是 zip，不是 PBClus 容器」，而不是含糊的「文件太小」—— 后者会让人以为文件损坏，
     然后去反复重新下载。
   - 若不是 PBClus 魔术字：按 §11 嗅探，是 zip 就给「这是 zip，改后缀不改变格式，
     请用 `pbclus-pack` 重新打包」；否则报「魔术字不匹配」并给出前 8 字节。
2. 文件长度 ≥ `128 + 48 = 176`，且 ≤ 64 MiB。
3. 解析头部：
   1. `magic` 匹配；
   2. `1 ≤ version ≤ 2`；**更高版本必须拒绝**，并说明「更高版本可能带了本程序不认识的
      平台标签或签名，硬读会装错二进制」；
   3. `headerSize == 128`；
   4. `headerCRC == CRC-32/IEEE(Header[0:116])`。
      **CRC 必须先于其它字段被信任** —— 它保证「头部没被改坏」，之后再用头里的偏移量
      去读别的区域才有意义。
4. 布局不变量：`manifestOff == 128`；`payloadOff == manifestOff + manifestLen`；
   `0 < manifestLen ≤ 64 MiB`。
5. `contentEnd = payloadOff + payloadLen`；
   `sigLen = 108 if (flags & FlagSigned) else 0`；
   `contentEnd + sigLen + 48 == 文件实际长度`（同时挡截断、尾部追加、
   「声明有签名但没有」与「没声明却多出 108 字节」）。
6. 读取清单字节，验证 `SHA-256(清单) == manifestSHA`。
7. 解析 JSON，验证：
   1. `format == "PBClus"`；
   2. `1 ≤ version ≤ 2`；
   3. 清单 `version` 与头部 `version` 一致；
   4. `(flags & FlagSigned != 0) == (man.signed == true)`。
8. 读尾部：`tail[32:40] == magic` 且 `totalLen == 文件实际长度`。
9. 若 `FlagSigned` 置位：读 `[contentEnd, contentEnd+108)` 解析签名块 ——
   校验 `alg == 1`，校验 `keyId == hex(SHA-256(publicKey)[0:8])`。
   **这里只解析，不验证签名**：解析只是为了能告诉管理员「这个包是谁签的」（很便宜），
   验证要重算摘要、要读一遍载荷，代价与用途都不同。

**结构校验不遍历载荷，也不校验清单里的条目路径与平台。** 那些属于 8.3 与 8.2。
这是一个有意的取舍：打开包要快，而「条目是否合法」只需要在真正要解包时确认。

### 8.2 清单语义与布局校验

这一步是**安全关键**：清单里的路径、平台与偏移全部来自不可信输入，在用它们拼目标路径
或 seek 之前必须先证明它们合法。任何时候要用清单，都必须先过这一步。

按顺序执行：

1. `entryCount == len(entries)`（不一致说明清单被手工拼过）。
2. `len(entries) ≤ 2000`。
3. **平台声明自洽性**：
   1. 逐项 `platforms`：`any` 单独记下，其余必须是合法平台标签（§3.3.1）且不重复；
   2. 具体平台数量 ≤ 8；
   3. **不得同时出现 `any` 与具体平台**。
4. 逐条目：
   1. `path` 通过 §5 的全部路径规则；
   2. **`(path, platform)` 在本次清单内不重复**（唯一性键是路径与平台两元组，
      `platform` 缺失与空串等同）；
   3. 若 `platform` 非空：必须是合法平台标签；**不得**与清单的 `any` 声明共存；
      且必须出现在 `platforms` 里；
   4. `codec` 是 `raw` 或 `deflate`；
   5. `size`、`packedSize`、`offset` 均非负；
   6. `size ≤ 64 MiB`；
   7. `codec == "raw"` 时 `packedSize == size`；
   8. `offset + packedSize ≤ payloadLen`；
   9. `offset` 必须等于游标（前一条目的 `offset + packedSize`，首条为 0）——
      **条目必须紧密排列**，出现空洞或重叠说明包被手工拼过；
   10. 游标前进 `packedSize`；
   11. 累计 `size`，超过 256 MiB 即失败；
   12. `len(sha256) == 64` 且内容是合法十六进制。
5. 游标最终必须等于 `payloadLen`。
6. 清单声明了某平台，就必须**至少有一个**该平台的条目，否则包是坏的。
7. 无前缀冲突：**不存在**两个路径 `a` 与 `a/…`，即没有任何路径既是文件又是另一个
   路径的目录（例如 `lib` 与 `lib/x.so` 同时存在）。

### 8.3 完整校验

在 8.2 之上，另外做三件事：

1. `SHA-256(Payload) == payloadSHA`；
2. `SHA-256(文件[0:contentEnd]) == fileSHA`（尾部摘要）。
   **右边界是 `contentEnd`，不是 `文件长度 - 48`**：带签名时两者之间夹着签名块，
   而签名块不该被算进摘要；
3. 逐条目：解压 → 统计字节数 → `n == size` → `SHA-256(内容) == entry.sha256`。

完整校验对**安装流程是可选的**（解包时会逐条目校验），但对「先告诉用户包坏在哪，
再决定要不要解」更有用：一个 200 MiB 的包损坏时，它能给出具体是哪个文件坏了。

### 8.4 条目读取上限

无论清单怎么写，单个条目的读取流都必须被限制在 `MaxFileBytes + 1` 字节；
解包时进一步限制为 `size + 1` 字节（多读 1 字节是为了在清单把 `size` 说小时发现异常）。
这是防「解压炸弹」的关键一环：**清单是不可信输入**，长度约束不能由它单方面决定。

### 8.5 签名与信任校验

签名状态是**三个独立的事实**，实现必须分成三个字段，不要合并成一个布尔值：

| 字段 | 含义 |
| --- | --- |
| `Present` | 文件里是否有签名块（`FlagSigned` 置位） |
| `Valid` | 签名与内容是否**自洽**（**不代表可信**） |
| `Trusted` | 签名者的 `keyId` 是否在**宿主的信任库**里 |

验证步骤：

1. **先做完整校验（§8.3）。** 签名校验依赖尾部里的 `fileSHA`，而「`fileSHA` 与内容对应」
   这件事由完整校验确认。少了这一步，攻击者可以同时改内容**和** `fileSHA`，签名照样通过。
2. 构造签名消息：`"PBClus/v2 signed-package\n" ‖ fileSHA`。
3. `Ed25519-Verify(publicKey, 签名消息, signature)` → 决定 `Valid`。
4. 用 `keyId` 查宿主的信任库 → 决定 `Trusted` 与展示用的名字。

**安装决策只能用 `Trusted`，绝不能用 `Valid`。** 公钥内嵌在包里，
攻击者可以用自己的密钥签一个包并让 `Valid` 为真 —— 拿 `Valid` 当「这个包可信」
等于没做校验。这是这类设计最常见也最危险的误用，所以规范要求两个状态从一开始就分开。

**宿主没有信任库时，`Trusted` 恒为 `false`** —— 不得退化成「信任所有签名」。
空信任库 ≠ 信任所有签名。

签名策略（宿主侧）：

| 策略 | 行为 |
| --- | --- |
| `off` | 完全不看签名 |
| `warn` | 照装，但把签名状态记入告警；**这是默认值** |
| `require` | 只接受 **`Trusted`** 的包；无签名、签名无效、签名有效但未受信任，三种都拒绝 |

默认 `warn` 而不是 `require`：`require` 会让所有存量无签名插件在升级后立刻装不上，
而建立信任库需要管理员先做一步操作。**策略值写错时按 `require` 处理**（fail closed）——
配置笔误不该悄悄降低安全门槛。

---

## 9. 解包流程与安全模型

### 9.1 两道独立防线

只看路径规则不够。规则是「对字符串的白名单检查」，而真正决定文件落在哪儿的，是
**把路径拼成绝对路径之后**的结果。因此解包必须同时做两件事：

1. **格式路径规则**：每个条目路径先过 §5 的校验。挡的是畸形写法：目录穿越、绝对路径、
   反斜杠、保留设备名。
2. **解析后的前缀校验**：把条目路径转成宿主机路径语义（Windows 上把 `/` 换成 `\`），
   与目标目录拼接后，检查结果**确实落在目标目录之内**（按「目标目录 + 路径分隔符」做
   前缀比较），并再次检查转换后的相对路径不是绝对路径。

> 这两层都是**承重的**，不是装饰。参考实现用「结构完全合法、摘要全部正确、但清单恶意」
> 的容器反向验证过：把两层**同时**停用，目录穿越就会成功。单独拿掉任何一层都会留下缺口。

反过来说：测试这类实现时，不要用自己打包器产出的包去测。攻击者不会用你的打包器，
他会手工拼一个结构合法、摘要正确的包。这正是 §8.2 存在的意义。

### 9.2 逐条目写入

1. 先建目录，权限 `0o750`。
2. 写文件，权限 `0o750`。
3. 边写边算 SHA-256，同时限制读取 `size + 1` 字节。
4. 校验 `n == size` 且摘要匹配。
5. **任一不符：删除已写出的文件并立即失败**，不要留下半成品。
   半损坏的插件目录比直接失败更难排查。
6. 累计写出总量，超过 256 MiB 立即失败。

### 9.3 文件权限

PBClus **有意不携带权限位**：包不能决定宿主的文件权限。
解包统一使用 `0o750`（owner 可执行），与 zip 安装路径保持一致 —— 插件的可执行文件需要
owner 执行位。Windows 上没有 POSIX 权限位，Go 只能表达只读标志，因此那里的表现
与 zip 路径一致，不是 PBClus 特有的差异。

### 9.4 平台选择（v2）

解包可以针对某个平台执行（通常是当前平台）。规则：

1. **先做清单校验（§8.2），再判断平台支持。** 顺序有实际影响：清单里的 `platforms`
   若是畸形（比如 `linux-amd64` 少了斜杠），先判断支持会得到「不支持当前平台」——
   而真实原因是清单写错了。把畸形清单报成平台不匹配，会让人去改平台标记，越改越远。
2. 判断是否支持：`platforms` 里出现 `any` 或恰好等于目标平台，即视为支持；
   `platforms` 缺失时按 `["any"]` 处理。
3. 不支持时**必须拒绝，并给出可执行的提示**，格式是「包支持什么、当前是什么、怎么办」：

   ```
   这个插件包不支持当前平台：包内含 linux/amd64，当前是 windows/amd64。
   用 pbclus-pack pack --targets windows/amd64,linux/amd64 重新打包即可做成跨平台包
   ```

4. 支持时逐条目判定：
   - `platform` 为空（通用）→ 解出；
   - `platform` 等于目标平台 → 解出；
   - 其它平台 → **跳过**，并计入 `skipped` 计数。
5. 跳过而不是报错：一个包装了三个平台，在某台上只用得上其一是**正常情况**而非异常。
   但跳过数量必须**如实报出**（解包结果的 `skipped`），否则管理员会以为包里少了文件。

**安装器必须在解包之前调用平台检查。** 顺序错了，错误信息会变成「入口文件不存在」，
与本平台的插件目录问题混在一起，排查方向完全跑偏。

---

## 10. 打包（写入）流程与可复现性

参考实现采用「两段写」：先把每个条目压缩进临时 blob 并记录偏移与摘要，再依次写出
头部 → 清单 → 载荷 →（签名块）→ 尾部。这样打包是流式的：不需要把整个包读进内存，
也不需要回填任何字段 —— 头部需要的所有信息在写它之前都已确定。

### 10.1 收集条目

遍历源目录时：

- 跳过源目录本身。
- 相对路径转成纯正斜杠形式后立即做 §5 校验；不合法立即失败（**不静默改名**，
  因为改名会让包里的路径与用户预期不符）。
- **跳过符号链接与所有非普通文件**（设备文件、FIFO、socket）。格式里不存在这些条目，
  所以跳过而不是打包进去让解包方处理。
- 单文件超过 64 MiB 立即失败。
- 被排除规则命中的路径跳过；命中目录时整棵子树跳过。
- 目录本身不产生条目（格式不需要显式目录条目）。

**两种模式：**

| 模式 | 触发 | 条目归属 |
| --- | --- | --- |
| 多平台 | 声明了平台列表（`--targets`） | `dist/<goos>-<goarch>/**` 下的文件归属对应平台；源目录里除 `dist/` 之外的其它文件为**通用**条目；清单 `platforms` = 声明的平台列表 |
| 单平台 / 通用 | 未声明平台列表 | 全部条目用同一个平台标签；显式声明通用时清单 `platforms = ["any"]`，显式标注平台时清单 `platforms = [该平台]` |

多平台模式的约束：

- 每个声明的平台都必须有对应的目录（工具默认取 `dist/<goos>-<goarch>/`），
  目录缺失或为空即失败 —— 否则会打出一个某个平台缺二进制的包，装上去才发现。
- `dist/` 本身不作为通用目录打进去。
- 声明的平台必须是合法标签、不得为 `any`、去重后数量 ≤ 8，并按字典序排序。
- 源目录里存在 `dist/<goos>-<goarch>/` 布局却未声明平台时，工具**直接报错**而不是静默忽略：
  静默忽略会打出一个「只含 plugin.json 的包」，装上去必然失败。

单平台模式下，声明为「通用」还是「具体平台」必须显式选择，不能靠猜：
一个带原生二进制的包如果标成通用，装到别的平台上会以「入口文件不存在」失败，
而真实原因是平台不匹配。

若最终条目数为 0，打包失败：一个没有内容的包没有意义。

### 10.2 排序、去重、冲突检查

- 条目按 `(path, platform)` **字节序升序**排序（先比路径，再比平台）。
  这是可复现性的关键：同样的输入必须产生同样的载荷顺序与同样的偏移。
  平台参与排序键还让多平台包里同一路径的多个版本**相邻**，出问题时一眼能看出
  「这个文件有哪几个平台」。
- 显式检查重复条目并拒绝，唯一性键是 `(path, platform)`
  （即使遍历本身不可能产出重复，规则也不该依赖「不可能」）。
- 检查前缀冲突（§8.2 第 7 条）并拒绝。
- 条目数超过 2000 立即失败；平台数超过 8 立即失败。

### 10.3 压缩

逐条目：

1. 计算 `SHA-256(原文件内容)`。
2. 若要求 `raw`（`--store`）：直接拷进载荷，`packedSize = size`。
3. 否则先用 DEFLATE 压进内存缓冲：
   - 压缩结果**严格小于**原大小时使用它，`codec = "deflate"`；
   - 否则丢掉结果，回退为原样存，`codec = "raw"`。
     注意此时内容摘要需要重算（因为先前喂给摘要的是压缩器的读取流；参考实现会
     seek 回文件头并重置摘要）。
4. 记录 `offset`（当前载荷游标）、`packedSize`、`size`、`codec`、`sha256`，
   游标前进 `packedSize`，并累计解压总量（超过 256 MiB 立即失败）。

只要有一个条目用了 `deflate`，头部 `flags` 的 bit0 置位。

### 10.4 写出

1. 构造清单（`platforms`、`signed`、`entryCount`、`totalSize`、排序后的 `entries`），
   序列化为 JSON，计算 `manifestSHA`。
2. 组装头部：`manifestOff = 128`、`manifestLen = len(JSON)`、
   `payloadOff = 128 + manifestLen`、`payloadLen = 载荷总字节`，
   置位 `FlagDeflate` / `FlagSigned`，计算 `headerCRC`。
3. 建一个 SHA-256 上下文，依次写入 头部、清单、载荷并同步喂给该上下文。
4. 取出 `fileSHA = SHA-256(头部 ‖ 清单 ‖ 载荷)`。**签名块不在其中**：
   若本次要签名，用 `fileSHA` 构造签名消息并签出签名块，写在载荷之后、尾部之前
   （见 §10.6）。`fileSHA` 在签名之前就已确定，所以不存在循环依赖。
5. 写尾部：`fileSHA ‖ magic ‖ totalLen`，其中
   `totalLen = 128 + manifestLen + payloadLen + sigLen + 48`。
6. `fsync`（或等价操作）后再算完成。

失败时不留半成品：参考工具在打包出错时删掉输出文件。

### 10.5 可复现性

给定相同的输入目录、相同的选项与相同的工具版本，参考实现产生**字节级相同**的输出
（除去 `createdAt` 与 `tool` 字段）。要做到这一点需要：

- 条目按 `(path, platform)` 字节序排序；
- 使用同一 DEFLATE 实现与压缩级别；
- 使用同一 JSON 序列化策略（键顺序、转义）；
- 使用同一签名密钥（签名是确定性的：Ed25519 对同一消息产生同一签名）。

**跨实现不要求字节级一致**，只要求语义等价：任何符合本规范的消费者都应能读。
若你的工具需要「同样输入产生同样输出」用于比对，请自行固定上述各点。

### 10.6 签名（写入侧）

1. 按 §10.4 第 4 步算出 `fileSHA`。
2. 构造签名消息：`"PBClus/v2 signed-package\n" ‖ fileSHA`。
3. `signature = Ed25519-Sign(privateKey, 签名消息)`（64 字节）。
4. `publicKey` = 私钥对应的公钥（32 字节）；`keyId = hex(SHA-256(publicKey)[0:8])`。
5. 写 108 字节签名块：`alg=1 ‖ reserved(3 字节 0) ‖ keyId(8 字节) ‖ signature(64) ‖ publicKey(32)`。
6. 置位头部 `FlagSigned`，清单写 `"signed": true`。
7. 写尾部（`totalLen` 含这 108 字节）。

签名是**可选**的：没有签名块时 `FlagSigned` 清零、清单省略 `signed`、`totalLen` 里没有它。
不存在「有签名位但块不完整」的中间状态 —— 长度不变量会挡住。

**私钥文件的安全要求**（参考实现）：JSON 文件，权限 `0600`，
字段 `name` / `privateKey`（base64 的 64 字节）/ `publicKey`（base64 的 32 字节）/ `keyId`。
写入前必须校验「自述公钥与私钥匹配、`keyId` 与公钥匹配」：密钥文件是可以被人手改的，
而「私钥与公钥不匹配」会表现为「签出来的包验证不过」，排查时会绕一大圈
（怀疑格式、怀疑传输，就是不会怀疑密钥文件）。

---

## 11. 格式识别（魔术字嗅探）

识别依据是**内容**，不是扩展名。扩展名是给人看的提示，不是格式的判据。

| 前缀 | 判定 | 常量 |
| --- | --- | --- |
| `50 42 43 6C 75 73 1A 0A`（`PBClus\x1a\n`） | PBClus | `KindPBClus` |
| `50 4B 03 04`（`PK\x03\x04`，普通 zip） | zip | `KindZip` |
| `50 4B 05 06`（`PK\x05\x06`，空归档） | zip | `KindZip` |
| `50 4B 07 08`（`PK\x07\x08`，分卷） | zip | `KindZip` |
| 其它 | 未知 | `KindUnknown` |

嗅探只需要文件开头 8 字节，允许文件短于 8 字节（此时必然是「未知」）。

**扩展名与内容矛盾时的处理**（宿主侧策略，规范建议这样做）：
若内容是 zip 而扩展名是 `.pbclus`，报错必须明确指出「扩展名是 `.pbclus`，但内容是 zip
—— 改后缀不会改变格式，请用打包工具重新打包」，**不要**含糊地说「文件损坏」。
用户改后缀的意图很明确，报错要直接回应这个意图，否则他会以为校验没生效。

---

## 12. 安全性说明

### 12.1 完整性、签名有效、受信任是三件不同的事

| 状态 | 它证明了什么 | 它**不**证明什么 |
| --- | --- | --- |
| **完整性**（校验通过） | 内容没有被意外损坏、没有被截断 | 内容没被人动过 |
| **签名有效**（`Valid`） | 内容与签名自洽 | 签名者是谁、你要不要信任它 |
| **已受信任**（`Trusted`） | 签名者的 `keyId` 在宿主信任库里 | 签名者以后不会做坏事 |

容器里的 CRC-32 与 SHA-256 只保证**完整性**。它们**不提供任何防伪能力**：
任何人都能重新打包容器并算出全部正确的摘要。SHA-256 在这里是校验和，不是签名；
它证明「字节没变」，不证明「谁做的」。

### 12.2 攻击者可以把所有摘要同步改一遍 —— 这就是签名的意义

格式里的摘要与 CRC 全部是**公开可算**的。攻击者改完内容之后，可以把

- 头部 `headerCRC`、`manifestSHA`、`payloadSHA`，
- 清单里每个条目的 `sha256`，
- 尾部 `fileSHA` 与 `totalLen`

**全部同步重算一遍**。此时：

```
Verify()  → 通过     （完整性检查看不出任何异常）
签名校验  → 失败     （签名覆盖的是「重算之前的那个 fileSHA」）
```

这是「为什么完整性检查不够、为什么需要签名」最有力的说明。
参考实现里的 `TestSignatureCatchesWhatIntegrityCannot` 构造的就是这个场景。

结论：**「完整性检查通过」在任何时候都不能被当成「内容没被人动过」。**
（若包根本没有签名块，那就连「没被人动过」的可能性都无法判断。）

### 12.3 内嵌公钥：任何人都能签一个「验证通过」的包

签名块的公钥是**内嵌在包里**的。任何人都可以用自己的密钥签一个包，
再把对应公钥一起放进去 —— 这个包的 `Valid` 就是 `true`。

因此：

- `Valid` 只证明「内容与签名自洽」，**不证明可信**。
- 安全语义**完全来自宿主侧的信任库**（`keyId` 白名单）。只有 `Trusted` 才有意义，
  **安装决策只能用 `Trusted`**。用 `Valid` 做决策等于没做校验。
- 宿主**没有信任库时不会信任任何签名** —— 不会退化成「信任所有签名」。
- 内嵌公钥不是多余的：它让信任**能够被建立**（管理员首次信任某发布者时可以比对 `keyId`），
  也让「这个包是谁签的」可被审计。

**信任库**（宿主侧，文件名 `plugin-trust.json`，放在宿主数据目录下）：

```json
{
  "keys": [
    {
      "keyId": "3f2a9c1d4b6e7081",
      "name": "某发布者",
      "publicKey": "<base64 的 32 字节 Ed25519 公钥>",
      "addedAt": "2026-10-07T05:51:00Z"
    }
  ]
}
```

| 规则 | 说明 |
| --- | --- |
| `keyId` | 必填，16 个十六进制字符，库内不得重复（比较时大小写不敏感） |
| `name` | 展示与审计用。**库级校验不检查它**（`KeyRing.Validate` 只查 `keyId` 的形态与重复、以及 `publicKey` 的自洽），空名字的库文件会被接受；`pbclus-pack trust add` 在命令行层要求 `--name` 非空 |
| `publicKey` | 可选。**填了就必须与 `keyId` 自洽**：否则会出现「信任库里写着可信的 `keyId`、公钥却是别人的」，等于信任了一个假发布者。所以推荐填 —— 只写 `keyId` 无法防止这种替换 |
| 文件不存在 | 视为**空信任库**（空库 ≠ 信任所有签名），不是错误 |
| 文件存在但非法 | **报错**，不得静默当成空库：静默降级会让「我明明配了信任库」变成一个查不出原因的问题 |

### 12.4 因此：把包当不可信输入

校验通过意味着「这个包是完整的」，**不**意味着「这个包是安全的」；
签名有效意味着「内容与签名自洽」，**不**意味着「这个包来自你信任的人」。
安装流程必须按不可信输入处理：

- 清单里的路径、平台、偏移、长度、摘要全部先校验再用（§8.2）；
- 文件写出的位置做前缀校验（§9.1）；
- 单条目读取有硬上限（§8.4）；
- 平台检查在解包之前做（§9.4），签名策略只认 `Trusted`（§8.5）；
- 清单里的 `plugin` 元数据只用于展示，权威来源是包内的插件清单文件；
- 不要因为「校验通过」或「签名有效」就跳过路径检查或限额检查。

### 12.5 v2 明确不提供的能力

| 能力 | 状态 |
| --- | --- |
| 来源认证 | **部分提供**：签名块能证明「内容与某个密钥持有者自洽」。但公钥内嵌，任何签名者身份都无法自证，**可信与否由宿主信任库决定** |
| 信任传递 / 证书链 / 吊销 | 不提供（信任库就是一张扁平的 `keyId` 白名单；撤销 = 从库里删掉一条） |
| 签名的时间戳 / 长期有效性证明 | 不提供 |
| 加密 / 保密性 | 不提供 |
| 条目级权限位 | 不提供（有意为之，见 §9.3） |
| 符号链接等特殊条目 | 格式里不存在 |
| 增量更新 / 去重索引 | 不提供 |
| 随机访问压缩条目 | 不提供（`deflate` 条目必须从头顺序解压） |
| 一个平台多套架构变体（如通用二进制） | 不提供（按平台标签精确匹配；`any` 只能声明整包通用） |

---

## 13. 拒绝条件清单

| 条件 | 拒绝时机 |
| --- | --- |
| 文件短于 176 字节 | 结构校验 |
| 文件大于 64 MiB | 结构校验 |
| 魔术字不匹配（且不是 zip） | 结构校验 |
| 魔术字不匹配但内容是 zip | 结构校验（并给出「改后缀」提示） |
| `version < 1` 或 `version > 2` | 结构校验 |
| `headerSize != 128` | 结构校验 |
| 头部 CRC 不匹配 | 结构校验 |
| `manifestOff != 128` | 结构校验 |
| `payloadOff != manifestOff + manifestLen` | 结构校验 |
| `manifestLen == 0` 或 > 64 MiB | 结构校验 |
| `contentEnd + sigLen + 48 != 文件长度` | 结构校验（截断、尾部追加、签名块与 `FlagSigned` 不符） |
| 清单摘要不匹配 | 结构校验 |
| 清单不是合法 JSON | 结构校验 |
| 清单 `format != "PBClus"` | 结构校验 |
| 清单 `version` 不在 `1~2` | 结构校验 |
| 清单 `version` 与头部 `version` 不一致 | 结构校验 |
| 头部 `FlagSigned` 与清单 `signed` 不一致 | 结构校验 |
| 签名块长度不是 108（由长度不变量推出） | 结构校验 |
| 签名算法不是 `1`（Ed25519） | 结构校验 |
| 签名块 `keyId` 与内嵌公钥不匹配 | 结构校验 |
| 尾部 `magic` 缺失 | 结构校验 |
| 尾部 `totalLen != 文件长度` | 结构校验 |
| `entryCount != len(entries)` | 清单校验 |
| 条目数 > 2000 | 打包 / 清单校验 |
| 平台标签形状非法 | 打包 / 清单校验 |
| `platforms` 里出现重复的**具体平台** | 清单校验拒绝；`any` 重复不检查（`any` 走单独分支），打包端对重复的 `--targets` 静默去重 |
| `platforms` 同时含 `any` 与具体平台 | 打包 / 清单校验 |
| 平台数 > 8 | 打包 / 清单校验 |
| 条目 `platform` 不在清单 `platforms` 里 | 清单校验 |
| 条目标了平台但清单声明 `platforms = ["any"]` | 清单校验 |
| 声明了平台但没有任何该平台的条目 | 清单校验 |
| 源目录有 `dist/<平台>/` 布局却未声明平台 | 打包 |
| 声明的平台缺少对应目录，或目录为空 | 打包 |
| 路径不合法（§5 任一条） | 打包 / 清单校验 |
| `(路径, 平台)` 重复 | 打包 / 清单校验 |
| 路径前缀冲突 | 打包 / 清单校验 |
| 未知 `codec` | 清单校验 / 条目读取 |
| 长度或偏移为负 | 清单校验 |
| `size > 64 MiB` | 打包 / 清单校验 |
| 解压总量 > 256 MiB | 打包 / 清单校验 / 解包 |
| `raw` 但 `packedSize != size` | 清单校验 |
| `offset + packedSize > payloadLen` | 清单校验 |
| 条目偏移与游标不符（空洞或重叠） | 清单校验 |
| 摘要长度不为 64 或不是十六进制 | 清单校验 |
| 条目合计 != `payloadLen` | 清单校验 |
| 载荷摘要不匹配 | 完整校验 |
| 整文件摘要不匹配（`[0, contentEnd)`） | 完整校验 |
| 条目实际字节数 != `size` | 完整校验 / 解包 |
| 条目内容摘要不匹配 | 完整校验 / 解包 |
| 签名与内容不匹配（`Valid == false`） | 签名校验（`verify` 工具返回非 0） |
| 包不支持目标平台（`warn`/`require`/任何策略下都拒绝） | 解包前 |
| `require` 策略下无签名 / 签名无效 / 未受信任 | 安装 |
| 解析后的目标路径越出目标目录 | 解包 |
| 解包时条目路径是绝对路径 | 解包 |
| 源目录里没有可打包的文件 | 打包 |
| 源文件超过单文件上限 | 打包 |

---

## 14. 参考实现与一致性测试

- 实现：`backend/pkg/plugin/pbclus/`（`pbclus.go`、`pbclus_platform.go`、`pbclus_sign.go`）
- 测试：`backend/pkg/plugin/pbclus/*_test.go`
- 工具：`backend/cmd/pbclus-pack/`

测试的重点**不是**「正常路径能跑通」，而是「恶意容器能不能被挡住」。关键手法是构造
「结构完全合法、摘要全部正确」的容器，只让**清单内容**恶意：越界路径、重复条目、
撒谎的长度、未知 codec、空洞载荷、畸形平台标签、`any` 与具体平台混用……
这样测的才是解包侧的防线，而不是打包侧的校验。

若要验证自己的实现，至少覆盖以下用例：

1. **往返**：打包 → 打开 → 完整校验 → 解包，逐字节比对；
2. **压缩回退**：高度可压缩文件用 `deflate`，不可压缩文件回退 `raw`；
3. **格式识别**：真 zip 改名 `.pbclus` 必须被识别为 zip，且报错不含「损坏」字样；
4. **篡改检测**：分别改载荷 1 字节、改清单 1 字节、改头部 1 字节、截断、尾部追加垃圾
   —— 前两者应在结构或完整校验阶段被发现，头部篡改应由 CRC 首先报出，后两者应由
   长度不变量挡住；
5. **恶意清单**：父目录穿越、深层穿越、绝对路径、反斜杠、连续斜杠、`.` 段、结尾斜杠、
   保留设备名（含带扩展名与带目录的形式）、重复条目（同路径同平台）、
   同路径多平台（**必须通过**）、文件与目录冲突、声明大小超限、偏移越出载荷、
   未知 codec、非十六进制摘要、条目数与声明不符、条目平台不在 `platforms` 里、
   `platforms` 含 `any` 又含具体平台、声明平台却无该平台条目 —— 逐一被拒或被接受，
   且**目标目录里不许落下任何文件**；
6. **越界不落地**：在目标目录外放一个哨兵文件，确认越界路径被拒后哨兵不存在；
7. **撒谎的长度**：清单把 `size` 说小、实际负载更长，必须被发现；
8. **路径规则**：§5 的正例集合与反例集合逐条断言（含错误信息关键词）；
9. **符号链接**：源目录里的符号链接不出现在包里；
10. **平台解包**：多平台包在平台 A 上只解出「通用 + A」，`skipped` 等于其它平台的条目数，
    且其它平台的二进制**没有**落在目标目录里；
11. **平台拒绝**：不支持当前平台时，错误信息里同时包含包内平台、当前平台与重打包建议；
12. **签名往返**：签名包 → 打开 → `Present`/`Valid`/`Trusted` 三个状态各自正确；
13. **签名抓得住完整性抓不住的东西**：改内容并把所有摘要同步重算，`Verify()` 通过而
    签名失败（对应 `TestSignatureCatchesWhatIntegrityCannot`）；
14. **keyId 自洽**：把签名块里的 `keyId` 改成另一个值（哪怕那个值在信任库里），
    必须因「与内嵌公钥不匹配」被拒；
15. **空信任库不信任任何签名**：不提供信任库时，一个自洽的签名包 `Valid == true`
    但 `Trusted == false`，`require` 策略下必须拒绝安装。

---

## 附录 A · 常量速查

```
Extension          = ".pbclus"
FormatName         = "PBClus"
Version            = 2                   // 写出
MinReadVersion     = 1                   // 读取下限
HeaderSize         = 128
FooterSize         = 48
SigBlockSize       = 108
MaxPathLen         = 512                 // 字节
MaxEntries         = 2000
MaxFileBytes       = 64 << 20            // 67108864
MaxExtractedBytes  = 256 << 20           // 268435456
MaxPackageBytes    = 64 << 20            // 67108864
MaxTargets         = 8

Magic              = 50 42 43 6C 75 73 1A 0A      // "PBClus\x1a\n"

CodecRaw           = "raw"
CodecDeflate       = "deflate"

FlagDeflate        = 1 << 0              // flags bit0
FlagSigned         = 1 << 1              // flags bit1

PlatformAny        = "any"
DistDirName        = "dist"              // 源码目录里多平台布局的顶层目录

SigAlgEd25519      = 1
KeyIDLen           = 16                  // keyId 的十六进制字符数
sigDomain          = "PBClus/v2 signed-package\n"

KindUnknown        = 0
KindPBClus         = 1
KindZip            = 2
```

平台标签相关函数（语义）：

```
HostPlatform()                 -> "<goos>/<goarch>"           // 当前运行平台
ValidatePlatform(p)            -> ok | error                  // 形状校验，不做白名单
DirName("linux/amd64")         -> "linux-amd64"               // 平台 → 目录名
PlatformFromDirName("linux-amd64") -> "linux/amd64", ok       // 目录名 → 平台
Platforms()                    -> 排序后的平台列表（缺失时 ["any"]）
Supports(platform)             -> bool                        // 声明式判断，不需解包
```

## 附录 B · 校验伪代码

```
function open(path):
    head8 = read(path, 0, 8)
    if head8 != Magic:
        if sniff(head8) == ZIP:
            fail("这是 zip，不是 PBClus 容器（改后缀不会改变格式）")
        fail("魔术字不匹配")

    size = filesize(path)
    if size < 176 or size > 64Mi: fail("长度异常")

    h = parse_header(read(path, 0, 128))
        # 依次检查：magic / 1 <= version <= 2 / headerSize == 128 / headerCRC
        # version > 2 必须拒绝：更高版本可能带了本程序不认识的平台标签或签名
    if h.manifestOff != 128: fail("清单偏移异常")
    if h.payloadOff != h.manifestOff + h.manifestLen: fail("载荷偏移与清单长度不一致")
    if h.manifestLen == 0 or h.manifestLen > 64Mi: fail("清单长度异常")

    contentEnd = h.payloadOff + h.payloadLen
    sigLen     = 108 if (h.flags & FlagSigned) else 0
    if contentEnd + sigLen + 48 != size: fail("长度不符：截断或被追加")

    man = read(path, h.manifestOff, h.manifestLen)
    if sha256(man) != h.manifestSHA: fail("清单摘要不匹配")
    m = json_parse(man)
    if m.format != "PBClus": fail("清单标识不支持")
    if m.version < 1 or m.version > 2: fail("清单版本不支持")
    if m.version != h.version: fail("头部版本与清单版本不一致")
    if (m.signed == true) != (sigLen == 108): fail("头部签名标记与清单 signed 不一致")

    foot = read(path, size - 48, 48)
    if foot[32:40] != Magic: fail("尾部标记缺失")
    if u64le(foot[40:48]) != size: fail("尾部总长不符")

    sig = null
    if sigLen == 108:
        sig = parse_signature_block(read(path, contentEnd, 108))
            # alg 必须是 1（Ed25519）；keyId 必须等于 hex(sha256(publicKey)[0:8])
        # 只解析，不验证：验证要重算摘要、要读一遍载荷

    return {size, h, m, contentEnd, sig}    # 结构校验到此为止，尚未看载荷与条目路径


function verify_layout(rd):
    if rd.m.entryCount != len(rd.m.entries): fail("条目数与声明不符")
    if len(rd.m.entries) > 2000: fail("条目数超限")

    declared = {}; any_declared = false
    for p in (rd.m.platforms or []):
        if p == "any": any_declared = true; continue
        validate_platform(p)                     # <goos>/<goarch> 形状
        if p in declared: fail("清单 platforms 里 " + p + " 重复")
        declared.add(p)
    if len(declared) > 8: fail("平台数超限")
    if any_declared and len(declared) > 0: fail("platforms 不能同时声明 any 与具体平台")

    seen = {}; used = {}; paths = []; total = 0; cursor = 0
    for e in rd.m.entries:
        validate_path(e.path)                    # §5 全部规则
        key = (e.platform or "") + "\0" + e.path # 唯一性按 (路径, 平台)
        if key in seen: fail("同平台下条目重复: " + e.path)
        seen.add(key); paths.append(e.path)
        if e.platform:
            validate_platform(e.platform)
            if any_declared: fail("条目标了平台但清单声明 any")
            if e.platform not in declared: fail("条目平台不在清单 platforms 里")
            used.add(e.platform)
        if e.codec not in {"raw","deflate"}: fail("未知压缩方式")
        if e.size < 0 or e.packedSize < 0 or e.offset < 0: fail("负数")
        if e.size > 64Mi: fail("超过单文件上限")
        if e.codec == "raw" and e.packedSize != e.size: fail("raw 但长度不等")
        if e.offset + e.packedSize > rd.h.payloadLen: fail("偏移超出载荷")
        if e.offset != cursor: fail("载荷有空洞或重叠")
        cursor += e.packedSize
        total  += e.size
        if total > 256Mi: fail("解压总量超限")
        if len(e.sha256) != 64 or not is_hex(e.sha256): fail("摘要格式错误")
    if cursor != rd.h.payloadLen: fail("条目合计与载荷长度不一致")
    for p in declared:
        if p not in used: fail("声明支持平台 " + p + "，但没有任何条目属于它")
    check_prefix_conflicts(paths)


function verify(rd):
    verify_layout(rd)
    if sha256(payload) != rd.h.payloadSHA: fail("载荷摘要不匹配")
    if sha256(file[0:rd.contentEnd]) != footer.fileSHA: fail("整文件摘要不匹配")
        # 右边界是 contentEnd：签名块不在被签内容里
    for e in rd.m.entries:
        data = decompress(read(payload, e.offset, e.packedSize), e.codec,
                          limit = 64Mi + 1)
        if len(data) != e.size: fail("实际长度与清单不符")
        if sha256(data) != e.sha256: fail("内容摘要不匹配")


function signature_status(rd, trust_store):
    if rd.sig == null: return {present: false, valid: false, trusted: false}
    key_id   = rd.sig.keyId
    name, ok = trust_store.lookup(key_id) if trust_store else ("", false)
    msg      = "PBClus/v2 signed-package\n" + footer.fileSHA
    valid    = ed25519_verify(rd.sig.publicKey, msg, rd.sig.signature)
    # ⚠ valid 只证明内容与签名自洽；可信与否完全由 trust_store 决定。
    #   安装决策只能用 trusted —— 用 valid 做决策等于没做校验。
    return {present: true, keyId: key_id, valid: valid, trusted: ok, keyName: name}


function supports(rd, platform):
    for p in (rd.m.platforms or ["any"]):
        if p == "any" or p == platform: return true
    return false


function extract(rd, dst, platform):          # platform 形如 "windows/amd64"
    verify_layout(rd)                         # 先校验清单，再判断平台
    if not supports(rd, platform):
        fail("不支持当前平台：包内含 " + join(declared, "、") +
             "，当前是 " + platform + "。用 --targets " + platform +
             "," + other_common_platform(platform) + " 重新打包")
        # 必须在解包之前判定：否则错误会变成「入口文件不存在」，方向完全跑偏
    dst = absolute(dst)
    skipped = 0
    for e in rd.m.entries:
        if e.platform and e.platform != platform:
            skipped += 1                      # 别的平台：跳过，不报错
            continue
        rel    = host_path(e.path)            # 仅做分隔符转换
        target = join(dst, rel)
        if not target.startswith(dst + SEP): fail("解析后越出目标目录")
        if is_absolute(rel): fail("绝对路径")
        mkdirs(dirname(target), 0o750)
        data = decompress(read(payload, e.offset, e.packedSize), e.codec,
                          limit = e.size + 1)
        if len(data) != e.size: remove(target); fail("实际长度与清单不符")
        if sha256(data) != e.sha256: remove(target); fail("内容摘要不匹配")
        write(target, data, 0o750)
    return {files: ..., skipped: skipped}     # skipped 要如实报给调用方
```
