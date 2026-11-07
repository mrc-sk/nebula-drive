# 06 · 上手教程

> 跟着做一遍，你就能拥有一个可运行的插件。完整可参考 `examples/plugins/referer-guard/`。

## 目标

写一个最小防盗链插件：在 `onAntiLeech` 里，Referer 不在白名单就返回含 `blocked` 的 error（→ 403）。

## 步骤 1 · 建目录与清单

```bash
mkdir myplugin && cd myplugin
```

`plugin.json`：

```json
{
  "name": "myplugin",
  "title": "我的第一个插件",
  "version": "1.0.0",
  "author": "你",
  "description": "Referer 在白名单外就拦截",
  "entry": "myplugin",
  "protocolVersion": 1,
  "hooks": ["onAntiLeech"],
  "permissions": ["read-download-metadata"]
}
```

## 步骤 2 · 写 main.go

```go
package main

import (
    "bufio"; "encoding/json"; "fmt"; "os"; "strings"
)

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
            var p struct {
                Hook string `json:"hook"`; Ctx map[string]any `json:"ctx"`
            }
            json.Unmarshal(req.Params, &p)
            allow := false
            if ref, _ := p.Ctx["referer"].(string); strings.HasPrefix(ref, "https://trusted.example.com") {
                allow = true
            }
            res := map[string]any{}
            if !allow {
                res["error"] = "blocked by myplugin: 外站 Referer " + fmt.Sprint(p.Ctx["referer"])
            }
            reply(req, res, nil)
        case "shutdown":
            reply(req, map[string]any{"bye": true}, nil); return
        }
    }
}

func reply(req struct{ ID int64 `json:"id"` }, result any, rerr any) {
    m := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
    b, _ := json.Marshal(m)
    os.Stdout.Write(append(b, '\n'))
}
```

> 真实项目里把协议结构（`request`/`response`/`initParams`/`fireParams`/`fireResult`）单独声明并复用，别每次手写——`examples/plugins/*/main.go` 顶部有可直接抄的版本。

## 步骤 3 · 构建

```bash
go build -o myplugin .          # Windows: -o myplugin.exe .
```

不限于 Go：**任何语言**能读写 stdin/stdout 的 JSON 行即可。只要实现了 `init`/`fire`/`shutdown` 循环，并遵守 [03-协议](03-protocol.md) 的三条铁律。

## 步骤 4 · 安装

两种方式：

- **本地目录**（开发最方便）：管理端「插件 → 安装」，选 `source=local`，`path` 填该目录绝对路径；或直接 `POST /api/admin/plugins/install {"source":"local","path":"/abs/myplugin"}`。
- **URL / 商店**：`source=url` 填 zip 地址（仅允许 http/https）。

> **安装不会自动启用**——必须显式点「启用」。这是刻意的：否则「阅读并同意插件协议」那一步形同虚设。首次进插件页会弹协议，必须**滚动到底**才能勾选，且后端 `requireAgreement` 强制（未同意时安装/启用接口直接 403，只靠前端弹窗可被 API 绕过）。

## 步骤 5 · 启用 + 配置

启用后插件进程被拉起。`onAntiLeech` 不需要配置即可工作；如果需要配置，在插件页（或 `PUT /api/admin/plugins/:name/config`）填 JSON，重启后生效。**配置不要放敏感值到 `env`**（env 会覆盖宿主变量）。

## 步骤 6 · 看日志 / 调试

- 插件日志：`GET /api/admin/plugins/logs?name=myplugin&limit=200`（每插件 500 条环形缓冲）。
- 协议关卡失败、init 返回 `ok=false`、启动超 10s、单次 fire 超 2s、崩溃 5 次 → 都会在列表的 `status`/`error` 字段暴露。
- **经典坑**：`entry` 写成带路径的、`protocolVersion` 写错、hooks 拼错大小写 → 直接拒载，看列表错误信息即可定位。
- 进程外插件崩溃只杀子进程，宿主会指数退避自动重启（最长 5 分钟）。

## 进程内插件（内置功能）

改动要重编译主服务，适合内置能力，**不适合第三方**。写法：

```go
package plugin // backend/pkg/plugin/custom/ 下

func init() {
    Register(HookAntiLeech, func(ctx map[string]any) (map[string]any, error) {
        defer func() { recover() }() // 必须，进程内 panic 会炸请求
        return nil, nil
    })
}
```

注意：`ctx` 字段必须带类型断言（`ctx["userId"].(uint)` 实际是 `float64` 会 panic）；不要在 handler 里调 `Register`（旧实现持锁会死锁）；`ctx` 被同钩子所有 handler 共享，恶意/有 bug 的插件可篡改后续所见数据。
