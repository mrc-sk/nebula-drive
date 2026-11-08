package plugin

// 本文件只放钩子常量与元信息表；派发逻辑见 hooks.go。
// 拆分的原因是这张表会随社区需求持续增长，单独成文件便于 review 与生成文档。

// HookName 钩子名。v1 的 10 个名字永久保留，不得重命名或改语义。
type HookName string

// HookGroup 钩子分组，前端按此分组展示。
type HookGroup string

const (
	GroupSystem  HookGroup = "system"  // 系统生命周期
	GroupRequest HookGroup = "request" // HTTP 请求边界
	GroupAuth    HookGroup = "auth"    // 认证与会话
	GroupUser    HookGroup = "user"    // 用户与权限
	GroupFile    HookGroup = "file"    // 文件全生命周期
	GroupContent HookGroup = "content" // 内容治理
	GroupShare   HookGroup = "share"   // 分享
	GroupTask    HookGroup = "task"    // 离线下载
	GroupWebDAV  HookGroup = "webdav"  // WebDAV
	GroupStorage HookGroup = "storage" // 存储与策略
	GroupCollab  HookGroup = "collab"  // 协作编辑
	GroupNotify  HookGroup = "notify"  // 通知与邮件
	GroupBilling HookGroup = "billing" // 套餐与付费
	GroupAudit   HookGroup = "audit"   // 审计与统计
	GroupBackup  HookGroup = "backup"  // 备份与恢复
	GroupAPI     HookGroup = "api"     // 开放 API 与 CLI
	GroupPlugin  HookGroup = "plugin"  // 插件自身生命周期
	GroupLimit   HookGroup = "limit"   // 限流
	GroupUI      HookGroup = "ui"      // UI 扩展点（侧边栏/主题/设置页）
	GroupAI      HookGroup = "ai"      // AI 智能（补全/推荐/摘要/问答/网关）
)

// GroupDocs 分组中文名。
var GroupDocs = map[HookGroup]string{
	GroupSystem: "系统生命周期", GroupRequest: "HTTP 请求", GroupAuth: "认证与会话",
	GroupUser: "用户与权限", GroupFile: "文件管理", GroupContent: "内容治理",
	GroupShare: "分享", GroupTask: "离线下载", GroupWebDAV: "WebDAV",
	GroupStorage: "存储与策略", GroupCollab: "协作编辑", GroupNotify: "通知与邮件",
	GroupBilling: "套餐与付费", GroupAudit: "审计与统计", GroupBackup: "备份与恢复",
	GroupAPI: "开放 API 与 CLI", GroupPlugin: "插件生命周期", GroupLimit: "限流",
	GroupUI: "UI 扩展点", GroupAI: "AI 智能",
}

// ---- v1 冻结钩子（名字与语义不得变更） ----

const (
	HookEmail      HookName = "onEmail"
	HookBackup     HookName = "onBackup"
	HookRestore    HookName = "onRestore"
	HookApiAuth    HookName = "onApiAuth"
	HookDBMigrate  HookName = "onDBMigrate"
	HookRateLimit  HookName = "onRateLimit"
	HookAntiLeech  HookName = "onAntiLeech"
	HookCLI        HookName = "onCLI"
	HookCollabOpen HookName = "onCollabOpen"
	HookCollabSave HookName = "onCollabSave"
)

// ---- v2 新增钩子 ----

const (
	// 系统生命周期
	HookBoot          HookName = "onBoot"
	HookShutdown      HookName = "onShutdown"
	HookConfigChanged HookName = "onConfigChanged"
	HookScheduledTick HookName = "onScheduledTick"
	HookHealthCheck   HookName = "onHealthCheck"

	// HTTP 请求边界
	HookRequestBefore  HookName = "onRequestBefore"
	HookRequestAfter   HookName = "onRequestAfter"
	HookOpenAPIRequest HookName = "onOpenAPIRequest"

	// 认证与会话
	HookAuthBeforeLogin     HookName = "onAuthBeforeLogin"
	HookAuthProviderResolve HookName = "onAuthProviderResolve"
	HookAuthLoginSuccess    HookName = "onAuthLoginSuccess"
	HookAuthLoginFailed     HookName = "onAuthLoginFailed"
	HookAuthLogout          HookName = "onAuthLogout"
	HookAuthRegisterBefore  HookName = "onAuthRegisterBefore"
	HookAuthRegisterAfter   HookName = "onAuthRegisterAfter"
	HookAuthPasswordChange  HookName = "onAuthPasswordChange"
	HookAuth2FAVerify       HookName = "onAuth2FAVerify"
	HookAuthTokenIssue      HookName = "onAuthTokenIssue"

	// 用户与权限
	HookPermissionCheck HookName = "onPermissionCheck"
	HookUserCreate      HookName = "onUserCreate"
	HookUserUpdate      HookName = "onUserUpdate"
	HookUserDelete      HookName = "onUserDelete"
	HookUserQuotaChange HookName = "onUserQuotaChange"
	HookGroupChange     HookName = "onGroupChange"

	// 文件全生命周期
	HookFileUploadBefore   HookName = "onFileUploadBefore"
	HookFileUploadAfter    HookName = "onFileUploadAfter"
	HookFileUploadFailed   HookName = "onFileUploadFailed"
	HookFileRapidBefore    HookName = "onFileRapidBefore"
	HookFileChunkInit      HookName = "onFileChunkInit"
	HookFileChunkComplete  HookName = "onFileChunkComplete"
	HookFileDownloadBefore HookName = "onFileDownloadBefore"
	HookFileDownloadAfter  HookName = "onFileDownloadAfter"
	HookFilePreview        HookName = "onFilePreview"
	HookFileThumb          HookName = "onFileThumb"
	HookFileRename         HookName = "onFileRename"
	HookFileMove           HookName = "onFileMove"
	HookFileCopy           HookName = "onFileCopy"
	HookFileDelete         HookName = "onFileDelete"
	HookFileRestore        HookName = "onFileRestore"
	HookFilePurge          HookName = "onFilePurge"
	HookFileVersionCreate  HookName = "onFileVersionCreate"
	HookFileVersionRestore HookName = "onFileVersionRestore"
	HookFileTagChange      HookName = "onFileTagChange"

	// 内容治理
	HookFileTypePolicy   HookName = "onFileTypePolicy"
	HookContentScan      HookName = "onContentScan"
	HookContentHashCheck HookName = "onContentHashCheck"
	HookContentReport    HookName = "onContentReport"

	// 分享
	HookShareCreateBefore   HookName = "onShareCreateBefore"
	HookShareCreateAfter    HookName = "onShareCreateAfter"
	HookShareAccess         HookName = "onShareAccess"
	HookShareVerify         HookName = "onShareVerify"
	HookShareDownloadBefore HookName = "onShareDownloadBefore"
	HookShareDelete         HookName = "onShareDelete"
	HookShareExpire         HookName = "onShareExpire"

	// 离线下载
	HookTaskCreateBefore HookName = "onTaskCreateBefore"
	HookTaskCreateAfter  HookName = "onTaskCreateAfter"
	HookTaskComplete     HookName = "onTaskComplete"
	HookTaskFailed       HookName = "onTaskFailed"
	HookTaskCancel       HookName = "onTaskCancel"

	// WebDAV
	HookWebDAVRequest  HookName = "onWebDAVRequest"
	HookWebDAVPropfind HookName = "onWebDAVPropfind"
	HookWebDAVPut      HookName = "onWebDAVPut"
	HookWebDAVDelete   HookName = "onWebDAVDelete"
	HookWebDAVLock     HookName = "onWebDAVLock"

	// 存储与策略
	HookStorageResolve      HookName = "onStorageResolve"
	HookStorageBeforeWrite  HookName = "onStorageBeforeWrite"
	HookStorageAfterWrite   HookName = "onStorageAfterWrite"
	HookStorageBeforeRead   HookName = "onStorageBeforeRead"
	HookStoragePolicyChange HookName = "onStoragePolicyChange"

	// 协作编辑
	HookCollabConnect    HookName = "onCollabConnect"
	HookCollabDisconnect HookName = "onCollabDisconnect"

	// 通知与邮件
	HookNotificationSend HookName = "onNotificationSend"
	HookNotificationRead HookName = "onNotificationRead"

	// 套餐与付费
	HookCodeRedeemBefore HookName = "onCodeRedeemBefore"
	HookCodeRedeemAfter  HookName = "onCodeRedeemAfter"
	HookPlanSubscribe    HookName = "onPlanSubscribe"
	HookPlanExpire       HookName = "onPlanExpire"
	HookPaymentCallback  HookName = "onPaymentCallback"

	// 审计与统计
	HookAuditLog       HookName = "onAuditLog"
	HookStatsCollect   HookName = "onStatsCollect"
	HookDashboardQuery HookName = "onDashboardQuery"

	// 插件自身生命周期
	HookPluginInstall      HookName = "onPluginInstall"
	HookPluginEnable       HookName = "onPluginEnable"
	HookPluginDisable      HookName = "onPluginDisable"
	HookPluginConfigChange HookName = "onPluginConfigChange"

	// UI 扩展点（侧边栏/主题/设置页被访问或渲染时）
	HookUIPageOpen     HookName = "onUIPageOpen"
	HookUIThemeApply   HookName = "onUIThemeApply"
	HookUISettingsSave HookName = "onUISettingsSave"
	HookUIPageRegister HookName = "onUIPageRegister"

	// AI 智能（v2 增量，协议版本不变；v1 插件无感）
	HookAIRequest         HookName = "onAIRequest"
	HookAIResponse        HookName = "onAIResponse"
	HookAIProviderResolve HookName = "onAIProviderResolve"
	HookAIComplete        HookName = "onAIComplete"
	HookAIRecommend       HookName = "onAIRecommend"
	HookAISearch          HookName = "onAISearch"
	HookAISummary         HookName = "onAISummary"
	HookAIAsk             HookName = "onAIAsk"
	HookAIConfigChange    HookName = "onAIConfigChange"
)

// ---- 钩子元信息表 ----

// HookMode 决定宿主如何派发该钩子。
type HookMode int

const (
	// ModeAsync 派发后不等待结果，仅用于通知型钩子。
	ModeAsync HookMode = iota
	// ModeSync 派发后阻塞等待插件返回，用于决策型/变换型钩子。
	ModeSync
)

func (m HookMode) String() string {
	if m == ModeSync {
		return "sync"
	}
	return "async"
}

// HookMeta 描述一个钩子的静态信息。
type HookMeta struct {
	Name  string    `json:"name"`
	Group HookGroup `json:"group"`
	Doc   string    `json:"doc"`
	Mode  HookMode  `json:"-"`
	// Blockable 该钩子的 error/decision 能否拦截请求。
	Blockable bool `json:"blockable"`
	// Mutatable 插件能否通过 CtxPatch 回写 ctx 改变业务行为。
	// 为 true 时必须是 ModeSync（异步派发不会回写）。
	Mutatable bool `json:"mutatable"`
	// Wired 该钩子是否已接入业务代码。
	// 由 hooks_wiring_test.go 静态校验：Wired=true 却找不到调用点会直接 FAIL，
	// 因此这个字段不再能靠手工标注变成装饰品。
	Wired bool `json:"wired"`
	// Since 引入该钩子的协议版本（1 = v1 冻结钩子，2 = 本次新增）。
	Since int `json:"since"`
	// Payload ctx 中保证存在的字段（供插件作者与前端展示）。
	Payload []string `json:"payload"`
}

// 简写：让下表保持一行一个钩子，完整文档见 docs/插件钩子完全指南.md。
func sm(g HookGroup, doc string) HookMeta {
	return HookMeta{Group: g, Mode: ModeAsync, Wired: true, Since: 2, Doc: doc}
}
func syncM(g HookGroup, block, mutate bool, doc string) HookMeta {
	return HookMeta{Group: g, Mode: ModeSync, Blockable: block, Mutatable: mutate, Wired: true, Since: 2, Doc: doc}
}
func v1(g HookGroup, mode HookMode, block, mutate bool, doc string) HookMeta {
	return HookMeta{Group: g, Mode: mode, Blockable: block, Mutatable: mutate, Wired: true, Since: 1, Doc: doc}
}

// Hooks 是全部钩子的元信息表，顺序即前端展示顺序。
//
// 同步钩子只放在低频决策点；下载/预览等高频路径虽标 ModeSync，
// 业务侧一律走 internal/hooks.Decide（内部用 FireIf 轻量门）——
// 没有插件订阅时零 IPC、零等待。
var Hooks = func() []HookMeta {
	type row struct {
		name HookName
		meta HookMeta
	}
	rows := []row{
		// ---------------- 系统生命周期 ----------------
		{HookBoot, sm(GroupSystem, "bootstrap 完成后触发（DB/迁移/JWT/插件宿主均就绪）。每次启动一次。ctx={version,protocolVersion,dataDir,siteURL}")},
		{HookShutdown, syncM(GroupSystem, false, false, "收到退出信号后、宿主关闭前触发。插件应在此 flush 缓冲。ctx={reason}")},
		{HookConfigChanged, sm(GroupSystem, "后台保存站点设置后触发。ctx={operatorId,operatorName,changed}")},
		{HookDBMigrate, v1(GroupSystem, ModeAsync, false, false, "AutoMigrate 与版本化迁移完成后触发，每次启动都会触发。插件建表必须幂等。ctx={fromVersion,toVersion}")},
		{HookScheduledTick, sm(GroupSystem, "宿主定时心跳（每 5 分钟）。插件可据此做周期任务，无需另起进程。ctx={tick,at}")},
		{HookHealthCheck, syncM(GroupSystem, false, true, "GET /api/health 时触发。写 ctx.checks 追加健康项，写 ctx.healthy=false 可返回 503。")},

		// ---------------- HTTP 请求边界 ----------------
		{HookRequestBefore, syncM(GroupRequest, true, true, "所有 /api 请求进入业务前触发。可拦截（WAF/维护模式）、可改写上下文。ctx={method,path,ip,ua,userId,userName,requestId}")},
		{HookRequestAfter, sm(GroupRequest, "请求完成后触发。ctx={method,path,status,durationMs,userId,requestId}")},
		{HookOpenAPIRequest, syncM(GroupRequest, true, false, "/api/v1/* 开放接口专用，APIAuth 之后触发。ctx={method,path,clientId,userId,scope,ip}")},

		// ---------------- 认证与会话 ----------------
		{HookAuthBeforeLogin, syncM(GroupAuth, true, true, "校验密码之前触发。可拦截；写 ctx.provider 切换注册型认证源、写 ctx.username 改写登录名。")},
		{HookAuthProviderResolve, syncM(GroupAuth, false, true, "注册型(auth)：外部认证源校验。插件写 ctx.ok=true 放行，可附 ctx.claims 注入身份信息。")},
		{HookAuthLoginSuccess, sm(GroupAuth, "登录成功、会话建立后触发。ctx={userId,userName,ip,ua}")},
		{HookAuthLoginFailed, sm(GroupAuth, "登录失败触发（含被钩子拦截）。ctx 不含密码。ctx={username,ip,ua,reason}")},
		{HookAuthLogout, sm(GroupAuth, "登出触发，可联动外置 SSO 单点登出。ctx={userId,userName,ip}")},
		{HookAuthRegisterBefore, syncM(GroupAuth, true, true, "注册写入前触发。可拦截；写 ctx.email/ctx.groupId 实现自动分组。")},
		{HookAuthRegisterAfter, sm(GroupAuth, "注册成功后触发。用于欢迎邮件、初始空间分配。ctx={userId,userName,email}")},
		{HookAuthPasswordChange, sm(GroupAuth, "修改密码成功后触发。ctx 不含任何密码或哈希。")},
		{HookAuth2FAVerify, syncM(GroupAuth, true, false, "2FA 设置/验证时触发，stage=setup|confirm|login。可拦截以接入硬件密钥。")},
		{HookAuthTokenIssue, syncM(GroupAuth, false, true, "JWT 签发前触发。写 ctx.claims 注入自定义 claim、改 ctx.ttl 调有效期。")},

		// ---------------- 用户与权限 ----------------
		{HookPermissionCheck, syncM(GroupUser, true, false, "鉴权中间件通过身份校验后触发。可拦截实现 ABAC/部门级权限。高频，走轻量门。")},
		{HookUserCreate, sm(GroupUser, "管理员创建用户后触发。ctx={operatorId,userId,userName,groupId}")},
		{HookUserUpdate, sm(GroupUser, "管理员修改用户后触发。ctx={operatorId,userId,changed}")},
		{HookUserDelete, sm(GroupUser, "管理员删除用户后触发，可做外部账号回收。")},
		{HookUserQuotaChange, syncM(GroupUser, false, true, "计算有效配额时触发。写 ctx.maxStorage 覆盖额度（对接外部计费）。source=plan|group|fallback")},
		{HookGroupChange, sm(GroupUser, "用户组增删改后触发，action=create|update|delete。")},

		// ---------------- 文件全生命周期 ----------------
		{HookFileTypePolicy, syncM(GroupContent, false, true, "扩展名白名单判定时触发。写 ctx.allowed 覆盖判定，实现按用户/目录的类型策略。")},
		{HookFileUploadBefore, syncM(GroupFile, true, true, "上传落盘前触发。可拦截；写 ctx.fileName 重命名、ctx.policyId 改存储策略、ctx.parentId 改归档位置。HTTP/分片/WebDAV/开放 API 共用。")},
		{HookFileUploadAfter, syncM(GroupFile, true, true, "文件落盘后触发。同步可拦截：deny 时宿主回滚记录与引用，用于病毒扫描不通过即拒绝。可写 ctx.tags 自动打标签。")},
		{HookFileUploadFailed, sm(GroupFile, "上传失败（校验/落盘/被拦截）后触发。ctx={userId,fileName,size,reason}")},
		{HookFileRapidBefore, syncM(GroupFile, true, false, "秒传命中前触发。可拦截以禁用特定用户/哈希的秒传。")},
		{HookFileChunkInit, syncM(GroupFile, true, false, "分片上传会话创建前触发。可拦截大文件、限制分片数。")},
		{HookFileChunkComplete, sm(GroupFile, "分片合并完成后触发。mergedSize 是实际落盘字节数（非客户端声明值）。")},
		{HookFileDownloadBefore, syncM(GroupFile, true, true, "登录用户下载前触发。可拦截；写 ctx.fileName 改下载名、ctx.headers 注入响应头。高频，走轻量门。")},
		{HookAntiLeech, v1(GroupFile, ModeSync, true, false, "【v1 兼容】登录用户下载前防盗链，error 含 \"blocked\" 即 403。语义不变；新插件建议用 onFileDownloadBefore。公开分享下载不走本钩子。")},
		{HookFileDownloadAfter, sm(GroupFile, "下载响应结束后触发，bytes 为实际发送字节数。用于流量计费。")},
		{HookFilePreview, syncM(GroupFile, true, true, "在线预览前触发。注册型(preview)：写 ctx.redirect 接管预览，宿主 302 过去。")},
		{HookFileThumb, sm(GroupFile, "缩略图生成/命中后触发。ctx={fileId,fileName,width,height,cacheHit}")},
		{HookFileRename, syncM(GroupFile, true, true, "重命名前触发。写 ctx.newName 可规范化命名（去敏感词、加前缀）。")},
		{HookFileMove, syncM(GroupFile, true, false, "移动前触发。可拦截实现「禁止移出部门目录」。")},
		{HookFileCopy, syncM(GroupFile, true, false, "批量复制前触发，ctx.fileIds 为 ID 列表。")},
		{HookFileDelete, syncM(GroupFile, true, false, "删除（移入回收站）前触发。可拦截实现合规保留期锁定。")},
		{HookFileRestore, sm(GroupFile, "从回收站恢复后触发。")},
		{HookFilePurge, syncM(GroupFile, true, false, "彻底删除（Purge/回收站自动清理）前触发。可拦截实现法务保全。")},
		{HookFileVersionCreate, sm(GroupFile, "新版本归档后触发。ctx={userId,fileId,version,size}")},
		{HookFileVersionRestore, syncM(GroupFile, true, false, "恢复历史版本前触发，可拦截实现版本审批。")},
		{HookFileTagChange, sm(GroupFile, "标签增删改后触发，action=add|remove|update。")},

		// ---------------- 内容治理 ----------------
		{HookContentScan, syncM(GroupContent, true, true, "注册型(contentScan)：上传完成后、对外可见前触发。写 ctx.verdict=\"reject\" 即拦截并回滚；可写 ctx.labels/ctx.tags 回填分类。")},
		{HookContentHashCheck, syncM(GroupContent, true, false, "计算完哈希后触发。可拦截实现违规文件哈希黑名单。")},
		{HookContentReport, sm(GroupContent, "用户提交举报（POST /api/reports）后触发。宿主只落库不做判定。")},

		// ---------------- 分享 ----------------
		{HookShareCreateBefore, syncM(GroupShare, true, true, "创建分享前触发。可拦截；改写 ctx.expireAt/ctx.extractCode 强制站点级分享策略。")},
		{HookShareCreateAfter, sm(GroupShare, "分享创建成功后触发，ctx.url 为完整分享链接。")},
		{HookShareAccess, syncM(GroupShare, true, false, "访问公开分享页时触发（未认证）。可拦截实现地区限制、风控。")},
		{HookShareVerify, syncM(GroupShare, true, true, "校验提取码/密码时触发。写 ctx.ok 放行自定义凭证或额外拒绝。ctx 不含明文密码。")},
		{HookShareDownloadBefore, syncM(GroupShare, true, true, "公开分享下载/预览前触发，补上 v1 onAntiLeech 覆盖不到的公开分享盲区（防盗链/限速/次数）。")},
		{HookShareDelete, sm(GroupShare, "分享删除后触发。")},
		{HookShareExpire, sm(GroupShare, "定时心跳检测到分享过期时触发。")},

		// ---------------- 离线下载 ----------------
		{HookTaskCreateBefore, syncM(GroupTask, true, true, "离线下载任务创建前触发。可拦截（URL 黑名单/BT 禁用）；写 ctx.url 做镜像改写。宿主出站 IP 校验在其后执行。")},
		{HookTaskCreateAfter, sm(GroupTask, "任务入库后触发。")},
		{HookTaskComplete, sm(GroupTask, "离线下载完成并归档为文件后触发。")},
		{HookTaskFailed, sm(GroupTask, "任务失败后触发。ctx={userId,taskId,reason}")},
		{HookTaskCancel, sm(GroupTask, "任务取消后触发。")},

		// ---------------- WebDAV ----------------
		{HookWebDAVRequest, syncM(GroupWebDAV, true, false, "每个 WebDAV 方法进入前触发。可拦截实现方法级白名单、只读模式。")},
		{HookWebDAVPropfind, syncM(GroupWebDAV, false, true, "PROPFIND 组装响应前触发。写 ctx.props 注入自定义 DAV 属性。")},
		{HookWebDAVPut, syncM(GroupWebDAV, true, false, "WebDAV PUT 写入前触发（协议层，可见原始 DAV 路径）。")},
		{HookWebDAVDelete, syncM(GroupWebDAV, true, false, "WebDAV DELETE 前触发。")},
		{HookWebDAVLock, syncM(GroupWebDAV, true, false, "LOCK/UNLOCK 前触发，operation=lock|unlock。可拦截实现外置锁协调。")},

		// ---------------- 存储与策略 ----------------
		{HookStorageResolve, syncM(GroupStorage, false, true, "注册型(storage)：filesystem.New 遇到未知策略类型时触发。写 ctx.handled=true + ctx.plugin 让宿主改走代理读写。")},
		{HookStorageBeforeWrite, syncM(GroupStorage, true, false, "物理对象写入前触发。可拦截实现写入审计、加密前置。")},
		{HookStorageAfterWrite, sm(GroupStorage, "物理对象写入后触发。用于跨区复制、WORM 归档。")},
		{HookStorageBeforeRead, syncM(GroupStorage, true, false, "物理对象读取前触发。可拦截实现冷存储解冻、按用户限流。")},
		{HookStoragePolicyChange, sm(GroupStorage, "存储策略增删改后触发，action=create|update|delete。")},

		// ---------------- 协作编辑 ----------------
		{HookCollabOpen, v1(GroupCollab, ModeSync, false, true, "【v1 兼容】打开协作编辑时触发。写 ctx.url 则 302 跳转（OnlyOffice 等）。v2 语义不变。")},
		{HookCollabSave, v1(GroupCollab, ModeAsync, false, false, "【v1 兼容】保存协作内容时触发，仅通知。content 是完整明文，大文件占内存。")},
		{HookCollabConnect, sm(GroupCollab, "协作 WebSocket 建立连接后触发。")},
		{HookCollabDisconnect, sm(GroupCollab, "协作 WebSocket 断开后触发。")},

		// ---------------- 通知与邮件 ----------------
		{HookEmail, v1(GroupNotify, ModeAsync, false, false, "【v1 兼容】创建站内通知时触发，邮箱为空则不触发。系统不含发信实现，此钩子是邮件外发入口之一。")},
		{HookNotificationSend, syncM(GroupNotify, false, true, "注册型(notify)：站内通知落库前触发。写 ctx.channel 切渠道、ctx.delivered=true 表示已由插件投递。")},
		{HookNotificationRead, sm(GroupNotify, "通知标记已读后触发。")},

		// ---------------- 套餐与付费 ----------------
		{HookCodeRedeemBefore, syncM(GroupBilling, true, true, "兑换码核销前触发。可拦截；写 ctx.planId 改写实际到账套餐。")},
		{HookCodeRedeemAfter, sm(GroupBilling, "兑换成功后触发。")},
		{HookPlanSubscribe, sm(GroupBilling, "用户订阅生效后触发，source=redeem|payment|admin。")},
		{HookPlanExpire, sm(GroupBilling, "定时心跳检测到订阅过期时触发。")},
		{HookPaymentCallback, syncM(GroupBilling, true, true, "注册型(payment)：POST /api/pay/callback/:provider 命中时触发。插件验签并写 ctx.status/userId/planId，宿主据此开通。")},

		// ---------------- 审计与统计 ----------------
		{HookAuditLog, sm(GroupAudit, "写操作（POST/PUT/DELETE /api）完成后触发。用于审计外送、SIEM 对接。")},
		{HookStatsCollect, syncM(GroupAudit, false, true, "后台统计取数时触发。插件可写 ctx.traffic/fileTypes/activity 提供数据 —— 这是让三个图表真正有数据的实现方式。")},
		{HookDashboardQuery, syncM(GroupAudit, false, true, "后台 KPI 查询时触发。写 ctx.cards 追加自定义指标卡片。")},

		// ---------------- 备份与恢复（v1 占位 → v2 真接线） ----------------
		{HookBackup, v1(GroupBackup, ModeSync, false, true, "POST /api/admin/backup 触发。注册型(backup)：插件导出到 ctx.target 并写 ctx.result。宿主不做备份实现，只鉴权+审计。")},
		{HookRestore, v1(GroupBackup, ModeSync, true, true, "POST /api/admin/restore 触发。插件从 ctx.source 恢复并写 ctx.result。dryRun=true 只校验不写入。需二次确认。")},

		// ---------------- 开放 API 与 CLI（v1 占位 → v2 真接线） ----------------
		{HookApiAuth, v1(GroupAPI, ModeSync, true, true, "middleware.APIAuth 校验 PAT/OAuth token 后触发。可拦截实现细粒度授权；写 ctx.claims 注入身份信息。")},
		{HookCLI, v1(GroupAPI, ModeSync, false, true, "nebula hook <name> [args...] 触发。注册型(cli)：插件把结果写进 ctx.output，宿主打印到 stdout。")},

		// ---------------- 插件自身生命周期 ----------------
		{HookPluginInstall, sm(GroupPlugin, "插件安装成功后触发（此时尚未启用）。")},
		{HookPluginEnable, sm(GroupPlugin, "插件启用（子进程拉起成功）后触发。")},
		{HookPluginDisable, sm(GroupPlugin, "插件禁用后触发。")},
		{HookPluginConfigChange, sm(GroupPlugin, "插件配置更新后触发。插件需自行重启才会读到新配置。")},

		// ---------------- UI 扩展点 ----------------
		{HookUIPageOpen, sm(GroupUI, "用户打开插件注册页面（iframe 加载）时触发。ctx={plugin,page,userId,userName,ip}")},
		{HookUIThemeApply, syncM(GroupUI, false, true, "前端应用插件主题时触发。写 ctx.variables 可动态覆盖 CSS 变量。")},
		{HookUISettingsSave, syncM(GroupUI, true, true, "插件设置面板保存时触发。可拦截校验；写 ctx.values 覆盖落库值。")},
		{HookUIPageRegister, syncM(GroupUI, false, true, "宿主聚合 /api/plugins/ui 时触发。插件可写 ctx.sidebar/ctx.theme/ctx.settings 动态注入 UI 点。")},

		// ---------------- AI 智能 ----------------
		{HookAIRequest, syncM(GroupAI, true, true, "AI 网关向模型发起请求前触发。可拦截（决策 deny 即本次不外发）；可写 ctx.model/ctx.prompt 改写请求。ctx={userId,scene,provider,model,promptChars,codeSpace}")},
		{HookAIResponse, sm(GroupAI, "AI 请求结束后触发（成功或失败都发）。ctx={userId,scene,provider,model,success,latencyMs,promptTokens,completionTokens,error}")},
		{HookAIProviderResolve, syncM(GroupAI, false, true, "注册型(ai)：宿主点名调用 AI 能力提供方时触发。插件写 ctx.handled=true 并写 ctx.result 即可完全接管模型调用。ctx={userId,scene,prompt,maxTokens}")},
		{HookAIComplete, syncM(GroupAI, false, true, "代码空间请求补全时触发（AI 网关之前）。插件写 ctx.completion 可直接提供结果，宿主不再调模型。ctx={userId,fileId,fileName,language,prefix,suffix,maxTokens}")},
		{HookAIRecommend, syncM(GroupAI, false, true, "搜索联想/近期常用/热门推荐生成时触发。插件可写 ctx.suggestions 或 ctx.fileIds 覆盖内置结果。ctx={userId,q,limit}")},
		{HookAISearch, syncM(GroupAI, true, true, "自然语言搜文件时触发。插件可写 ctx.filters 覆盖解析出的检索条件、写 ctx.answer 直接作答；可拦截。ctx={userId,query,scope}")},
		{HookAISummary, syncM(GroupAI, false, true, "生成文件摘要与自动标签时触发。插件可写 ctx.summary / ctx.tags 接管。ctx={userId,fileId,fileName,mimeType,size,textChars}")},
		{HookAIAsk, syncM(GroupAI, true, true, "AI 助手问答时触发。插件可写 ctx.answer 直接回答；可拦截。ctx={userId,question,historyChars}")},
		{HookAIConfigChange, sm(GroupAI, "后台保存 AI 配置后触发。ctx={operatorId,operatorName,enabled,provider,model,changed}")},

		// ---------------- 限流 ----------------
		{HookRateLimit, v1(GroupLimit, ModeAsync, false, false, "【v1 兼容】高频操作前触发。整体异步仅通知；限流中间件这一处通过 RateLimitGate 强制同步读取拦截决策。")},
	}

	out := make([]HookMeta, 0, len(rows))
	for _, r := range rows {
		m := r.meta
		m.Name = string(r.name)
		if m.Payload == nil {
			m.Payload = []string{}
		}
		out = append(out, m)
	}
	return out
}()

// HookModeOf 返回钩子的派发模式。未登记的钩子按异步处理。
func HookModeOf(h HookName) HookMode {
	for _, m := range Hooks {
		if m.Name == string(h) {
			return m.Mode
		}
	}
	return ModeAsync
}

// AllHookNames 返回全部钩子名。
func AllHookNames() []HookName {
	out := make([]HookName, 0, len(Hooks))
	for _, m := range Hooks {
		out = append(out, HookName(m.Name))
	}
	return out
}

// MetaOf 返回某钩子的元信息。
func MetaOf(h HookName) (HookMeta, bool) {
	for _, m := range Hooks {
		if m.Name == string(h) {
			return m, true
		}
	}
	return HookMeta{}, false
}

// HooksInGroup 返回某分组的钩子元信息。
func HooksInGroup(g HookGroup) []HookMeta {
	out := make([]HookMeta, 0, 8)
	for _, m := range Hooks {
		if m.Group == g {
			out = append(out, m)
		}
	}
	return out
}

// IsV1Hook 判断是否为 v1 冻结钩子。
func IsV1Hook(h HookName) bool {
	m, ok := MetaOf(h)
	return ok && m.Since == 1
}

// V1HookNames 返回全部 v1 钩子名，供 manifest 的兼容校验使用。
func V1HookNames() []string {
	out := make([]string, 0, 12)
	for _, m := range Hooks {
		if m.Since == 1 {
			out = append(out, m.Name)
		}
	}
	return out
}

// HookDocs 钩子中文描述（供前端展示）。由 Hooks 表生成，避免两处维护。
var HookDocs = func() map[HookName]string {
	m := map[HookName]string{}
	for _, x := range Hooks {
		m[HookName(x.Name)] = x.Doc
	}
	return m
}()
