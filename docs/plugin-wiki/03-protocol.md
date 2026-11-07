# 03 · 协议 JSON-RPC

> 源码：`backend/pkg/plugin/manifest.go`（`RPC*` 常量与结构）、`backend/pkg/plugin/host/manager.go`
> 协议极简：**一行一个 JSON 对象，UTF-8，不做跨行分帧**。插件日志走 stderr，绝不污染 stdout。

## 协议版本

`ProtocolVersion = 1`。清单 `protocolVersion` 必须与之相等，否则宿主拒载。

## 方法（method）

| 方法 | 方向 | 作用 |
|---|---|---|
| `init` | 宿主→插件 | 启动后插件**必须主动等待**这个请求才能工作。插件回 `ok/hooks/version` |
| `fire` | 宿主→插件 | 一次钩子调用。参数 `{hook, ctx, seq}` |
| `shutdown` | 宿主→插件 | 插件收到后应清理资源并**退出进程** |
| `ping` | 宿主→插件 | 健康检查，插件应立即响应（如 `{"pong":true}`） |
| `log` | 插件→宿主 | 通知：把一条日志发给宿主统一落库/转发。可选，更常见是写 stderr |

## init 参数与结果

```jsonc
// 宿主下发（init 的 params）
{
  "hostVersion": "V26-10.0-d",
  "hostProtocol": 1,
  "pluginName": "referer-guard",   // = manifest.name
  "dataDir": "/var/lib/nebula/plugins/referer-guard", // 已创建，可写；持久化放这里
  "config": { ... },               // 管理员填的配置（JSON 字符串反序列化后的对象）
  "startTimeoutMillis": 10000,
  "siteUrl": "https://disk.example.com"  // 可选，插件需要回调宿主时可用
}

// 插件返回（init 的 result）
{ "ok": true, "name": "referer-guard", "version": "1.0.0", "hooks": ["onAntiLeech"] }
// ok=false 时宿主会卸载并记录 error
```

> 插件**不要**去写宿主的数据目录，只写 `dataDir`。`config` 来是 `plugins.config`（JSON 字符串），安装后由管理员填写。

## fire 参数与结果

```jsonc
// 宿主下发（fire 的 params）
{ "hook": "onAntiLeech", "ctx": { "userId": 1, "ip": "1.2.3.4", "referer": "https://a.com", "fileName": "x.zip" }, "seq": 7 }

// 插件返回（fire 的 result）
{
  "ctxPatch": { "refererGuard": "allowed" },  // 浅层合并，顶层键覆盖；不能删宿主已有键（要删请显式置 null）
  "error": "blocked by referer-guard: 外站访问"  // 含 "blocked" → 宿主把它转成可拦截请求的 error
}
```

**致命细节**：`FireResult` **没有 `Blocked` 布尔字段**。拦截**只能**通过 `error` 文本包含子串 `blocked`（区分大小写）来表达。这与进程内插件的既有约定一致，两种形态拦截语义完全相同。

## 三条铁律（写插件必读）

1. **stdout 专供 RPC，一行一个 JSON。** 日志一律写 **stderr**。混用会破坏分帧，插件直接失联。
2. **唯一输出通道是 `ctxPatch`（浅层合并，顶层键覆盖）。** `result` 里没有「设置 ctx 字段」的语义，想改 ctx 就写 `ctxPatch`。
3. **拦截用 error，且文本必须含小写 `blocked`。**
   - `{"error": "blocked by x: 外网 IP"}` → 403 ✅
   - `{"ctxPatch": {"blocked": true}}` → **不会**拦截 ❌

## 超时与兜底

| 常量 | 值 | 含义 |
|---|---|---|
| `DefaultStartTimeout` | 10s | 启动 + init 往返预算 |
| `DefaultFireTimeout` | 2s | 单次 fire 同步往返超时 |
| 单条日志 | ≤2000 字符 | 超出截断（按 rune 截断，不切坏 UTF-8） |
| 一次响应日志行 | ≤50 条 | 超出丢弃 |

宿主对超时/崩溃做隔离与自动重启（见 [01-架构](01-architecture.md)）。插件侧应尽量快：把重活异步化，别在 `fire` 里同步做网络 IO 又卡满 2s。

## 最小可运行插件骨架（Go）

```go
package main

import ("bufio"; "encoding/json"; "os")

func main() {
    sc := bufio.NewScanner(os.Stdin)
    for sc.Scan() {
        var req struct {
            ID int64 `json:"id"`; Method string `json:"method"`; Params json.RawMessage `json:"params"`
        }
        if json.Unmarshal(sc.Bytes(), &req) != nil { continue }
        switch req.Method {
        case "init":
            reply(req, map[string]any{"ok": true, "hooks": []string{"onAntiLeech"}}, nil)
        case "fire":
            reply(req, map[string]any{"ctxPatch": map[string]any{"ok": true}}, nil)
        case "shutdown":
            reply(req, map[string]any{"bye": true}, nil); return
        }
    }
}
func reply(req struct{ID int64 `json:"id"`}, result any, rerr any) { /* 序列化 + 写 stdout + '\n' */ }
```

> 插件**不 import 宿主包**：宿主与插件是独立进程，import 宿主会把整个后端拖进依赖图并造成版本耦合。协议结构在插件侧独立声明即可（参考 `examples/plugins/*/main.go` 顶部的「协议结构」段）。
