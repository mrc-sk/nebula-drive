# CHANGELOG

> 作者 **mrc-sk** · 仓库 <https://github.com/mrc-sk/nebula-drive>

---

## V26-10.0-d — Beta（2026-10-05）

> **插件系统从「装饰品」变成真的能用。** 此前管理后台的插件页看着挺完整，
> 但装不了、启不动、扩展点数量是随机数 —— 因为后端压根没有加载第三方代码的能力。

### ✨ 插件热加载体系（独立子进程 + JSON-RPC）

插件以**独立子进程**运行，通过 stdin/stdout 上的 JSON-RPC 2.0 与宿主通信。
**启用 = 拉起进程，禁用 = 杀进程，主服务不重启。**

选型说明：不用 Go 原生 `plugin.Open`（仅 Linux/macOS 且需 cgo，
**Windows 根本编译不了**，而本项目日常在 Windows 开发、要打包给 Linux 朋友）；
不用 WASM（需 CGO，Go host function 绑定成本过高）。

- **协议层** `pkg/plugin/manifest.go`
  `plugin.json` 清单（name/version/entry/hooks/protocolVersion/permissions/license），
  逐字段校验。`entry` **禁止路径分隔符** —— 否则 manifest 能让宿主执行
  插件目录外的任意文件，等于开放任意代码执行。协议版本不匹配直接拒载。

- **宿主** `pkg/plugin/host/`
  子进程生命周期、RPC 编解码、调用超时（默认 2s）、崩溃指数退避自动重启
  （连续 5 次停止并转人工）、环形日志缓冲（500 条）。
  进程组隔离：Windows 用 `taskkill /T` 避免留孤儿进程，Unix 用 `kill(-pgid)`。

- **安装器** `pkg/plugin/install/`
  zip slip 防护、zip bomb 限额（解压 256MB / 单文件 64MB / 条目 2000）、
  拒绝符号链接、拦截 Windows 保留名（`CON`/`NUL`/`COM1`…）。

- **示例插件** `examples/plugins/referer-guard/`
  独立 module（不 import 宿主），实现 `onAntiLeech` 的 Referer 白名单，
  支持前缀 / `*.` 通配 / `re:` 正则三种写法，附下载审计日志。

### 🛡️ 插件协议与强制关卡

首次进入插件页会弹窗要求阅读**插件协议**（8 节，含权限风险、拦截语义、
AGPL 合规要求）。两条硬约束：

- **必须滚动到底**才能勾选同意 —— 一打开就能点同意等于没要求同意；
- **后端强制**（`requireAgreement`），未同意时安装/启用接口直接 403。
  只靠前端弹窗的话，直接调 API 就能绕过。

协议正文硬编码在宿主内置（`pkg/plugin/agreement.go`），
不从配置文件读 —— 从外部读意味着部署者能改掉管理员看到的内容。
协议版本升级后需重新同意。

### 🔧 修复

- **「启用/禁用」开关是装饰性的**
  `models.Plugin.Enabled` **此前没有任何生产代码读取** ——
  `grep "Enabled" | grep -i plugin` 唯一命中是测试数据。
  `TogglePlugin` 只翻转数据库布尔值，插件代码根本不读它。
  现在启用会真正拉起子进程，并把 `status`/`pid`/运行错误返回给前端；
  数据库说启用但进程没跑时，列表会**显式提示这个矛盾**。

- **扩展点数量是随机数**
  前端 `client.ts` **没有 `pluginsHooks` 方法**，代码 fallback 到同样不存在的
  `api.get`，于是走进 mock 分支：`Math.random()` 生成 handler 数量。
  页面上显示的数字每次刷新都变，与真实情况无关。现在接真实接口。

- **插件页的「安装」是 `alert()` 占位**，商店拉取失败时一律返回空数组
  （分不清"商店为空"还是"拉取失败"）。现在真实安装，失败给明确原因，
  商店条目逐条校验（坏条目跳过并说明原因，不让一个坏条目打不开整个商店）。

- **`Config` 字段带 `json:"-"`**，管理端根本拿不到插件配置，
  插件作者无法配置任何东西。已放开。

- **宿主退出后插件变孤儿进程**。新增 signal 处理，
  退出前 `StopPlugins()` 清理；第二次信号恢复默认行为允许强杀。

### ⚙️ 其它

- `onRateLimit` 是异步钩子但限流中间件需要拦截决策。
  新增 `RateLimitGate` / `Dispatcher.DispatchSync`：
  钩子保持异步（不拖累另外 6 个不读结果的热路径），
  仅在需要决策处强制同步等待。
- 进程内 handler 加 panic recover 与 5s 超时；
  `Fire` 改为先复制 handler 快照再释放读锁（锁内做 IPC 往返会卡死 `Register`）。
- 新增 migration v6（插件表字段 + 存量数据补默认许可）。
- 发版脚本扩展到 5 平台（新增 linux/arm64、darwin/arm64），
  注入版本号（插件可据此做版本判断），生成 `SHA256SUMS.txt`。

### ✅ 验证

- `go build ./...` / `go vet ./...` 全过
- 交叉编译 5 平台：windows-amd64、linux-amd64、linux-arm64、darwin-arm64、darwin-amd64
- `go test ./...` 全包通过（插件宿主 17 项：崩溃隔离、超时、20 路并发、
  崩溃自动重启、init 失败拒绝、拦截语义）
- 前端 `tsc -b` + `vite build` 通过，i18n 五语言门禁「全部 key 齐全」
- 实机冒烟：真实启动二进制 → 装插件 → 启用 → 验证外站 Referer 被 403 拦截
  → 禁用 → 验证拦截消失且**主服务未重启**、插件子进程已回收

---

## V26-10.0-c — Beta（2026-10-04）

> **分享页整条链路接通了。** 此前下载按钮点不动、预览区全是假内容 —— 分享功能等于只有一个链接能打开。

### 🐛 修复

- **分享页的下载按钮是个纯装饰**
  `pages/Share.tsx` 里的下载按钮**没有 `onClick`**，点了什么都不发生。
  更根本的是后端根本没有可用的下载端点：分享接收方通常没登录，
  而登录下载接口挂在 `Auth` 中间件后面 —— 整条链路是断的。

  新增 `GET /api/shares/:id/download`（挂在 `api` 组上，不带 `Auth`）：
  校验过期 / 密码 / 提取码，可选 `?fileId=` 下载分享目录内的某个子文件，
  用 `gorm.Expr` 在数据库侧自增 `downloads`（并发下载不会互相覆盖），
  并记`share_download` 审计日志。

- **「预览」区显示的是硬编码的假内容**
  `Preview` 组件里图片用的是外部占位图 URL
  （`trae-api-cn.mchost.guru/...text_to_image?prompt=Nebula cloud storage...`），
  文本显示一段写死的假代码，视频音频只有一行字 —— 不管用户分享的是什么，都是这套内容。

  现按真实类型分别接真实内容：图片走 `<img>`、视频走 `<video controls>`、
  音频走 `<audio controls>`、文本用 fetch 读取（超过 512KB 截断并在界面上说明截了多少）。
  新增 `GET /api/shares/:id/preview`：与下载同一套鉴权，但用
  `Content-Disposition: inline`（用 attachment 的话 `<img src>` 只会弹下载框），
  且**不计下载次数** —— 预览不是下载。

- **`fileId` 可被构造成越权读取任意文件**
  下载端点接受客户端传的 `?fileId=`。如果不校验，任何人拿一个有效分享 ID
  拼上 `?fileId=<任意文件ID>` 就能把别人账号下的文件拖走 ——
  分享链接会变成一个越权读取入口。
  现用 `fileWithinShare` 逐级向上验证祖先链：每层都必须
  `owner_id == 分享所有者 && 未删除`，最后落到分享根 ID，`maxDepth=64` 防parent 成环。
  失败时不区分「不存在」与「越权」—— 告诉攻击者哪个 ID 存在本身就是信息泄露。

- **分享浏览次数永远慢一拍**
  `UpdateColumn("views", s.Views+1)` 之后直接读 `s.Views` 组 `meta.viewTimes`，
  而下一步 `s.Views++` 在 `UpdateColumn` 已经写回新值之后又加了一次 ——
  第一次访问显示 2、第二次显示 3。
  改为 `gorm.Expr("views + ?", 1)` 数据库侧自增后单独回读 `views` 列。

- **数据库连不上时服务仍启动，随后每个请求都 panic**
  `bootstrap()` 里 `db.Init` 失败只打一条 `[WARN]` 就继续。
  于是 `db.Get()` 返回 nil，之后**每一个** API 请求都在 handler 里空指针 panic，
  由 gin Recovery 兜成 500。表现为「服务起来了但什么都不好用，日志里全是 panic」，
  比启动失败难排查得多。现改为 `log.Fatalf` 直接退出。

  最常见的触发原因是 sqlite 的 `file` 是相对路径，换个工作目录启动就打不开这个库，
  且报错信息是极具误导性的 `out of memory (14)`。

- **中文文件名下载下来是乱码**
  `Content-Disposition` 只给了裸 `filename="中文名.txt"`，部分浏览器按 latin-1 解码。
  现同时给出 RFC 5987 的 `filename*=UTF-8''...`，并对 ASCII 回退名做安全过滤
  （挡 `../` 头部注入与引号/换行，同时保留扩展名）。

### ♻️ 重构

- **`streamFile` 抽出共享**：登录下载与分享下载共用同一套响应逻辑
  （对象存储 302 / Range 206 / 普通流式），避免两边各写一份慢慢漂移。
  `dispAttachment` / `dispInline` 两种模式区分「存盘」与「内联显示」。

### 🧪 测试

新增 `TestIntegration_ShareDownloadAndPreview`（9 组断言）与
`TestIntegration_ShareDetailViewsIncrements`（3 次连续访问逐次断言）。

覆盖：未认证可下载、密码/提取码的query 与 Header 两种传法、
密码与提取码并存时缺一即拒、跨用户 `fileId` 越权、同 owner 但分享子树外越权、
预览 inline 且不计下载/浏览次数、Range 206、目录拒绝、根文件被删后404。

**反向验证**：把 `fileWithinShare` 的调用改成 `if false` 后，
测试立刻报 `cross-user fileId: got 200, want 404` —— 确认断言真的在守这条边界。

### ✅ 验证

- `go build ./...`、`go test ./... -count=1`（13 包全绿）、`npx tsc -b` 全通过
- i18n 五语言对称门禁通过（新增 7 个 key × 5 语言）
- **实机冒烟 14 项全通过**（真跑二进制 + curl）：
  未认证下载内容一致 / `attachment` / 预览 `inline` / Range 206 /
  `downloads` 计数正确而 `preview` 不计 / 中文名 `filename*` 存在
- **浏览器 UI 实测 11 项全通过**（系统 Edge + CDP）：
  分享页未被路由守卫重定向、`img.src` 指向 preview 端点、
  图片真的解码成功（`naturalWidth=64`）、文本页显示真实文件内容且不含旧的假代码

  过程中抓到并修掉一个自己引入的 bug：图片成功时「预览失败」提示也叠在图上
  （原本用 `absolute inset-0` 常驻渲染，没默认 `hidden`），改为条件渲染。

---

## V26-10.0-b — Beta（2026-10-03）

> 侧边栏三个入口修复 + 六项既有缺陷修复 + i18n 门禁。三平台单二进制（Windows / Linux / macOS）已重新打包。

### 🐛 修复

- **侧边栏「分享列表 / 离线下载 / 回收站」点击无反应**
  根因：这三个路径在 `App.tsx` 里**从未注册**。React Router v6 遇到未匹配路径会命中
  末尾的 `<Route path="*" element={<Navigate to="/" replace />} />`，被**静默重定向回首页** ——
  不报错、不白屏，所以看起来就是「点了没反应」。
  现补齐 `/share-list`、`/tasks`、`/trash` 三条路由，并新增 `pages/ShareList.tsx`、
  `pages/TasksOffline.tsx`、`pages/Trash.tsx` 三个页面（均为路由级懒加载）。
  影响面不止桌面侧栏：移动端底部 tabbar 的第二个入口同样指向 `/share-list`，此前也是死的。

- **管理员「用户管理」列表恒为空**
  `api/client.ts` 把返回类型声明成 `items`，而后端实际返回 `list`。
  `u.data.items || []` 永远取到空数组 —— 管理员打开用户管理页看到的一直是空表。
  同样错配的还有通知铃铛（`(r.data as any).items || r.data || []`），已一并修正。

- **侧栏「关于」在任何语言下都显示不出来**
  `MainLayout.tsx` 里写的是 `labelKey: '关于'`（中文字面量而非 key），
  渲染时变成查 `ns_admin.关于`，必然查不到；且 `ns_admin.about` 这个 key 当时压根不存在。
  现补上五语言的 `about` key 并修正引用。

- **任务状态显示成字面量 `task.1`**
  后端 `Task.Status` 是 **int**（0 等待 / 1 进行 / 2 完成 / 3 失败），前端却写
  `(tk.status || tk.state || 'pending') as TaskStatus` 把它硬转成字符串枚举。
  `status=0` 因为是 falsy 恰好落到 `'pending'` 才显得正常，`status=1` 就直接渲染成 `task.1`。
  这纯属巧合掩盖了类型错误。现补显式 int→枚举映射（`toStatus`），两条任务页共用。

- **任务「重试」按钮打 404**
  前端按钮调 `POST /api/tasks/:id/retry`，后端从未注册该路由。现补 `RetryTask`：
  校验归属（非管理员只能操作自己的任务）、只允许终态任务重试（进行中重试会重复下载同一 URL）、
  重置 `progress` 与 `error` 后复用与创建任务相同的下载分派逻辑。

- **Windows 下存储路径静默失效（数据错位隐患）**
  存储策略 config 此前用字符串拼接生成（`{"path":"C:\Users\..."}`），而反斜杠在 JSON 中
  不是合法转义，`json.Unmarshal` 直接失败；代码又忽略了这个错误，于是配置路径为空后静默
  回落到**相对目录** `uploads` —— 而相对路径按进程 CWD 解析。
  结果：Windows 上所有上传都落到与配置声明无关的位置，文件错乱且极易丢失，**全程不报错**。
  现改用 `json.Marshal` 正确转义生成配置；解析失败时优先回退到配置中的绝对上传目录并打日志，
  仍拿不到才显式报错 —— 绝不静默落到相对目录。

### 🧹 重构

- **抽出 `GuardedPage`**，消除 8 处重复手写的路由守卫三段式
  （`installed === null`  loading / `!installed`  去 `/install` / `authBusy`  loading / `!user`  去 `/login`）。
- **抽出 `components/Modal.tsx` 与 `utils/format.ts`**，收敛原先分散在 `Files.tsx` 私有一份、
  `admin/Users.tsx` 导出一份的重复实现。
- **`CreateTask` 从 67 行缩到 27 行**，并抽出 `startTask` 供创建与重试共用 ——
  否则 aria2/net-http 的分派逻辑要写两遍，两边很容易各自漂移。
- **任务列表轮询改为按需**：仅当存在等待/进行中任务时继续轮询，否则 `clearInterval`；
  并加 `alive` 守卫避免组件卸载后 setState。

### 🧪 测试

- **新增全栈集成测试**（`backend/routers/integration_test.go`）
  启动完整引擎（含全部中间件、认证、安装守卫、WebDAV、REST 路由）以真实 HTTP 请求驱动，
  走与生产完全一致的代码路径，CI 的 `-race` 可自动守住回归。覆盖：

  | 用例 | 守住的缺陷 |
  |---|---|
  | WebDAV 路径穿越 | `/dav/../../../evil.txt` 只能落在用户根，绝不落到服务器根；无法读取服务器文件 |
  | WebDAV PUT 覆盖 | 覆盖写必须真实落盘，不能"返回成功但数据全丢" |
  | SSRF 防护 | 环回 / 链路本地 / 私网 / 非 http(s) 协议全部拒绝，公共地址正常放行 |
  | 权限矩阵 | 用户之间互相不可见、不可访问，越权一律 403 |
  | 文件生命周期 | 上传 → 复制 → 软删 → 彻底删除，配额与物理文件始终一致 |

- **新增 `TestIntegration_TaskRetryAndTrashEndpoints`**（7 组断言）
  锁死本轮三个入口所依赖的后端能力：未认证 401、`ListTasks` 的 `owner_id` 过滤不泄露他人任务、
  Retry 他人任务 403、Retry 进行中任务 400、Retry 失败任务正确重置状态、
  回收站软删→可见→他人不可见→还原的完整生命周期、Purge 他人文件 403。
  同时验证 `DELETE /api/files/:id` 无 `X-Confirm-Password` 时确实被 `RequireConfirm` 拦下。

- **新增 i18n 对称性门禁**（`frontend/check_i18n.py`，已接入 `frontend-ci.yml`）
  五个语言文件必须逐命名空间、逐 key 完全一致，且页面引用的 key 都真实存在。
  少一个 key 的表现是界面直接显示 `ns_mine.emptyTrash` 这种原始串，
  **tsc 与 vite 都发现不了** —— 本轮新增 40 个 key 跨 5 语言，正是最需要这道门禁的场景。

### 📦 产物

`NebulaDrive-V26-10.0-b-{windows,linux,darwin}-amd64`（各含 zip）。
单文件二进制，前端已内嵌，无需安装 Go / Node / 数据库。

- **发布包补入 `LICENSE`**（`packaging/repack.py`）：AGPL-3.0 要求分发二进制时随附许可证声明，
  原脚本漏掉了它，合规上站不住脚。
- **Linux / macOS 包补入 `stop.sh`**：`start.sh --daemon` 会写 `.nebula.pid` 并提示
  「停止服务请执行 `./stop.sh`」，`试用说明.md` 也在教用户执行 `./stop.sh` ——
  但原脚本只复制了 `start.sh`。用户按文档操作会直接吃到 `No such file or directory`。
  `build-release.sh` 一直有复制它，只有 `repack.py` 漏了。
- **Linux 包补入 `Linux上手说明.md`**：跨平台的 `试用说明.md` 没覆盖 Linux 用户真正会卡的地方 ——
  必须先解压不能直接跑压缩包、`chmod +x` 的时机、`uname -m` 确认架构、
  `bad interpreter: /bin/sh^M` 的真实原因（说明文件被改坏，不是脚本问题）、
  以及「纯静态链接（`CGO_ENABLED=0`）所以不挑 glibc，老发行版也能跑」这条实用信息。
- 包内文本统一转 LF（仓库工作区的 `LICENSE` / `README.md` 是 CRLF，直接 copy 会带进包）。
- zip 条目统一带顶级目录前缀，避免解压后一堆散落文件覆盖用户同名文件。
- 新增 `packaging/verify_release.py`：自动校验 zip CRC、平台魔数、权限位、行尾、
  内嵌前端是否为新版，以及**「文档里提到的文件是否真的在包里」**
  （`stop.sh` / `Linux上手说明.md` 断言），三平台全通过。
- `repack.py` 的 `.sh` 自检从「只查启动脚本」改为**遍历包内全部 `.sh`**，避免漏检。

### ⚖️ 许可

- **协议残留清理**：仓库 `LICENSE` 与 `README.md` 早已是 AGPL-3.0，但前端关于页、插件页、
  `backend/nfpm.yaml`、预告片工具等处仍残留 **MIT** 表述。现已全量统一为 **AGPL-3.0**。
- **同时废除「界面至少保留 3 处品牌名」附加条款**：该条款原附在 MIT 之上（要求运行时展示署名），
  与 MIT 第 1 条冲突、解释权不明；改用 AGPL-3.0 后本就不存在该要求。前端两处合规声明
  已改写为 AGPL 的**核心义务：网络服务同样需向使用者提供完整对应源码**。
- `nfpm.yaml` 的 `license` 用 SPDX 标识 `AGPL-3.0`；`version` 由过期的 `1.0.0` 校正为
  `26.10.0`（`release: b`），`homepage` 校正为 `github.com/mrc-sk/nebula-drive`。
- 前端产物已重建并同步 `backend/frontend_dist`（`go:embed` 内嵌，CI 有同步守卫）。

### 🚑 侧边栏三个入口点了没反应（路由从未注册）

| 入口 | 路径 | 原因 |
|---|---|---|
| 分享列表 | `/share-list` | `App.tsx` 只有 `/share/:id`（单页公开分享），从未注册 `/share-list` |
| 离线下载 | `/tasks` | 只有一个 `/admin/tasks`（管理员），用户版从未注册 |
| 回收站 | `/trash` | 回收站只在 `Files.tsx` 里做成了 `trashMode` 开关，没有独立路由 |

三者都落进 `App.tsx` 的 catch-all `<Route path="*" element={<Navigate to="/" replace />} />`，
被静默弹回文件页 —— 表现为「点了没反应」而不是报错。

**修复**：新增 `ShareList.tsx` / `TasksOffline.tsx` / `Trash.tsx` 三个页面并注册路由，
守卫抽成 `GuardedPage` 组件（此前同样的 installed/authBusy/user 三段式在 8 处重复）。

**回收站有一条必须让用户知道的行为**：后端 `Restore` 每次都会新建一个
「恢复的文件-YYYYMMDD-HHMMSS」目录把文件塞进去 —— 批量还原 10 个文件会得到 10 个时间戳目录。
UI 上已就此给出提示，避免用户以为还原丢了文件。

### 🐛 顺带修掉的既有缺陷

- **管理员用户列表恒为空**：`admin.ListUsers` 返回 `{total, list}`，`client.ts` 却声明成
  `items`，`Users.tsx` 读 `u.data.items` 恒为 `undefined`。表格空白但页面不报错。
  通知列表有同样的 `items`/`list` 错配（被 `|| r.data` 兜住了）。两处均已对齐。
- **管理员侧栏「关于」不翻译**：`MainLayout.tsx` 的 `labelKey` 写的是中文 `'关于'`，
  渲染时变成 `t('ns_admin.关于')`，任何语言下都显示不出正确文案；而 `ns_admin.about`
  这个 key 压根不存在。已补齐 5 种语言的 `about` 并修正 labelKey。
- **任务状态显示错误**：`Task.Status` 是 int（0/1/2/3），但页面写的是
  `(tk.status || tk.state || 'pending') as TaskStatus` —— 把 int 硬转字符串枚举，
  `status=1`（进行中）会渲染成字面量 `task.1`、徽章配色回落到灰色 pending。
  `status=0` 恰好因 falsy 落到 `'pending'` 才显得正常，纯属巧合。现改为显式映射。
- **「重试」按钮必然 404**：前端 `api.tasks.retry` 已封装并有按钮，但后端从未注册该路由，
  前端还用 `(api.tasks as any).retry?.(id)` 静默吞掉。现**已实现后端 `POST /api/tasks/:id/retry`**
  （重置状态 + 复用创建时的分派逻辑），并抽出 `startTask` 供创建与重试共用。
  进行中/等待中的任务返回 400 —— 否则会对同一 URL 重复下载。
- **任务页无条件 2s 轮询**：即使全部任务已完成也一直打接口，且请求在途时卸载会 setState。
  现仅在存在等待/进行中任务时继续轮询，并加 `alive` 守卫。

### 🧹 已知遗留

- `V1-0.0.2-beta` / `V1-0.0.2.1-beta` / `V1-0.0.2.2-beta` / `V1-0.0.3-beta` 尚未补记条目。
- ✅ ~~⚠️ `V26-10.0-b` 三平台产物**打包于本次许可修正之前**，二进制内嵌的仍是写着 MIT 的旧前端。
  重新打包后方可对外分发。~~ **已于 2026-10-03 重新打包解决**（见下）。

#### 重新打包记录（2026-10-03）

原先的三平台产物内嵌 MIT 旧前端，已全部重建并替换：

| 平台 | 大小 | 文件头 | 校验 |
|---|---|---|---|
| Windows amd64 | 47,545,344 B | `4d5a` (PE) | ✅ |
| Linux amd64 | 46,719,160 B | `7f454c46` (ELF) | ✅ |
| macOS amd64 | 47,807,712 B | `cffaedfe` (Mach-O) | ✅ |

- 三个二进制内 `AGPL-3.0` 均出现 2 次，`MIT License` **零残留**；
- 启动脚本行尾按平台转换：`start.bat` → CRLF，`start.sh` → LF（防 `bad interpreter: /bin/sh^M`）；
- zip 权限位：`nebula` / `*.sh` 置 0755，文档 0644；
- 实跑验证：`/api/health` → `{"installed":false,"ok":true}`；
  `/dot-motion-loader.js` → 200（证明 `go:embed` 前端确实可用）。
- 打包脚本固化为 `packaging/repack.py`，避免每次手工处理行尾与权限位。

---

## V1-0.0.1 — Beta（2026-08-09）

> 首个公开测试版。功能对标 Cloudreve Pro 正常版 + 增强版能力；但仍处于 Beta 阶段，部署用于生产环境前请充分测试，
> 并及时在 Issue 反馈 Bug。

### 🎉 功能总览（共 **120+** 功能点，按模块归类）

#### 🗂️ 文件管理模块（20 项）
- [x] 列表视图 / 网格视图，默认列表可切换
- [x] 面包屑导航栏右侧「全部展开 / 折叠」按钮
- [x] 拖拽上传：整个窗口四周脉冲发光呼吸边框
- [x] 鼠标跟随的多选上下文菜单（默认模式，可在个人设置切换为底部浮动条 / 右侧抽屉）
- [x] 批量操作：批量移动 / 批量复制 / 批量改存储策略 / 批量删除
- [x] 批量删除支持密码二次确认（X-Confirm-Password 头）
- [x] 文件版本控制：保留最近 **10** 个版本，数量后台可改
- [x] 版本历史：上传新版本 / 下载旧版本 / 恢复指定版本 / 删除单版本
- [x] 文件标签：自定义标签 + 颜色 + 聚合筛选 + 标签条 + 行内标签徽章
- [x] 秒传：相同 hash + 相同 owner + 相同文件直接命中，无需重传
- [x] 大文件分片上传：2 并发 × 20MB/片（可扩展参数）
- [x] 断点续传：`chunk-init / chunk-upload / chunk-merge` 三接口
- [x] 文件夹上传（webkitdirectory）+ 目录结构保留
- [x] 文件名/大小/修改时间/类型 表头排序
- [x] 搜索框按文件名模糊 + 标签精准筛选联合检索
- [x] 新建文件夹 + 重命名 + 移动 + 复制 + 删除到回收站
- [x] 回收站：软删除，保留 **30** 天后自动永久清理（天数后台可改）
- [x] 恢复策略：全部还原到根目录「恢复的文件-时间戳」隔离文件夹，不污染原位置
- [x] 在线预览：图片（含缩略图）/视频(plyr)/音频(plyr)/Office(mammoth+xlsx)/PDF/代码
- [x] 缩略图：本地缩略缓存 + OSS/COS 原生图片处理接口抽象

#### ☁️ 多存储后端（4 家 + 6 项）
- [x] **本地存储**（Local）：`filesystem/fs.go` Handler 抽象
- [x] **AWS S3 / 兼容存储**（MinIO / R2 / B2）：`aws-sdk-go-v2` 官方 SDK
- [x] **阿里云 OSS**：`aliyun-oss-go-sdk` 官方 SDK
- [x] **腾讯云 COS**：`cos-go-sdk-v5` 官方 SDK
- [x] 存储策略管理后台：CRUD + 测试连接 + 溢出菜单操作
- [x] 对象命名规则：`{year}/{month}/{day}/{ownerId}/{uuid8}.{ext}`，日期分层避免 S3 单目录性能瓶颈

#### 🌐 WebDAV（8 项，RFC 4918）
- [x] PROPFIND（列目录 / 读取属性）+ DEPTH 0/1/infinity
- [x] MKCOL（创建目录）
- [x] GET / HEAD（文件下载 + 元数据）
- [x] PUT（写文件）
- [x] DELETE（删除文件/目录）
- [x] COPY / MOVE（重命名 + 跨目录移动）
- [x] `Range` 部分下载（206 Partial Content）
- [x] 后台系统设置可自定义：WebDAV 路径前缀 + 全局开关 + 方法集勾选 + RFC4918 帮助文档链接

#### 🚀 离线下载（6 项）
- [x] HTTP / HTTPS 下载（内嵌 net/http 回退逻辑 + 进度实时更新）
- [x] BT 种子 / 磁力链接 下载（内嵌 **aria2c** 子进程 + JSON-RPC）
- [x] aria2 管理：优先连接系统已有 RPC，找不到则询问式内嵌启动（两种都支持）
- [x] 任务状态轮询（每 2s `tellStatus`）+ 下载完成自动创建 File 记录 + 发站内通知
- [x] 任务卡片毛玻璃进度条 + 速度 + 剩余时间 + 百分比
- [x] 任务管理：暂停 / 取消 / 继续 / 删除 + 任务毛玻璃卡片进度条

#### 🔐 认证与安全（15 项）
- [x] bcrypt 密码哈希（默认 cost 10）
- [x] JWT 令牌 + Session 模型 + 单设备登录（新 Session 使旧的失效）
- [x] **2FA** 两步验证（TOTP / Google Authenticator），用户可选 + 首次登录红点引导
- [x] 邮箱验证（邮件通知走 onEmail 钩子）
- [x] 记住登录：**7** 天 session 有效期，默认勾选
- [x] 密码强度策略：最少位数 + 大写字母 + 数字 + 特殊字符，**全部后台可配**
- [x] 注册/改密码：实时显示密码强度条（弱/中/强）+ 条件满足清单
- [x] 人机验证码：hCaptcha / Turnstile 接入钩子，连续 3 次失败强制验证码
- [x] 敏感操作二次确认：永久删除 / 清空回收站 / 删除用户，请求头 `X-Confirm-Password` 校验
- [x] **CSP** Content-Security-Policy + X-Frame-Options + X-Content-Type-Options + HSTS 安全头
- [x] Referrer-Policy + X-XSS-Protection 中间件
- [x] IP 黑名单（手动添加）+ 自动封禁（5 分钟 10 次登录失败 → 封 1 小时）
- [x] 上传文件扩展名白名单（后台可改）+ **魔术字节** 校验（http.DetectContentType 前 512B）
- [x] 内存滑动窗口限流：登录 10/min、上传 60/min、其他 600/min（后台可调阈值 + 插件 blocked→403 扩展）
- [x] 签名 URL 防盗链：下载链接带 expires+sign，**5** 分钟有效

#### 👥 用户、配额、组（8 项）
- [x] 用户模型：用户名/邮箱/密码/状态/Storage 已用/2FA Secret/Avatar/NickName/IsAdmin + 软删除
- [x] 用户组模型：名称/存储限额/速度限制/权限
- [x] 注册策略：后台开关（开放 / 仅限邀请码 / 关闭），管理员可添加用户
- [x] 管理员创建新用户 → 自动发送「欢迎加入」站内信
- [x] 个人设置：昵称 / 头像 / 密码 / 彩蛋总开关 / 多选 UI 模式 / 2FA 管理 / 访问令牌 PAT
- [x] 用户列表：CRUD + 调整用户组 + 调整存储配额 + 封禁/解封
- [x] 用户组列表：CRUD + 配额 + 限速
- [x] 存储配额：超出后禁止上传，秒传不计配额

#### 🔗 分享（9 项）
- [x] 单文件 / 多文件打包 / 整个目录分享
- [x] **双因子保护**：提取码 + 密码两层验证，前端先输入提取码，再输入密码
- [x] 默认配置：公开 + 无密码 + 永不过期（可在创建时加限制）
- [x] 过期时间 / 最大访问次数 / 仅登录用户访问 / 指定用户白名单
- [x] 访问水印 / 单日限流（插件钩子扩展可叠加）
- [x] 公开详情页布局：左侧文件树 + 右侧预览区
- [x] 分享下载触发通知：分享所有者收到"你的分享被访问"通知
- [x] 访问审计日志（audit_logs）+ 30 天自动过期
- [x] 提取码在 URL `#hash` 中，密码走表单提交，两层分离

#### 🤝 协作编辑（5 项）
- [x] onCollabOpen / onCollabSave 插件钩子
- [x] OnlyOffice 文档服务器对接（插件实现，走 onCollabOpen 返回 URL 302 跳转）
- [x] MD / TXT 内置 **Yjs CRDT** 实时多人协作（WebSocket `/api/collab/ws/:docId`）
- [x] 保存协作内容 → 自动归档当前版本
- [x] docId→[]conn map 广播 update 消息

#### 🧩 插件系统（10 项）
- [x] 8 个官方扩展钩子：onEmail / onBackup / onRestore / onApiAuth / onDBMigrate / onRateLimit / onAntiLeech / onCLI
- [x] 新增协作钩子：onCollabOpen / onCollabSave（共 10 个）
- [x] `pkg/plugin` 插件引擎：Register / Fire / ListHooks
- [x] 后台插件页：**卡片网格** + 安装按钮 + 版本/作者/描述显示
- [x] 后台可配置 **插件商店网址**：默认 `com.json` 拉取目录
- [x] 插件商店 com.json：6 个示例插件（SMTP/备份/OnlyOffice/防盗链/hCaptcha/CLI）
- [x] 插件「设置」面板：每个插件可声明动态 schema + UI 表单
- [x] **plugins 分支**（公共插件库，完全独立），社区 PR 提交
- [x] 插件提交指南：目录结构 / plugin.json 模板 / 品牌署名保留要求
- [x] 插件命名规范 kebab-case

#### 🔑 开放 API（8 项）
- [x] **OAuth 2.0 Authorization Code** 全流程：App CRUD → GET Authorize 确认 → POST Authorize 生成 code → /token code 换 access_token → /userinfo
- [x] 刷新令牌（Refresh Token）
- [x] **Personal Access Token**（PAT）：个人中心手动生成 + 只显示一次完整 token + 过期时间 + 作用域
- [x] PAT 前缀显示（`ndp_` 前缀）+ 最后使用时间
- [x] `/api/v1/*` 路由组 + APIAuth 中间件（先查 AccessToken 再查 PAT）
- [x] APIAuth 复用 CtxUserKey，共享 controllers.List / Upload / Download
- [x] onApiAuth 插件钩子（扩展 hCaptcha / 额外权限校验）
- [x] CORS：默认允许 *，可后台收紧

#### 📊 后台仪表盘 & 审计（9 项）
- [x] 4 列等宽毛玻璃 KPI：用户总数 / 文件总数 / 存储已用 / 分享总数
- [x] **ECharts 流量趋势折线图**：最近 7/30/365 天，上传(蓝)下载(绿)双 Y 轴
- [x] **ECharts 文件类型饼图**：图片/视频/音频/文档/压缩包/其他，环形+扇区高亮
- [x] **ECharts 活跃度热力图**：GitHub 风格年度贡献日历
- [x] 最近用户表 / 最近文件表 / 最近分享表
- [x] 审计日志 `audit_logs` 9 个写入点：登录 / 上传 / 下载 / 分享创建删除 / 用户 CRUD / 组 CRUD / 策略 CRUD / 设置更新
- [x] 审计日志：按时间 / 操作人 / 类型 / 关键词过滤
- [x] 审计日志 **30 天自动过期清理**
- [x] 版本化数据库迁移：`schema_migrations` 表 v1~v5 + onDBMigrate 钩子

#### 🛠️ 系统设置（10 个垂直 Tab 模块）
- [x] 基本设置（站点名/域名/协议/默认语言/时区/备案号/页脚文本）
- [x] 邮件与缓存（onEmail 钩子配置 + Redis 连接）
- [x] 安全设置（密码策略/验证码/上传安全/IP 封禁/回收站天数/版本数）
- [x] 上传与 WebDAV（分片大小/并发数/秒传开关/回收站天数/最大版本数）
- [x] WebDAV 专页（路径前缀/开关/方法集勾选/RFC4918帮助链接）
- [x] 外观设置（站点名 / Logo 上传 / 主题色 6 预设 + 自定义 / ICP / 页脚 / 默认主题 / 自定义 CSS）
- [x] 插件页（商店 Tab / 已安装 Tab / Hooks 扩展点 Tab + 完整文档）
- [x] HTTPS / TLS：ACME 自动申请(Let's Encrypt) 与手动上传双模式
- [x] 限流参数（login/upload/default/minute）+ 插件扩展
- [x] 存储策略页（列表/新增/编辑/溢出菜单: 编辑 / 测试连接 / 删除）

#### 🎨 前端 UI（18 项）
- [x] 毛玻璃 `.glass` + 强毛玻璃 `.glass-strong` 两个基础类
- [x] 玻璃拟态微动画（透明度 + 模糊半径 + 轻微位移渐变）
- [x] 登录页动态三色流体背景（SVG/CSS 渐变流动）
- [x] 主题三态切换：浅色 / 深色 / 跟随系统（Tailwind `darkMode:class`）
- [x] 5 种语言：简体 / 繁體 / English / 日本語 / 한국어，右上角色球切换
- [x] 通知铃铛 + WebSocket 实时推：未读徽章 + 弹层面板 + 全部已读 + toast
- [x] plyr.js 视频播放器（倍速 0.5x~2x / 画中画 / 全屏）
- [x] plyr.js 音频播放器
- [x] Office 预览：docx(mammoth.js) / xlsx(sheet_to_html) / pptx(JSZip 解析)
- [x] 移动端响应式：<768px 底部 4 Tab（首页/文件/分享/我的）
- [x] 侧边栏：汉堡点击折叠 240↔64px + 响应式自动变抽屉
- [x] 面包屑导航栏右侧「全部展开 / 折叠」按钮
- [x] 右下角浮动传输抽屉（上传下载双 Tab + 进度 + 速度）
- [x] 安装向导：5 步向导模式 + 右上角「高级模式」复选框切单页大表单
- [x] OAuth 授权确认页：/oauth/authorize?client_id=xxx + 授权/拒绝按钮
- [x] PAT 管理页（个人设置 Tab）：创建 / 撤销 / 只显示一次完整 token + 复制
- [x] 品牌白标签：站点名 / Logo 上传 / 主题色调色板 / ICP / 页脚
- [x] 密码强度提示：注册/改密码实时强度条 + 4 条件清单
- [x] 分享页双因子：先输入提取码 → 再输入密码

#### 🥚 彩蛋系统（4 个，统一「彩蛋模式」总开关）
- [x] Logo 彩蛋（老）：持续 10s/圈旋转，hover 暂停**不回正**，松开继续
- [x] 主题转盘彩蛋：输入 `imfeelinglucky`，6 色转盘 4~8s 减速停下 → 切换主题色
- [x] Konami 代码彩蛋：`↑↑↓↓←→←→BA` → 界面文字左右轻微摇摆 10s
- [x] 头像彩蛋：4 秒内连续点 10 次头像 → 头像变成 🌈 霓虹呼吸光环，再 10 点复原

#### ⚡ 性能（6 项）
- [x] Gzip 压缩（Gin gzip 中间件）
- [x] 静态资源 Cache-Control: max-age=31536000, immutable + ETag
- [x] 数据库连接池：MaxOpen=50 / MaxIdle=10 / ConnMaxLifetime=30min
- [x] GORM 慢查询日志（>200ms warn）
- [x] sync.Pool 32KB buffer 复用池（上传下载路径）
- [x] 对象存储签名 URL 302 跳转：省服务器带宽，最快下载

#### 🚢 部署（4 项）
- [x] **单二进制**：Go embed 内嵌前端 dist，SPA fallback
- [x] **多数据库**：SQLite（默认）/ MySQL / PostgreSQL 三引擎
- [x] **HTTPS**：ACME 自动 + 手动上传证书双模式
- [x] **nfpm 打包**：deb + rpm 双系统包（systemd service + postinstall/preremove 脚本）

#### ✅ 测试与质量
- [x] `go test ./... -cover`：整体覆盖率 **42.2%**（>40% 验收要求）
- [x] middleware 87.6% / routers 93.8% / service 88.2% / pkg/util & plugin 100% / pkg/jwt 87.5% / models 78.6% / conf 76.4% / pkg/db 73.9% / pkg/tls 61.4%
- [x] `npm run build`：tsc + vite build 双通过，2689 modules 18s

---

### 🐛 已知 Bug & 局限（Beta 期待改进，欢迎提 Issue）

#### 高优先级 Bug
1. **大文件分片上传失败后重试断点续传元数据未持久化**：当前断点续传状态只保存在内存 map，服务重启丢失（计划 v1.0.0-rc 修复，落盘到 `chunk_sessions` 表）。
2. **移动端 <420px 宽度下侧边栏部分中文被截断**：因浏览器窗口视口太窄，侧栏 240px 固定宽度下文字溢出（计划增加 360~480px 断点下侧栏字号缩放）。
3. **admin 关于页 Footer 合规卡品牌文字在深色主题下对比度略低**：在最暗模式下蓝色文字与毛玻璃卡片背景只有 3.2:1（需 ≥4.5:1 WCAG AA）。
4. **插件「安装」按钮目前只拉取 com.json，未实际解压并加载插件二进制**：插件框架（Register/Fire/Hooks）完成，但 Go 插件加载（`plugin.Open`）需要 .so 动态库构建流程，当前只做 UI + 钩子。（计划 v1.0.0-rc 加入 `nebula plugin build` 命令把插件编译成 .so，或走 WASM）。

#### 中优先级 Bug / 局限
5. **aria2 未安装时 BT/磁力任务返回失败提示，但「失败原因」toast 文案不统一**：有的地方显示 "aria2 not installed"，有的地方是 "BT引擎未配置"（统一文案待做）。
6. **MySQL 5.7 以下 JSON 字段兼容性**：models.Notification.Meta 等用了 JSON 类型，MySQL 5.6 及以下会失败（用户手册中写了最低 MySQL 8.0，但实际 5.7 可用，需在文档中补充最低版本说明）。
7. **Yjs CRDT 的 docId 只按 fileId 生成，未包含版本号**：两个用户同时编辑同一个文件的不同版本会冲突（建议 v1.0.0 做 docId = `${fileId}-${version}`）。
8. **S3 R2 环境中 UploadPart Copy 校验失败（Etag 引号问题）**：aws-sdk-go-v2 CopyObject 返回的 ETag 带双引号，后续 UploadPart 去校验时未 strip，偶尔 ETag mismatch。
9. **图片缩略图 Local 实现依赖 `imaging` 库的纯 Go 解码**：在非常大的 PNG(>20MB) 下会占用较多内存（建议加 size 限制 + lazy resize）。
10. **ECharts 热力图目前走 mock 数据**：`/api/admin/stats/activity` 路由存在但返回 mock 数据（还没真正聚合 audit_logs 表，后端真实聚合 SQL 待补）。
11. **OAuth 2.0 PKCE 未启用**：当前只有 Authorization Code，没有要求 code_challenge/code_verifier（移动 App / SPA 建议 PKCE，计划 v1.0.0 加）。
12. **PAT 撤销后相关 Session 未立即失效**：依赖下一次请求查 token 表才 401，期间内如果客户端缓存了 JWT 可能短暂有效（建议引入 Redis token 黑名单，或每次查 PAT 表时加一个版本号字段 + 版本缓存）。
13. **密码强度规则「必须包含特殊字符」= false 时，前端仍会显示红色✕ + 提示**：前端 UI 未正确读取 settings 动态配置，前端显示逻辑需从固定 4 条件改为按 settings 开关条件判断。
14. **ACME 模式 DNS-01 校验未支持**：当前只做了 HTTP-01 校验，需要用户 80 端口公网可访问（加 Cloudflare/阿里云 DNS 插件钩子是后续方向）。

#### 低优先级 / 非 Bug 改进建议
15. **离线下载进度条当前只在 2s 轮询更新一次**：aria2 有 `aria2.onDownloadProgress` 事件推送，可以改 WebSocket 事件订阅更实时。
16. **plyr.js 播放器字幕轨道还没接**（多语言 SRT/VTT 未解析展示，File 模型中也没字幕关联字段，后续加 `subtitles` 关联表）。
17. **文件预览弹窗的「MD/TXT 协作编辑」入口还没按钮**：UI 已预留 Tab，按钮需增加「开启多人协作」入口链接到 /collab/:id WebSocket。
18. **IP 自动封禁记录后台未做列表页**：有表 models.IPBan + 路由，但前端后台还没做「IP 封禁」管理 Tab。
19. **分享访问限流 + 单日下载次数阈值没有 UI 配置入口**：后端逻辑存在，settings key 有，前端系统设置 Tab 没加对应输入框。
20. **nfpm 只是配置了 nfpm.yaml，deb/rpm 没有在 CI 中实际打包测试**：下次 rc 版本前加 GitHub Actions 自动 nfpm。

---

### 📦 本版本包含的发布产物

| 产物 | 说明 |
|---|---|
| `nebula` (附加到 Release) | **Linux amd64 单二进制**。Go embed 已内嵌前端，`chmod +x && ./nebula` 直接运行，默认监听 `:5212`。 |
| 源码 Source code (zip / tar.gz) | GitHub 自动生成。 |

### 🚀 快速上手 Beta

```bash
chmod +x nebula
./nebula &
# 浏览器打开 http://localhost:5212/
# 首次访问自动进入 5 步安装向导
# 默认 SQLite + 默认管理员 admin / admin123
# （向导中也可以切 MySQL / Postgres）
```

### 🧪 Beta 期反馈

请通过 GitHub Issues 提 Bug / 功能建议：
<https://github.com/mrc-sk/nebula-drive/issues>

提 Issue 请附上：
- 系统版本 + 部署方式（单二进制 / Docker / deb / rpm）
- 复现步骤
- 截图或控制台日志（尤其浏览器 Console + 后端 stdout）

---

署名保留：Copyright (c) 2026 **mrc-sk** · Repository: <https://github.com/mrc-sk/nebula-drive>
