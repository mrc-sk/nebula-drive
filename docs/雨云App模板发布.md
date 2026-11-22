# 雨云 App 模板发布指南（nebula-drive网盘）

> 对应雨云控制台：应用模板 → nebula-drive网盘 → 添加版本 →「从 Docker 导入」。
> 本文给出每个字段该填什么，照抄即可。参考官方教程：<https://forum.rainyun.com/t/topic/11296>

---

## 前置：镜像从哪来

雨云的容器本质是 K8s Deployment，必须有一个**公网可拉取**的 Docker 镜像。
仓库已配好 GitHub Actions（`.github/workflows/docker-publish.yml`），
push main 或手动触发后会构建镜像推到 GHCR：

```
ghcr.io/mrc-sk/nebula-drive:latest
```

**如果 GHCR 镜像是私有的**，雨云拉取会失败，二选一：
- 在 GitHub 仓库 Settings → Actions → General → Workflow permissions 勾选
  "Read and write permissions"，重新跑 workflow；然后在 Packages 里把该包可见性改为 **Public**
- 或把镜像推到你自己的 Docker Hub / 雨云镜像仓库，替换下面的镜像地址

> 「从 Docker 导入」按钮可能自动解析 Dockerfile/compose 填一部分字段，
> 但镜像可拉取是硬前提。导入后请按下面清单逐项核对。

---

## 版本基本信息

| 字段 | 填写值 |
|---|---|
| 版本号 | `26.11.3`（或 `1.0.0`，雨云示例用 X.Y.Z；建议与软件版本一致） |
| 镜像 | `ghcr.io/mrc-sk/nebula-drive:latest` |
| 最小 CPU | `1` 核（Go 单进程很轻，1 核够跑；并发上传多时建议 2） |
| 最小内存 | `512` MB（512 起步；大文件或高并调优到 1024） |

---

## Command & Env 命令与参数

镜像的 `ENTRYPOINT` 已经是 `/usr/local/bin/nebula`，`CMD` 默认是
`-data /data -listen :5212`。所以：

| 字段 | 填写值 | 说明 |
|---|---|---|
| Command | **留空** | 不要覆盖 ENTRYPOINT |
| Args | `-data /data -listen :5212` | 也可留空用镜像默认 CMD；显式写出更清晰 |

⚠️ 雨云文档强调：如果用 `/bin/sh` 包命令，必须加 `-c`。这里我们不需要包 shell，直接给参数即可。

---

## Env 环境变量

| Key | Value | 说明 |
|---|---|---|
| `TZ` | `Asia/Shanghai` | 时区，影响日志与计划任务时间 |

> 数据目录、监听地址已通过 Args 指定，不需要额外环境变量。
> 应用本身不读 `NEBULA_DATA_DIR` 作为主数据目录（那是插件页用的），别填错。

---

## VolumeMounts 持久化卷挂载

**必须配置**，否则用户部署后数据会在容器重启时丢失。

| 字段 | 填写值 |
|---|---|
| 名称 | `data` |
| 挂载路径 | `/data` |
| 子路径 | `nebula-drive`（项目磁盘下的子目录，避免和其他应用冲突） |
| 内容类型 | 目录 |
| 预设内容 | 留空 |

> `/data` 内含：SQLite 数据库、配置、签名密钥、上传文件。
> 安装时容器（initContainer）会把同一批卷挂到 `/app`，但我们的安装向导是运行时通过 Web 完成的，
> 不需要安装脚本，所以**不用配安装脚本/安装镜像**。

---

## Services 服务配置

| 字段 | 填写值 |
|---|---|
| 服务名称 | `web` |
| 显示名称 | `NebulaDrive 网盘` |
| 服务类型 | 外部访问 |
| 内部端口 | `5212` |
| 外部端口 | `5212`（或留空让系统分配；也可填 `${PORT}` 配合 Options） |
| 协议 | `tcp` |

> 容器内必须监听 `0.0.0.0`（`:5212`），镜像 CMD 已设好，不要改成 127.0.0.1。

---

## Options 用户可填选项（建议）

在部署时给用户友好输入：

| 标签 | 环境变量键 | 类型 | 默认值 | 必填 | 说明 |
|---|---|---|---|---|---|
| 外部端口 | `PORT` | 数字 | `5212` | 是 | 若 Services 外部端口填了 `${PORT}`，这里就能改 |
| 时区 | `TZ` | 选择/文本 | `Asia/Shanghai` | 否 | 已有 Env 默认值，这里可让用户覆盖 |

> 其余配置（域名、CDN、存储策略等）都在应用**运行后**的 Web 后台里设置，
> 不需要放进部署选项。

---

## ConfigMap / Scripts

| 项 | 是否需要 | 说明 |
|---|---|---|
| ConfigMap | 否 | 应用配置在 Web 后台管理并加密存储，不用配置文件挂载 |
| 安装脚本 / 安装镜像 | 否 | 安装向导是运行时 Web 流程 |
| 启动脚本 / 停止脚本 | 否 | 镜像 ENTRYPOINT/CMD 已处理 |

---

## 发布后自检

1. 部署实例，等容器 running
2. 浏览器打开分配的公网地址，应出现**安装向导**（首次访问）
3. 完成安装 → 上传一个小文件 → 刷新确认持久化生效
4. 重启实例，确认文件还在（验证 `/data` 卷真的挂上了）
5. 后台确认「关于」页版本号是 26-11.3

---

## 常见坑

| 现象 | 原因 |
|---|---|
| 容器起不来 / ImagePullBackOff | GHCR 镜像私有，或 workflow 没跑成功；检查 Packages 可见性 |
| 能起但访问 502 | 服务内部端口填错（必须 5212），或容器监听了 127.0.0.1 |
| 重启后数据没了 | VolumeMounts 没配 `/data`，或配成了文件类型 |
| 上传报权限错 | 持久化卷属主问题；镜像以 uid 10001 运行，需卷可写 |
| 版本号显示 dev | 构建时没传 `build-arg VERSION`，或用的不是 workflow 构建的镜像 |
