# NebulaDrive

> 新一代毛玻璃风格开源云存储系统 · 作者 **mrc-sk** · 仓库 <https://github.com/mrc-sk/nebula-drive>
>
> 作者：**mrc-sk** · <https://github.com/mrc-sk/nebula-drive>

[![License](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](./LICENSE)
[![Go Build](https://img.shields.io/badge/build-passing-brightgreen)](#)
[![Coverage](https://img.shields.io/badge/coverage-42.2%25-green)](#)

---

## ✨ 项目介绍

NebulaDrive 是一个对标 Cloudreve Pro 的**自托管云存储系统**，采用 Go + React 18 + Vite + TypeScript + TailwindCSS 构建，主打
**毛玻璃 / 亚克力设计语言**、支持多存储后端、离线下载、WebDAV 访问、分享双因子、OAuth2.0 开放 API、协作编辑钩子、公共插件库。

---

## 🎯 核心特性

| 模块 | 功能 |
|---|---|
| 🗂️ **文件管理** | 列表/网格双视图、版本控制（保留最近 10 版）、文件标签、拖拽上传（脉冲边框）、批量移动/复制/改存储策略、回收站 30 天自动清理 |
| ☁️ **多存储后端** | 本地 / AWS S3（aws-sdk-go-v2）/ 阿里云 OSS / 腾讯云 COS，官方 SDK 真实接入 |
| 🌐 **WebDAV** | RFC 4918 方法集：PROPFIND / PROPPATCH / MKCOL / GET / HEAD / PUT / DELETE / MOVE / COPY / LOCK / UNLOCK / OPTIONS，支持 Range 下载；后台可自定义路径前缀 & 方法集白名单。<br>**已知限制**：PROPPATCH 按 RFC §9.2 返回 `207 + 403 Forbidden`（不写自定义 dead property），LOCK 为进程内排他锁（重启后失效） |
| 🚀 **离线下载** | HTTP / BT / 磁力，内嵌 aria2c 子进程 + JSON-RPC，未安装 aria2 自动回退 |
| 🔐 **认证安全** | bcrypt + JWT + 单设备登录、2FA(TOTP) 可选、邮箱验证码、7 天记住登录 |
| 🔗 **分享双因子** | 提取码 + 密码两层保护，签名 URL 防盗链 5 分钟有效 |
| 🤝 **协作编辑** | OnlyOffice 对接钩子 + MD/TXT 内置 Yjs CRDT，WebSocket 实时同步 |
| 🧩 **插件系统** | 8 个官方扩展点（邮件/备份/恢复/API鉴权/DB迁移/限流/防盗链/CLI/协作）+ com.json 插件商店 |
| 🔑 **开放 API** | OAuth2.0 Authorization Code + 个人访问令牌 PAT + /api/v1/* 标准接口 |
| 🛡️ **安全加固** | CSP/X-Frame/HSTS 安全头、上传白名单+魔术字节、密码强度策略、IP 自动封禁、敏感操作二次确认 |
| 📊 **数据统计** | KPI 卡片 + 流量趋势折线 + 文件类型饼图 + 用户活跃度热力图 |
| 🎨 **外观定制** | 站点名/Logo/主题色(6色预设+自定义)/ICP/页脚 后台可视化配置 |
| 🥚 **彩蛋模式** | Logo 持续旋转 hover 暂停、imfeelinglucky 主题转盘、Konami ↑↑↓↓←→←→BA 文字摇摆、连点头像霓虹光环 |
| 🌏 **多语言** | 简体中文 / 繁體中文 / English / 日本語 / 한국어，5 种语言一键切换 |

---

## 🚀 快速开始

### 方式一：一键试用包（推荐给想快速体验的人）

不想装环境的话，用这个：

```bash
# 产出试用包（构建者侧）
packaging\build-release.bat          # Windows
./packaging/build-release.sh          # Linux / macOS
./packaging/build-release.sh --all    # 在 Linux 上交叉编译三个平台

# → release\NebulaDrive-<版本>-<系统>-<架构>\

# 使用者侧：解压后运行对应脚本即可
#   Windows : start.bat        （双击）
#   Linux   : ./start.sh
#   macOS   : ./start.sh
```

**构建者只需装 Go 1.25+**；前端产物 `backend/frontend_dist/` 随仓库提交，
所以**不需要 Node**。使用方则什么都不用装。

**单二进制 45 MB，打成 zip 约 15 MB**
（`-trimpath -ldflags="-s -w"` 剥离符号表，比默认小 27%）。
前端通过 `go:embed` 内嵌进二进制，**使用者无需安装 Go / Node / 数据库**。

`start.sh` 会自动补加执行权限，并检查二进制格式是否与当前系统匹配
（拿了 Windows 版会得到明确提示，而不是难以理解的 `Exec format error`）。

`start.bat` 做的事：
- `-autoport` 自动挑选空闲端口（5212 起，冲突时顺延），避免"端口被占用"起不来
- `-open` 启动后自动打开浏览器
- 缺文件 / 端口全占时给中文提示，而不是命令行报错
- 前台运行，日志直接显示（不另开黑框）

`packaging\试用说明.md` 面向使用者，含安装向导步骤、可试的功能、常见问题。

### 方式二：单二进制运行

```bash
# 下载发行版或自行编译
./nebula

# 默认监听 :5212，浏览器打开
# http://localhost:5212/   → 首次访问自动跳安装向导
```

启动后控制台会打印访问地址和下一步提示：

```
  ────────────────────────────────────────────
   访问地址：http://127.0.0.1:5212

   首次使用：请在浏览器打开上面的地址，
             按安装向导创建管理员账号。
             （默认使用 SQLite，无需额外数据库）
  ────────────────────────────────────────────
```

### 常用启动参数

| 参数 | 说明 |
|---|---|
| `-autoport` | 自动挑选空闲端口（5212 起试30 个），忽略配置里的端口 |
| `-open` | 启动后自动打开浏览器 |
| `-listen <addr>` | 指定监听地址，如 `127.0.0.1:6000` |
| `-data <dir>` | 数据目录，默认 `data`（含 SQLite 文件与加密密钥） |

> `-autoport` 优先级高于配置文件里的 `listen`。这样"上次安装留下的端口被别人占了"
> 不会导致起不来。

### 源码编译

```bash
# 后端 (Go 1.25+)
cd backend
go build -o nebula .

# 前端 (Node 20+)
cd ../frontend
npm install
npm run build
# 产物会被 go embed 嵌入到后端二进制
```

### Docker / deb / rpm 打包

```bash
# deb + rpm
nfpm package -p deb
nfpm package -p rpm
```

---

## 🧩 公共插件库（plugins 分支）

> 社区可以做好插件往 **`plugins`** 分支提交 PR。

### 插件目录结构

```
plugins/
├── smtp-email/          ← 插件名（kebab-case）
│   ├── plugin.json      ← 元数据（名称/版本/作者/钩子）
│   ├── main.go          ← 插件逻辑
│   ├── go.mod
│   └── README.md
├── backup-local/
│   ├── plugin.json
│   └── ...
└── README.md            ← 插件提交指南
```

### 插件提交步骤

1. 从 **`plugins`** 分支 checkout 出 feature/xxx
2. 按上述结构放好插件代码 + plugin.json
3. 提交 PR，目标分支选 **plugins**（不是 main）
4. CI 审核通过后合并

---

## 🏗️ 架构说明

```
nebula-drive/
├── backend/
│   ├── conf/            # 配置层（AES-GCM 加密敏感字段）
│   ├── models/          # GORM 数据模型
│   ├── middleware/      # 认证/限流/安全头/IP 封禁/二次确认
│   ├── controllers/     # 业务控制器（文件/分享/用户/管理/WebDAV）
│   ├── pkg/
│   │   ├── db/          # SQLite/MySQL/PostgreSQL 多库支持
│   │   ├── jwt/         # JWT 签发解析
│   │   ├── plugin/      # 插件钩子引擎
│   │   ├── aria2/       # aria2 JSON-RPC 客户端
│   │   ├── tls/         # ACME + 手动证书
│   │   └── util/        # 工具（UUID/MD5/buffer 池）
│   ├── filesystem/      # 存储抽象层（Local/S3/OSS/COS/缩略图）
│   ├── migrations/      # schema_migrations 版本化迁移
│   ├── routers/         # Gin 路由注册
│   ├── webdav/          # WebDAV RFC 4918 处理器
│   └── dist/            # go embed 嵌入的前端静态资源
└── frontend/
    ├── src/
    │   ├── components/  # UI 组件（通知铃铛/主题切换/语言切换/播放器/预览）
    │   ├── layouts/     # 主布局（侧边栏+顶栏+移动端4Tab）
    │   ├── pages/       # 登录/文件/分享/管理/安装向导
    │   ├── i18n/        # 5 种语言包
    │   ├── store/       # zustand 状态（主题/通知/用户）
    │   └── api/         # axios 客户端
    └── index.css        # 毛玻璃/亚克力/旋转彩蛋/动画
```

### 部署形态

当前为**单实例设计**。以下状态保存在进程内存中，多副本部署需注意：

| 状态 | 位置 | 多副本后果 |
|---|---|---|
| WebDAV 排他锁 | `webdav.go` 的 `davLocks` map | 副本间锁不共享 → 并发写可能互相覆盖 |
| 登录失败计数 | `middleware/ip_guard.go` 的 `loginFailures` map | 计数不共享 → 攻击者可用 N 个副本各试 9 次绕过封禁阈值（封禁**记录**在 DB，判定是共享的，但触发需要凑满 10 次） |
| 验证码触发阈值 | 同上（3 次失败后要求验证码） | 同上 |
| 通用限流 | `middleware/rate_limit.go` 的 `limiter.records` | 限流额度按副本数倍增 |
| 分片上传会话 | `controllers/file.go` 的会话 map | 必须开启会话粘性（sticky session） |

需要水平扩展时，数据库可切 MySQL / PostgreSQL（`conf` 已支持），IP 封禁记录也已落库可直接共享；上表前四项需先外置到 Redis 等共享存储。

---

## 🔧 CI / 持续集成

两个 GitHub Actions 工作流（`.github/workflows/`）：

| 工作流 | 触发条件 | 做什么 |
|---|---|---|
| `go-ci.yml` | 改 `backend/**` 或工作流本身 | `go build` + `go vet` + `go test -race`（ubuntu + windows 双平台，需 cgo） |
| `frontend-ci.yml` | 改 `frontend/**` 或工作流本身 | `npm ci` + `npm run build`（`tsc -b` 严格类型检查 + `vite` 生产构建），并校验 `backend/frontend_dist` 与构建产物一致 |

> ⚠️ **内嵌前端同步约定**：`backend/frontend_dist` 被 Go `go:embed` 打进单二进制。
> 改了前端源码后，必须在 `frontend/` 下执行 `npm run build` 再一并提交 `backend/frontend_dist/`，
> 否则 `frontend-ci.yml` 的同步守卫会判红。

---

## 📝 开源协议

> **GNU Affero General Public License v3.0（AGPL-3.0）**
>
> Copyright (c) 2026 **mrc-sk** · Repository: <https://github.com/mrc-sk/nebula-drive>

> 第一次听说 AGPL？看 [AGPL-通俗说明.md](./AGPL-通俗说明.md)，有说人话的版本和常见疑问解答。

### ✅ 你可以

- **自托管**：给自己团队 / 公司 / 客户部署，不需要通知作者
- **修改与二次开发**：任意修改、按需定制，自用无需公开
- **收费服务**：可以收费卖运维、部署、定制开发等服务

### 🔒 你必须

AGPL-3.0 的核心义务是**网络服务同样要开源**（这是它与 GPL 的唯一区别）：

1. **对外提供网络服务时**：若把本程序或其修改版部署到服务器、供他人通过网络访问，
   **必须向这些使用者提供完整的对应源码**（含你的修改）。
2. **分发二进制时**：随附完整源码，或提供获取源码的书面要约。
3. **保留版权与许可声明**：不得删除 [LICENSE](./LICENSE) 中的版权声明。
4. **标注修改**：对原代码做过修改的部分需注明。

> **常见疑问**
>
> | 场景 | 是否需要公开你的修改源码 |
> |---|---|
> | 内部使用，不对外提供服务 | ❌ 不需要（自部署自用完全自由） |
> | 改了代码自己部署，不对外开放 | ❌ 不需要 |
> | 改了代码部署到公网供他人使用 | ✅ **需要**（属对外提供网络服务，AGPL §13） |
> | 编译成二进制分发 / 卖给客户，客户自行部署 | ✅ **需要**（属"分发"，须随附或书面要约提供源码，§6） |
> | 用本项目搭建 SaaS 收费 | ✅ **需要**（包括你基于它做的修改） |
>
> 简言之：**自用随便，一旦让别人用上（无论发给他们，还是让他们上网访问），就得给源码。**
> 注意：AGPL **不允许**闭源分发衍生版本——这与 MIT 等宽松协议不同。
>
> 简单记：**只要"让别人用上"，就要开源你的修改**——不论是把软件发给他们，还是让他们上网访问。
> 唯一"什么都不用做"的情况是：**只给你自己（或你自己的组织）用**。

完整条款见 [LICENSE](./LICENSE)；通俗解释见 [AGPL-通俗说明.md](./AGPL-通俗说明.md)。

---

## ❤️ 致谢

作者：**mrc-sk** · <https://github.com/mrc-sk>
仓库：<https://github.com/mrc-sk/nebula-drive>

> 本项目基于 **NebulaDrive (<https://github.com/mrc-sk/nebula-drive>)** 开发，
> 原作者 **mrc-sk**。感谢所有为公共插件库贡献代码的社区成员。
