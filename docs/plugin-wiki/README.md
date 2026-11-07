# NebulaDrive 插件开发 Wiki

> 适用版本：**V26-10.0-d** 及之后
> 读者：想给 NebulaDrive 写**第三方插件**的开发者，以及需要管理插件的管理员
> 本 Wiki 所有结论均从源码（`backend/pkg/plugin/**`、`backend/controllers/plugins.go`、`examples/plugins/**`）逐行核对得出。**凡与代码不符之处，一律以代码为准。**

---

## 这是什么

NebulaDrive 的插件系统让你**不修改、不重编译主服务**就能扩展它的能力：防盗链、HMAC 鉴权、SMTP 发信、备份镜像、协作跳转……都可以做成独立插件，热插拔、可独立崩溃。

插件以**独立子进程**运行，通过 stdin/stdout 上的 **JSON-RPC 2.0** 与主服务（宿主）通信。宿主在固定的「钩子（hook）」点调用插件，插件读 `ctx`、写回 `ctxPatch`、必要时用含 `blocked` 的 error 拦截请求。

## 本 Wiki 的结构

| 页面 | 内容 |
|---|---|
| [01-架构](01-architecture.md) | 子进程模型、两种插件形态、sync/async 派发、生命周期与隔离 |
| [02-清单 plugin.json](02-manifest.md) | `plugin.json` 全部字段、校验规则、合法/非法示例 |
| [03-协议 JSON-RPC](03-protocol.md) | `init`/`fire`/`shutdown`/`ping`/`log`、参数与结果、`blocked` 语义、超时与日志铁律 |
| [04-钩子清单](04-hooks.md) | 全部 10 个钩子的字段表、模式、是否可拦截、是否真正接线 |
| [05-签名与信任](05-signing-trust.md) | PBClus 包格式、签名状态（present/valid/trusted）、信任库、三种策略 |
| [06-上手教程](06-tutorial.md) | 从零写一个插件：脚手架 → 构建 → 安装 → 配置 → 调试 |
| [07-官方示例](07-examples.md) | 仓库内 7 个示例插件的功能与钩子一览 |
| [08-管理端与 API](08-admin.md) | 安装/启用/配置接口、插件协议关卡、信任管理接口 |

## 与其他文档的关系

- 本 Wiki 是**开发者入口**，偏「怎么写、怎么接」。
- [../PBClus-格式规范.md](../PBClus-格式规范.md) 是 PBClus 包格式（v2）的**逐字节协议规范**，签发/校验/尾部布局看这里。
- [../插件制作指南.html](../插件制作指南.html) 是面向发布者的制作指引（打包、签名、上架商店）。
- [../插件系统完全指南.md](../插件系统完全指南.md) 是早期的内部总览，部分章节（如「预留未接入的钩子」）已过时——**以本 Wiki 与代码为准**。

## 一句话上手

```bash
cp -r examples/plugins/referer-guard myplugin
cd myplugin && go build -o myplugin .
# 然后把目录交给管理端「本地安装」，启用、填配置、看日志
```

不放心？直接读 [06-上手教程](06-tutorial.md)。
