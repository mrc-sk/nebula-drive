# 智能 AI 工单站使用说明（安全版）

> 本文对应 2026-11-08 上线的**安全版**。与初版最大的区别：公开页面**不再有**「管理后台 / AI 配置」入口，配置也不提供任何 Web 读写接口。

## 一、站点是什么

一个轻量的技术支持工单系统，随本次部署一起上线，用于收集网盘使用问题并**由本地小模型结合知识库自动给出初步答复**。

| 项目 | 值 |
| --- | --- |
| 公开访问 | `http://<服务器IP>:8081/` |
| 技术实现 | Python 3 标准库（`http.server` + `sqlite3`），无 Web 框架 |
| 数据存放 | `/opt/ai-desk/data/desk.db`（SQLite） |
| 配置文件 | `/opt/ai-desk/config.json`（权限 `600`，仅服务器本机可改） |
| 服务 | `ai-desk.service`，开机自启 |

> 提交后请**记下工单号**（形如 `ND-20261108-AB12CD`），它是查询凭证。请勿在工单里填写密码。

## 二、公开页面只有两个页签

| 页签 | 用途 |
| --- | --- |
| **提交工单** | 填写分类、标题、描述、联系方式，提交后立即显示智能答复与工单号 |
| **查询工单** | 凭工单号查看问题描述、智能答复与管理员后续回复 |

初版里的「管理后台」「AI 配置」两个页签已**全部下线**。以下路径对外统一返回 `404`，且不会暴露"是否存在该页面"：

```
/api/config          /api/admin/config
/admin               /console
```

管理类 API（`/api/admin/tickets`）只认请求头 `X-Admin-Token`，无令牌一律 `404`——令牌不会出现在 URL、日志或 Referer 里。

## 三、管理控制台：隐藏路径 + 令牌

管理控制台挂在一个随机隐藏路径下，必须同时满足「路径正确」和「令牌正确」才返回页面，否则同样是 `404`。

**只在服务器本机查看地址**，不要把带令牌的链接发到聊天、文档或工单里：

```bash
python3 /opt/ai-desk/deskctl.py url
```

输出形如 `http://127.0.0.1:8081/console-<随机>?token=<令牌>`。若需要从自己的电脑访问，先建 SSH 隧道：

```bash
ssh -L 8081:127.0.0.1:8081 root@<服务器>
```

控制台里可以查看全部工单、改状态、追加人工回复。

## 四、配置只走本机命令行

**站点没有任何 Web 配置页，也没有配置写入 API。** 所有配置由服务器本机的 `deskctl.py` 维护，改完下一次请求自动生效（无需重启）：

| 命令 | 作用 |
| --- | --- |
| `python3 deskctl.py url` | 显示管理控制台地址（含令牌） |
| `python3 deskctl.py config` | 查看 AI 配置（API Key 脱敏） |
| `python3 deskctl.py status` | 服务、AI 模式、网关地址、工单总数总览 |
| `python3 deskctl.py set-ai --enable` | 启用本地模型 |
| `python3 deskctl.py set-ai --disable` | 关闭模型，回到纯知识库模式 |
| `python3 deskctl.py set-ai --model <名称>` | 换模型名 |
| `python3 deskctl.py set-ai --base-url <URL>` | 换 OpenAI 兼容网关 |
| `python3 deskctl.py test` | 测试模型网关连通性 |
| `python3 deskctl.py tickets` | 列出最近 30 条工单 |

> 配置文件为 `600` 权限，仅 root 可读。轮换 `admin_token` 或 `admin_path` 需直接编辑 `config.json` 并 `systemctl restart ai-desk`。

## 五、智能答复怎么产生的

### 当前模式：本地小模型 + 知识库增强

答复链路分三层，**不需要任何外部云 API、不产生费用、数据不出服务器**：

1. **本地知识库**：内置关键词规则，覆盖配额/空间不足、注册登录、密码、分享、WebDAV、插件、访问慢、套餐等主题。
2. **本地模型网关**：`llm-local.service` 只监听 `127.0.0.1:8083`，提供 OpenAI 兼容 `/v1/chat/completions`。提交工单时，站点把知识库命中要点作为事实依据注入提示词，让小模型基于真实信息组织语言，而不是自由发挥。
3. **兜底**：网关超时或异常时，自动回退到纯知识库答复，并在答复顶部注明原因，工单不会丢失。

工单的 `ai_source` 字段标记来源：

| 值 | 含义 |
| --- | --- |
| `ai:qwen2.5-0.5b-instruct` | 本地模型生成（当前默认） |
| `ai-fallback` | 模型异常，已回退知识库 |
| `local-kb` | 纯知识库模式（用 `--disable` 关闭模型后） |

### 关于模型规格

站点使用 `Qwen2.5-0.5B-Instruct` 的 `Q4_K_M` 量化 GGUF（约 469 MB），在 2 核 / 1.9 GiB 的服务器上加载约 1.5 秒、单次答复约 5~20 秒，实际内存占用受 `MemoryMax=900M` 限制。

> 需求最初写作「本地 0.3B 模型」。目前主流可用的**中文**指令小模型最小档就是 0.5B；真正 0.3B 级别的模型（如 gemma-3-270m、SmolLM2-360M）中文工单答复质量会明显下降。因此选用 0.5B 作为"超小本地模型"的落点。若必须严格 0.3B，替换 `/opt/llm/model.gguf` 并用 `deskctl.py set-ai --model` 改名即可，网关代码无需改动。

### 换用外部大模型（可选）

`deskctl.py set-ai --base-url <OpenAI 兼容地址>` 加上 `--api-key`（走 `deskctl.py` 修改 `config.json`）即可切到云端或自建网关。默认配置指向本地回环，不连外网。

## 六、服务管理与备份

```bash
systemctl status ai-desk              # 状态
systemctl restart ai-desk             # 重启
systemctl status llm-local            # 本地模型网关
journalctl -u ai-desk -f              # 实时日志
curl -s http://127.0.0.1:8081/health # 健康检查
curl -s http://127.0.0.1:8081/api/status  # 查看 AI 是否启用
curl -s http://127.0.0.1:8083/health # 模型网关健康检查
```

```bash
# 备份工单库与配置
cp /opt/ai-desk/data/desk.db /root/backup/desk-$(date +%F).db
cp /opt/ai-desk/config.json  /root/backup/desk-config-$(date +%F).json
```

## 七、直接调 API

```bash
# 提交工单（公开）
curl -X POST http://127.0.0.1:8081/api/tickets \
  -H 'Content-Type: application/json' \
  -d '{"title":"无法上传","content":"提示配额不足","category":"存储与配额","contact":"user@example.com"}'

# 查询工单（公开，凭工单号）
curl http://127.0.0.1:8081/api/tickets/ND-20261108-XXXXXX

# 管理端列表（需请求头令牌）
curl -H 'X-Admin-Token: <令牌>' http://127.0.0.1:8081/api/admin/tickets
```

## 八、与网盘的关系

工单站是**独立站点**，不依赖网盘数据库，也不修改网盘代码。若希望把工单能力做进网盘内部（侧边栏入口、与账号打通），可改造成 v2 插件：用 UI 扩展点挂入口、用钩子回写数据。规范见《插件制作手册》与《插件平台 v2 完全指南》。
