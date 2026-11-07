# 01 · 架构

> 源码：`backend/pkg/plugin/hooks.go`、`backend/pkg/plugin/host/manager.go`、`backend/pkg/plugin/host/proc_{windows,unix}.go`

## 为什么是子进程，而不是 `plugin.Open` 或 WASM

| 方案 | 结论 |
|---|---|
| Go 原生 `plugin.Open` | ❌ 只支持 Linux/macOS 且需要 cgo；Windows 根本编译不了，且工具链/依赖版本一升级就可能让已构建的插件失效 |
| WASM（wasmtime-go） | ❌ 需要 CGO，Go host function 绑定成本过高 |
| **独立子进程 + JSON-RPC over stdio** | ✅ 跨平台、语言无关（插件可以用任何语言写）、崩溃隔离、真正热插拔 |

代价：每次调用多一次本地管道 IPC 往返（亚毫秒级）。

## 两种插件形态

业务代码对形态**完全透明**——只调 `plugin.Fire(hook, ctx)`，不关心 handler 在哪。

| 形态 | 加载方式 | 适用 |
|---|---|---|
| **进程外（主流）** | 独立可执行文件，宿主拉起并通过 RPC 调用 | 第三方扩展，可热插拔 |
| **进程内** | 宿主二进制里 `init()` 调 `plugin.Register` | 内置功能，零 IPC 开销 |
| 混合 | 两者同时存在，`Fire` 会**先跑完进程内、再派发进程外** | 常见：内置补核心能力，外部做扩展 |

进程内插件写法见 [06-教程 §进程内插件](06-tutorial.md#进程内插件内置功能)；它改动要重编译主服务，不适合第三方。

## 派发模式：sync / async

每个钩子在 `Hooks` 表里标注 `Mode`：

| 模式 | 行为 | 适用 |
|---|---|---|
| `ModeSync` | 阻塞等插件返回（超时 2s） | 业务要读结果才能决策：`onAntiLeech`（下载拦截）、`onCollabOpen`（302）、`onApiAuth`、`onCLI` |
| `ModeAsync` | 每插件一个 goroutine，**结果不回传** | 纯事件通知：邮件、审计、协作保存、备份/恢复事件、DB 迁移、限流事件 |

`ModeAsync` 不回写 `ctx`：业务已经继续往下走了，回写只会造成竞态。需要回传请用 `ModeSync`，或用 `plugin.RateLimitGate()` 强制同步（见 [04-钩子 · onRateLimit](04-hooks.md#onratelimit-的特殊处理)）。

## 生命周期与隔离

| 能力 | 状态 |
|---|---|
| 安装即用，不改源码、不重编译主服务 | ✅ |
| 启用 = 拉起进程；禁用 = 杀进程；**主服务不重启** | ✅ |
| 崩溃隔离（崩溃只杀子进程，不影响宿主） | ✅ |
| 崩溃自动重启（指数退避，最长 5 分钟） | ✅ |
| 连续 5 次崩溃后停止并转人工 | ✅ |
| 日志缓冲（每插件 500 条环形） | ✅ |
| 进程组隔离：Windows `taskkill /T /F`；Unix `kill(-pgid)` | ✅（避免孙进程变孤儿） |

## 信任边界（重要）

插件与主服务**同权等系统权限，没有沙箱**。隔离靠的是「进程边界 + 权限自觉」，不是容器/沙箱。对第三方插件，安全保证来自 [PBClus 签名 + 信任库](05-signing-trust.md)，而非操作系统隔离。详见 [05-安全与诚实边界](05-signing-trust.md#安全与诚实边界)。
