# WebDAV 客户端配置

> NebulaDrive 的 WebDAV 服务让你用系统文件管理器或第三方客户端直接访问网盘文件，
> 无需打开浏览器。本文给出主流客户端的配置方法与已知边界。

---

## 连接参数

| 项 | 值 |
|---|---|
| 服务地址 | `https://你的域名/dav/`（路径前缀可在后台改，默认 `/dav`） |
| 协议 | WebDAV (HTTP/HTTPS) |
| 用户名 | 你的 NebulaDrive 用户名 |
| 密码 | 你的登录密码（浏览器/客户端需支持 Basic Auth） |
| 方法白名单 | 后台可配，默认开放 12 个 RFC 4918 方法 |

> 地址末尾的 `/` 建议保留。部分客户端不加尾斜杠会把 `/dav` 当成文件而非目录。

### 先确认 WebDAV 是否开启

后台 → 设置 → WebDAV，确认开关已打开、路径前缀与方法集符合预期。
关闭状态下访问 `/dav/` 会得到 404。

---

## 各客户端配置

### Windows 10/11（映射网络驱动器）

1. 文件资源管理器 → 此电脑 → 映射网络驱动器
2. 文件夹填入 `https://你的域名/dav/`
3. 勾选「使用其他凭据连接」，输入用户名密码
4. 若提示「无法连接」：Windows 原生 WebDAV 对 HTTPS 证书要求严格，
   自签证书会失败。可改用下方客户端，或导入证书到受信任根证书颁发机构。

> Windows 原生客户端对 WebDAV 的实现较旧（只支持 Basic/Digest），
> 且大文件上传有 50MB 限制。传输大文件建议用浏览器或第三方客户端。

### macOS（访达挂载）

1. 访达 → 前往 → 连接服务器（⌘K）
2. 输入 `https://你的域名/dav/`
3. 点「连接」，选择「注册用户」，输入用户名密码

### Linux（mount.davfs / rclone）

```bash
# davfs2
sudo mount -t davfs https://你的域名/dav/ /mnt/nebula

# rclone（推荐，支持断点续传与大文件）
rclone config  # 新建 remote，类型选 webdav，url 填 https://你的域名/dav/
```

### 手机端

- **iOS**：Documents、FE File Explorer 等支持 WebDAV 的 App
- **Android**：Solid Explorer、CX 文件管理器等，新建 WebDAV 连接填入上述参数

---

## 已知限制（重要）

| 限制 | 说明 |
|---|---|
| PROPPATCH | 按 RFC §9.2 返回 `207 + 403 Forbidden`，**不写自定义 dead property**。依赖此特性的客户端会报错。 |
| LOCK | 进程内排他锁，**重启后失效**，多副本部署时副本间不共享。单实例日常使用无影响。 |
| 单实例设计 | WebDAV 锁、限流计数等状态在进程内存；多副本需外置到 Redis 并开启会话粘性。 |
| Range 下载 | 支持，视频拖动播放可用。 |
| 大文件上传 | 浏览器端推荐走 Web UI（有分片 + 秒传）；WebDAV PUT 是单请求流式写入。 |

---

## 与 CDN 加速的关系

如果后台开启了 CDN 加速，浏览器端的**下载/预览**会走 CDN 签名 URL，
但 **WebDAV 客户端仍直连源站**（WebDAV 需要 Basic Auth 与 LOCK 语义，
不适合经 CDN 缓存）。这是有意为之：WebDAV 的写操作与锁状态必须落在源站。

CDN 配置方法见后台 → 设置 → 存储 → CDN 加速区块。要点：
- 回源地址必须是真实源站，不能是 CDN 自身（否则回源死循环）
- CDN 缓存时间不要超过签名有效期（5 分钟）
- 签名 URL 是临时授权，拿到链接的人 5 分钟内可读取对应文件
