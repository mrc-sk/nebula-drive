# NebulaDrive

> 新一代毛玻璃风格开源云存储系统 · 作者 **mrc-sk** · 仓库 <https://github.com/mrc-sk/nebula-drive>
>
> 本项目基于 **NebulaDrive (<https://github.com/mrc-sk/nebula-drive>)** 开发，原作者 **mrc-sk**。

[![License](https://img.shields.io/badge/License-MIT%20%2B%20Attribution-blue)](#-开源协议)
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
| 🌐 **WebDAV** | RFC 4918 全方法实现，后台可自定义路径前缀 & 方法集白名单，支持 Range 下载 |
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

### 单二进制运行

```bash
# 下载发行版或自行编译
./nebula

# 默认监听 :5212，浏览器打开
# http://localhost:5212/   → 首次访问自动跳安装向导
```

### 源码编译

```bash
# 后端 (Go 1.22+)
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

---

## 📝 开源协议

> 组合协议：**MIT License + 署名保留附加条款 v1.0**
>
> Copyright (c) 2026 **mrc-sk** · Repository: <https://github.com/mrc-sk/nebula-drive>

### ✅ 你可以

- **商用**：任意修改、二次开发、分发、用于商业产品、收费服务 —— **完全免费，无需报备**
- **自托管**：给自己团队/公司/客户部署，不需要通知作者

### 🔒 你必须（**至少 3 处署名，禁止移除**）

1. **LICENSE 文件**：完整保留本仓库 [LICENSE](./LICENSE) 中 "Copyright (c) 2026 mrc-sk" 与仓库链接声明；
2. **用户界面页脚 / 关于页**：软件运行时的 UI 中（网页 Footer / 关于页面 / 命令行 --version）必须显示：
   `Powered by NebulaDrive · 作者 mrc-sk · 仓库 github.com/mrc-sk/nebula-drive`；
3. **README / 文档 / 产品介绍页**：任何配套文档、官网、产品介绍中必须包含项目来源声明：
   "本项目基于 NebulaDrive (https://github.com/mrc-sk/nebula-drive) 开发，原作者：mrc-sk"。

详见完整协议 [LICENSE](./LICENSE)。

---

## ❤️ 致谢

作者：**mrc-sk** · <https://github.com/mrc-sk>
仓库：<https://github.com/mrc-sk/nebula-drive>

> 本项目基于 **NebulaDrive (<https://github.com/mrc-sk/nebula-drive>)** 开发，
> 原作者 **mrc-sk**。感谢所有为公共插件库贡献代码的社区成员。
