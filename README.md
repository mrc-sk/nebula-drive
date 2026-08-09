# NebulaDrive 公共插件库

> 作者 **mrc-sk** · 主仓库 <https://github.com/mrc-sk/nebula-drive> · 插件商店配置：`com.json`
>
> 本插件库是 **NebulaDrive (<https://github.com/mrc-sk/nebula-drive>)** 项目的一部分，
> 原作者 **mrc-sk**。所有插件默认采用与主仓库相同的「MIT + 署名保留条款 v1.0」协议，
> 除非插件子目录内 LICENSE 文件另有声明。

---

## 🔌 这是什么分支？

**`plugins` 分支是 NebulaDrive 的公共插件库**，社区开发者可以做好插件往这里提交 PR，
由管理员审核后合并。所有已发布的插件会被前端插件商店页面（`/admin/plugins`）通过
`com.json`（本分支根）自动拉取展示，用户点击「安装」后即完成插件注册。

---

## 📦 插件目录结构（必须遵守）

```
your-plugin-name/            ← kebab-case，英文小写，用连字符
├── plugin.json              ← 元数据（见下方模板）
├── main.go                  ← 插件入口（Go 源码；如果是前端/纯配置插件可以没有）
├── go.mod                   ← 如果是 Go 插件
├── README.md                ← 插件说明（配置方式、截图、作者）
├── LICENSE                  ← 可选；不写则默认采用主仓库组合协议
└── assets/                  ← 可选；前端资源、图标、CSS
    └── icon.svg
```

### `plugin.json` 模板

```json
{
  "id": "smtp-email",
  "name": "SMTP 邮件插件",
  "version": "1.0.0",
  "author": "mrc-sk",
  "homepage": "https://github.com/mrc-sk/nebula-drive/tree/plugins/smtp-email",
  "description": "通过 SMTP 协议发送邮件通知。支持 QQ/163/Gmail/SendCloud。",
  "tags": ["email", "smtp", "notification"],
  "hooks": ["onEmail"],
  "minAppVersion": "1.0.0",
  "dependencies": [],
  "settings": [
    {"key": "smtp_host", "type": "string", "label": "SMTP 地址", "default": "smtp.qq.com"},
    {"key": "smtp_port", "type": "number", "label": "端口", "default": 465}
  ]
}
```

`hooks` 可以取的值（对应主仓库 8 大扩展点）：
- `onEmail` · `onBackup` · `onRestore` · `onApiAuth` · `onDBMigrate`
- `onRateLimit` · `onAntiLeech` · `onCLI` · `onCollabOpen` · `onCollabSave`

---

## 🏪 插件商店 com.json 说明

用户访问后台 `/admin/plugins` 时，会向 `plugin.store.url`（默认值：
`https://raw.githubusercontent.com/mrc-sk/nebula-drive/plugins/com.json`）
发起 GET 请求获取插件目录。

`com.json` 结构：

```json
{
  "storeName": "NebulaDrive 官方插件商店",
  "storeOwner": "mrc-sk",
  "storeRepo": "https://github.com/mrc-sk/nebula-drive",
  "updatedAt": "2026-08-09T00:00:00Z",
  "plugins": [
    {
      "id": "smtp-email",
      "name": "SMTP 邮件插件",
      "version": "1.0.0",
      "author": "mrc-sk",
      "homepage": "https://github.com/mrc-sk/nebula-drive/tree/plugins/smtp-email",
      "downloadUrl": "https://github.com/mrc-sk/nebula-drive/archive/refs/heads/plugins.zip",
      "archiveSubdir": "nebula-drive-plugins/smtp-email",
      "description": "...",
      "tags": ["email"],
      "hooks": ["onEmail"],
      "icon": "smtp-email/assets/icon.svg",
      "minAppVersion": "1.0.0",
      "settings": [...]
    }
  ]
}
```

合并 PR 后，管理员会更新 `com.json` 让新插件出现在商店里。

---

## 🔐 协议

所有向本分支提交代码，视为同意：
1. 插件采用与主仓库相同的组合协议（MIT + 署名保留条款 v1.0，至少 3 处保留原作者名 **mrc-sk** 和仓库名 **github.com/mrc-sk/nebula-drive**）；
2. 插件代码不包含任何恶意、窃取数据、破坏功能的逻辑；
3. 插件作者授权 NebulaDrive 项目方（**mrc-sk**）在插件商店中展示、托管与分发其插件。

---

## ✅ 品牌署名要求（二次发布插件时同样适用）

插件二次分发 / 打包售卖 / 集成进商业产品，必须遵守主仓库协议：
**至少 3 处保留原作者名 mrc-sk 与仓库名 github.com/mrc-sk/nebula-drive**：
1. 插件自身的 LICENSE；
2. 插件 README 顶部；
3. 插件 UI（如果有设置面板/弹窗）页脚或关于区。

---

作者：**mrc-sk** · <https://github.com/mrc-sk>  
仓库：<https://github.com/mrc-sk/nebula-drive>（plugins 分支）
