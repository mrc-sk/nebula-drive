package plugin

// 插件协议（NebulaDrive Plugin Agreement）。
//
// 作用：管理员在首次进入插件页面时，必须显式阅读并同意这份协议，
// 才能安装/启用第三方插件。协议升级后需要重新同意。
//
// 设计原则：
//   - 协议正文由宿主内置（不可被插件篡改），插件只能读取。
//   - 同意状态存 settings 表，键为 plugin.agreement。
//   - **后端强制**：未同意时 API 层直接拒绝安装/启用，
//     不能只靠前端弹窗 —— 否则直接调 API 就能绕过。

// AgreementVersion 协议版本。**每次修改协议正文都必须 +1**，
// 否则已同意过的管理员不会看到新内容。
const AgreementVersion = 2

// AgreementKey settings 表中的键。
const AgreementKey = "plugin.agreement"

// Agreement 协议全文与元信息（管理端下发用）。
type Agreement struct {
	Version int    `json:"version"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
	// Sections 分节展示，便于前端折叠渲染。
	Sections []AgreementSection `json:"sections"`
	// Required 一句话摘要，显示在勾选框上方。
	Required string `json:"required"`
}

// AgreementSection 协议的一节。
type AgreementSection struct {
	Heading string   `json:"heading"`
	Paras   []string `json:"paras"`
	// Bullets 无序列表项。
	Bullets []string `json:"bullets,omitempty"`
	// Critical 该节是否含关键风险（前端可高亮）。
	Critical bool `json:"critical,omitempty"`
}

// AgreementAccepted 同意记录（存 settings.value 的 JSON）。
type AgreementAccepted struct {
	Version    int    `json:"version"`
	AcceptedAt string `json:"acceptedAt"`
	AcceptedBy uint   `json:"acceptedBy"`
	UserName   string `json:"userName"`
	IP         string `json:"ip"`
}

// CurrentAgreement 返回当前协议全文。
//
// 内容是硬编码的常量而非配置文件：协议是法律/安全文本，
// 从外部文件读意味着可以被部署者改掉，管理员看到的就不再是宿主承诺的那份。
func CurrentAgreement() Agreement {
	return Agreement{
		Version: AgreementVersion,
		Title:   "NebulaDrive 插件协议",
		Updated: "2026-10-04",
		Required: "安装或启用任何第三方插件，即表示你已阅读并理解以下全部条款。",
		Sections: agreementSections,
	}
}

var agreementSections = []AgreementSection{
	{
		Heading: "一、插件是什么",
		Paras: []string{
			"插件是独立于 NebulaDrive 主程序的第三方可执行程序。宿主以子进程方式拉起它，" +
				"并通过标准输入输出上的 JSON-RPC 协议与它通信。",
			"启用、停用、重载插件都不会重启 NebulaDrive 主服务（热加载），" +
				"但插件崩溃会被自动重启，连续崩溃 5 次后停止自动重启并等待人工处理。",
		},
	},
	{
		Heading:   "二、权限与风险（关键）",
		Critical:  true,
		Paras:     []string{"这是本协议最重要的一节，请在同意前完整阅读。"},
		Bullets: []string{
			"插件以**与 NebulaDrive 主服务完全相同的系统权限**运行。" +
				"它能读写该操作系统账号能触及的所有文件，包括数据库与密钥文件。",
			"宿主对插件**没有进程级沙箱**。请只安装来源可信的插件，" +
				"或自行在容器/独立账号中运行以限制影响范围。",
			"插件可发起任意网络请求，包括访问你的内网服务。若你已配置" +
				"插件商店地址，插件商店中的条目同样由第三方维护。",
			"安装即表示你接受对该插件的行为负责。NebulaDrive 项目不对" +
				"第三方插件的行为、可用性或安全性作任何担保。",
		},
	},
	{
		Heading: "三、可观测性",
		Paras: []string{
			"宿主会记录插件的启动参数、退出原因与日志（最近 500 条），" +
				"并在管理端展示，便于排查插件故障。这些日志可能包含文件名与 IP。",
			"插件每次被派发钩子时，能读到该次钩子提供的全部上下文字段" +
				"（例如下载钩子会提供文件名、IP、User-Agent）。" +
				"请勿把敏感数据放入这些字段。",
		},
	},
	{
		Heading: "四、拦截语义",
		Paras: []string{
			"下载类钩子（onAntiLeech）中，若插件返回的 error 文本包含子串 " +
				"blocked（区分大小写），该次下载会被拒绝并返回 403。",
			"请注意：这是字符串匹配而非结构化判定。文本相似但不含该子串的" +
				"错误不会触发拦截。",
		},
	},
	{
		Heading: "五、数据与配置",
		Paras: []string{
			"每个插件有独立的数据目录，位于 <数据目录>/plugin-data/<插件名>/，" +
				"插件可自由读写。删除该目录即清除插件的全部本地数据。",
			"插件配置由你在管理端填写，并以明文 JSON 下发给插件。" +
				"请勿在配置中存放你无法承受泄露的凭据。",
		},
	},
	{
		Heading: "六、许可与合规",
		Critical: true,
		Paras: []string{
			"NebulaDrive 采用 GNU AGPL-3.0。安装插件不会改变本项目的许可，" +
				"但你分发包含插件的整套系统时，需同时满足 AGPL-3.0 与各插件自身许可的要求。",
			"你安装的插件由其作者授权，不是 NebulaDrive 项目的组成部分。" +
				"若某插件要求你以网络服务形式对外提供其功能，你需自行确认该插件的" +
				"许可条款是否要求你公开对应源码。",
			"宿主不校验插件声明的 permissions 是否与实际行为一致。" +
				"声明仅供你安装前参考。",
		},
	},
	{
		Heading: "七、卸载",
		Paras: []string{
			"卸载会停止插件进程并删除其安装目录与数据目录，不影响主服务与其他插件。",
			"卸载不会自动删除插件在外部系统产生的数据（如它自己写入的第三方服务）。",
		},
	},
	{
		Heading: "八、协议的变更",
		Paras: []string{
			"本协议正文由宿主内置，插件无法篡改。协议版本升级后，" +
				"你需要重新阅读并同意，之后才能继续安装或启用插件。",
			"已安装插件的代码不会因协议升级而改变；变更只影响" +
				"「是否允许你继续启用它们」这一决定。",
		},
	},
}
