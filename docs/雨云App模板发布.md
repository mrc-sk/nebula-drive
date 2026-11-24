# 雨云 App 模板发布指南（nebula-drive网盘）

> 对应雨云控制台：应用模板 → nebula-drive网盘 → 添加版本 →「从 Docker 导入」。
> 本文给出每个字段该填什么，照抄即可。参考官方教程：<https://forum.rainyun.com/t/topic/11296>

---

## 最快路径：用「从 Docker 导入」一键转换

### 方式 A：Docker Run 命令（最稳，推荐）

弹窗里选 **Docker Run 命令** 标签，粘这一行，点「转换」：

```
docker run -p 5212:5212 -e TZ=Asia/Shanghai ghcr.io/mrc-sk/nebula-drive:latest
```

**故意不带 `-v`**：雨云是 K8s 语义，`-v ./data:/data` 这种宿主机相对路径它解析不了，
很可能就是「点转换没反应」的原因。先用最简命令转换成功，再在导入后的表单里
**手动加一条 VolumeMounts**：

| 字段 | 值 |
|---|---|
| 名称 | `data` |
| 挂载路径 | `/data` |
| 子路径 | `nebula-drive` |
| 内容类型 | 目录 |

⚠️ 这条不能省——`/data` 里是数据库、配置、密钥和上传文件，不加的话应用重启后数据全丢。

其余转换后核对：镜像 = `ghcr.io/mrc-sk/nebula-drive:latest`；端口内部/外部都 `5212`、tcp、外部访问；Env `TZ=Asia/Shanghai`；Command 与 Args 留空（镜像已带默认 CMD）。

### 方式 B：Docker Compose 标签

如果方式 A 已成功，这段可跳过。选 **Docker Compose** 标签，把下面这段粘进输入框，点「转换」：

```yaml
version: '3'
services:
  web:
    image: ghcr.io/mrc-sk/nebula-drive:latest
    ports:
      - "5212:5212"
    environment:
      - TZ=Asia/Shanghai
    volumes:
      - ./data:/data
```

仓库里也存了一份：`docker-compose.rainyun.yml`（可直接复制全文）。
该 compose 已用 Docker Compose v2 的 `config` 命令验证语法通过。

> 如果这段点「转换」没反应：**删掉 `volumes:` 那两行再试**。
> `./data:/data` 是宿主机相对路径写法，标准 Compose 认，但雨云的 K8s 转换器可能静默失败。
> 删掉后转换成功，再按方式 A 里那张表手动补 VolumeMounts 即可。

**刻意没写的东西**（避免雨云解析器不认）：
- 没有 `build:` —— 镜像已推到 GHCR，雨云只需拉取；本地开发用的 `docker-compose.yml` 才带 build
- 没有 `container_name` / `restart` / `healthcheck` —— K8s 里这些由平台管，compose 里写了反而可能被误解析

**转换后必须核对的字段**（自动导入未必完整）：

| 位置 | 核对项 | 正确值 |
|---|---|---|
| 基本信息 | 镜像 | `ghcr.io/mrc-sk/nebula-drive:latest` |
| 服务配置 | 内部端口 / 外部端口 | `5212` / `5212`，协议 `tcp`，类型「外部访问」 |
| 环境变量 | `TZ` | `Asia/Shanghai` |
| **持久化卷** | 挂载路径 | `/data`，内容类型「目录」，子路径建议 `nebula-drive` |
| 命令与参数 | **留空即可** | 镜像自带 ENTRYPOINT+CMD，见下 |
| 最小 CPU / 内存 | | 1 核 / 512 MB |

### 镜像自带的运行参数（已从镜像 config 验证）

从 GHCR 拉取的镜像 config 实测：

```
Entrypoint : ["/usr/local/bin/nebula"]
Cmd        : ["-data", "/data", "-listen", ":5212"]
User       : nebula            (uid 10001，非 root)
WorkingDir : /data
Volumes    : {"/data": {}}     (镜像自带卷声明)
ExposedPorts: 5212/tcp
```

**「命令与参数」tab 两种填法都可以，推荐显式填**：

| 字段 | 填法 | 说明 |
|---|---|---|
| 运行命令 Command | 留空 | 镜像已有 ENTRYPOINT，一般不用覆盖 |
| 命令参数 Args | `-data /data -listen :5212` | **推荐**：显式指定数据目录，路径最干净 |

如果 Args 留空（用镜像默认 CMD），也能正常工作 —— 此时数据目录同样是 `/data`。
但如果平台自行接管了启动参数、`-data` 变成默认的相对路径 `data`，
数据库就会落到 `<工作目录>/data/nebula.db`（多一层 `data/`）。
功能正常，只是目录结构不整齐。显式填 Args 可避免这个歧义。

### ⚠️ 已修复的坑：安装向导报 "db connect failed ... out of memory (14)"

**现象**：安装向导「数据库配置」一步报错：

```
db connect failed: unable to open database file: out of memory (14)
```

**别被 "out of memory" 误导** —— SQLite 错误码 **14 是 `CANTOPEN`（打不开文件）**，
"out of memory" 只是 SQLite 对该错误码的默认文案。

**根因**：安装向导的数据库文件默认值是**相对路径**，而 SQLite 按**进程当前工作目录**
解释相对路径。容器里 `WORKDIR=/data`，于是 `data/nebula.db` 变成
`/data/data/nebula.db` —— 目录不存在 → CANTOPEN。

**修复**：已在 `backend/pkg/db/db.go` 的 `Init()` 中把 sqlite 相对路径锚定到
`conf.DataDir()`（即 `-data` 指定的数据目录）并自动创建父目录。
现在相对路径、绝对路径都能正常工作，且与工作目录无关。

> 如果你用的是**修复前**的镜像（GHCR 上 `latest` 早于本提交），
> 临时解法：安装向导「数据库配置」的文件路径直接填**绝对路径** `/data/nebula.db`，
> 上传路径填 `/data/uploads`。填完点「开始安装」即可。

### 其他 tab 的处理

| tab | 操作 |
|---|---|
| 命令与参数 | 留空即可，或按上表显式填 Args |
| 脚本设定 | 不用（安装是运行时 Web 向导） |
| 配置文件 | 不用（配置在后台加密存储） |
| 环境变量 | 已转换出 1 个 `TZ=Asia/Shanghai`，核对即可 |
| **持久化卷** | **要加**：`/data` 目录类型，子路径 `nebula-drive` |
| 服务配置 | 已转换出 1 个，核对内部/外部端口都 `5212`、tcp、外部访问 |

### 最小 CPU / 内存

1 核 / 512 MB 起步；并发上传多时建议 2 核 / 1024 MB。

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
| 版本号 | `26.11.4`（或 `1.0.0`，雨云示例用 X.Y.Z；建议与软件版本一致） |
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
5. 后台确认「关于」页版本号是 26-11.4

---

## 常见坑

| 现象 | 原因 |
|---|---|
| 容器起不来 / ImagePullBackOff | GHCR 镜像私有，或 workflow 没跑成功；检查 Packages 可见性 |
| 能起但访问 502 | 服务内部端口填错（必须 5212），或容器监听了 127.0.0.1 |
| 重启后数据没了 | VolumeMounts 没配 `/data`，或配成了文件类型 |
| 上传报权限错 | 持久化卷属主问题；镜像以 uid 10001 运行，需卷可写 |
| 版本号显示 dev | 构建时没传 `build-arg VERSION`，或用的不是 workflow 构建的镜像 |
