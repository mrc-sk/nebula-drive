# 02 · 清单 plugin.json

> 源码：`backend/pkg/plugin/manifest.go`（`ParseManifest` / `Validate`）
> 每个插件目录根下**必须**有一个 `plugin.json`。宿主加载时解析并逐字段校验；任何一项不合法直接拒载，错误信息含具体字段名，可直接展示给管理员。

## 全部字段

| 字段 | 类型 | 必需 | 说明 / 校验 |
|---|---|---|---|
| `name` | string | ✅ | 唯一标识。`[a-z0-9-_]`，长度 2–64；不能以 `-`/`_` 开头；不能是 Windows 保留名（`CON`/`NUL`/`COM1`…）。也是数据目录下的子目录名 |
| `title` | string | | 展示名，≤128 字符；为空回退到 `name` |
| `version` | string | | 语义化版本 `x.y.z`（三段式，每段 ≤5 位数字）。空表示不限 |
| `author` | string | | 作者名，≤64 |
| `description` | string | | 一句话描述，≤512 |
| `entry` | string | ✅ | 可执行文件名。**必须是单个文件名，不能含 `/`、`\`、`..` 或 `:*?"<>|`**。不带 `.exe` 即可跨平台通用（Windows 上自动补 `.exe`） |
| `args` | []string | | 传给可执行文件的参数，每条 ≤1024，禁止 NUL |
| `env` | map | | 附加环境变量，**会覆盖**宿主同名变量——故不用于传敏感配置 |
| `protocolVersion` | int | ✅ | **必须等于 `1`**，否则拒载 |
| `hooks` | []string | ✅ | 声明本插件实现哪些钩子；不声明就不会被调用（避免被误用于没实现的钩子）。最多 32 个；必须是已知钩子名且不能重复 |
| `permissions` | []string | | 权限声明，**仅供管理员安装前参考，宿主不强制、不校验**（每条 ≤64） |
| `license` | string | | SPDX 标识，≤64；空值默认 `AGPL-3.0-only` |
| `homepage` | string | | 项目地址，≤256 |
| `minHostVersion` | string | | 最低宿主版本（`x.y.z`），空表示不限 |

**全局限制**：清单文件 ≤ 64 KB；未知字段会报错（`json.Decoder.DisallowUnknownFields`，拼错字段名不再静默生效）；字段长度超限、版本非法都会被精确拦截。

## 合法示例

```json
{
  "name": "referer-guard",
  "title": "防盗链守卫",
  "version": "1.0.0",
  "author": "NebulaDrive",
  "description": "下载前校验 Referer，不在白名单内返回 403",
  "entry": "referer-guard",
  "args": [],
  "protocolVersion": 1,
  "hooks": ["onAntiLeech"],
  "permissions": ["read-download-metadata", "write-own-datadir"],
  "license": "AGPL-3.0-only"
}
```

## 常见校验失败

- `name` 含大写 / 中文 → `name 含非法字符`（只允许小写字母、数字、`-`、`_`）
- `entry` 写成 `bin/myplugin` 或 `..\x` → `entry 必须是单个文件名，不能含路径分隔符或 ..`（这是安全红线，见下）
- `protocolVersion` 写成 `2` 或漏写 → `protocolVersion 不匹配：插件声明 2，宿主要求 1`
- `hooks` 写错成 `onAntileech`（大小写错） → `未知钩子 "onAntileech"`（已知钩子见 [04-钩子](04-hooks.md)）

## 关于 `entry` 的硬约束

> `entry` 会拼成 `<插件目录>/<entry>` 再交给 `exec`。若允许路径分隔符，manifest 就能让宿主执行插件目录外的任意文件——等于任意代码执行。所以不仅 `Validate` 挡，运行时 `ExecPath` 还会二次校验解析后路径确实落在插件目录内。

跨平台写法：清单里写 `"entry": "myplugin"`（不带后缀），Windows 上 `ExecPath` 会自动尝试 `myplugin.exe`；清单显式写了 `.exe` 但文件不存在时**不会**静默补（那是真缺文件）。
