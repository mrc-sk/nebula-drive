# NebulaDrive Docker 部署

> 仓库根目录提供了 `Dockerfile` 与 `docker-compose.yml`，一条命令即可构建并运行。
> 镜像是单二进制 + 内嵌前端，运行时不需要 Node / Go / 数据库。

---

## 前置条件

- Docker Engine 24+ 或 Docker Desktop，带 `docker compose` 插件
- 约 1 GB 可用磁盘（构建缓存 + 镜像 + 数据）

> 注意：仓库的 `.gitignore` 排除了可执行文件（`*.exe` 等），
> 所以**不能用本地编译的二进制直接塞进镜像** —— 请用下面两种方式从源码构建。

---

## 方式一：docker compose（推荐）

在仓库根目录执行：

```bash
# 构建镜像并前台启动（第一次会下载 node/go 基础镜像，稍慢）
docker compose up --build

# 或后台启动
docker compose up -d --build
```

启动后浏览器打开 <http://localhost:5212/>，首次访问自动进入安装向导。

compose 文件已配置：
- 端口 `5212:5212`
- 数据卷 `./data:/data`（数据库、配置、密钥、上传文件全在这里）
- 非 root 运行（uid/gid 10001）
- 健康检查 + `restart: unless-stopped`

改端口/数据目录：编辑 `docker-compose.yml` 的 `ports` 和 `volumes` 两处即可。

---

## 方式二：纯 docker 命令

```bash
# 构建镜像（VERSION 会注入到二进制，供插件读取 HostVersion）
docker build --build-arg VERSION=V26-11.4 -t nebula-drive:V26-11.4 .

# 准备数据目录（容器内以 uid 10001 运行，需要写权限）
mkdir -p data && chmod 777 data

# 运行
docker run -d --name nebula-drive \
  -p 5212:5212 \
  -v "$PWD/data:/data" \
  -e TZ=Asia/Shanghai \
  --restart unless-stopped \
  nebula-drive:V26-11.4
```

---

## 数据持久化与备份

所有状态都在 `/data`（对应宿主机的 `./data`）：

| 内容 | 位置 |
|---|---|
| SQLite 数据库 | `/data/nebula.db` |
| 配置（conf.ini / conf.env） | `/data/conf.*` |
| 签名密钥 | `/data/secret.key` |
| 上传文件 | `/data/uploads/` |

**升级**：`docker compose down` → 拉取/构建新镜像 → `docker compose up -d`。数据卷保留，不会丢。

**备份**：直接打包 `./data` 目录即可；也可用后台「备份」功能生成 zip。

> ⚠️ `secret.key` 是配置加密密钥，丢失后加密字段（TOTP/存储密钥等）无法解密。
> 备份时务必连同 `data` 目录一起保存。

---

## 容器内配置要点

### 监听地址
容器**必须**监听 `0.0.0.0`（`:5212`），不能写 `127.0.0.1`，否则宿主机端口映射不通。
Dockerfile 的 CMD 已经设成 `-listen :5212`，一般无需改。

### 域名 / 反代
前面挂 Nginx/Caddy 反代时，把后台「系统设置 → 域名」设为对外域名。
容器内的 `conf` 仍以 `/data` 为准，反代只转发 HTTP。

### CDN 加速
后台「设置 → 存储 → CDN 加速」启用后，文件下载会 302 到 CDN 签名 URL。
在容器化 + 反代场景下注意：
- CDN 回源地址必须指向**对外域名或真实源站**，不能填容器内部地址
- 若回源走反代，确保反代透传 `Range` 头（视频拖动需要）

### WebDAV
默认路径 `/dav/`，反代时不要把该路径 rewrite 掉。客户端配置见
[《WebDAV客户端配置》](./WebDAV客户端配置.md)。

---

## 常见排查

| 现象 | 原因 / 处理 |
|---|---|
| 构建卡在 `npm ci` | 网络问题；-node 镜像可换国内源，或在本地先 `npm ci` 后调整 |
| 启动后访问 5212 空白 | 看日志 `docker logs nebula-drive`；多半是数据目录权限问题 |
| 重启后配置丢失 | 没挂 `/data` 卷；检查 compose 的 volumes |
| `permission denied` 写数据 | 宿主机 `./data` 属主不是 uid 10001；`chown -R 10001:10001 data` |
| 健康检查一直 unhealthy | `wget` 在 alpine 里可用；确认容器内 5212 端口在监听 |
