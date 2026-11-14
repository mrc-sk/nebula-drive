# 登录爆破抑制

同一 IP / 账号短时间连续登录失败后临时锁定，抑制暴力破解。

| 项目 | 值 |
| --- | --- |
| 插件名 | `nd-login-brake` |
| 版本 | 1.0.0 |
| 作者 | NebulaDrive Official |
| 协议 | AGPL-3.0-only |
| 订阅钩子 | `onAuthBeforeLogin`, `onAuthLoginFailed`, `onAuthLoginSuccess` |

## 功能

在 `onAuthLoginFailed` 计数、`onAuthBeforeLogin` 决策拦截、
`onAuthLoginSuccess` 清零。默认 60 秒内失败 5 次锁定 300 秒。
计数保存在插件进程内存中（重启即清零），属于轻量级第一道防线，
不替代网关层限流。

## 配置

安装后在后台「插件 → 配置」里填写 JSON（对应 `plugins.config`）。**不填任何配置时插件保持宽松/关闭状态，不会一装上就改变站点行为。

```json
{"windowSeconds": 60, "maxFailures": 5, "banSeconds": 300}
```

## 启用前须知

本插件以子进程方式运行，拥有宿主同等的文件与网络权限。请先在测试环境验证，确认策略符合预期后再在生产环境启用。

官方插件使用 Ed25519 签名，站点信任库中已内置官方公钥；安装时会在后台显示「已由受信任的发布者签名」。
