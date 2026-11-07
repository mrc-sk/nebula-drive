package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// 注册型扩展点：插件提供实现，宿主来选。
//
// 与「钩子」的区别：钩子是宿主在固定时机调用所有订阅者；
// 注册型是插件声明「我能当某种后端」，宿主在需要时**点名**调用它。
// 例如社区插件注册 storage:b2，管理员在存储策略里选 b2，
// 宿主解析到该插件后走 onStorageResolve 让它接管读写。

// ProviderKind 注册型能力类别。
type ProviderKind string

const (
	ProviderStorage     ProviderKind = "storage"
	ProviderAuth        ProviderKind = "auth"
	ProviderNotify      ProviderKind = "notify"
	ProviderPreview     ProviderKind = "preview"
	ProviderPayment     ProviderKind = "payment"
	ProviderContentScan ProviderKind = "contentScan"
	ProviderBackup      ProviderKind = "backup"
	ProviderRestore     ProviderKind = "restore"
	ProviderCLI         ProviderKind = "cli"
	ProviderSearch      ProviderKind = "search"
	ProviderTheme       ProviderKind = "theme"
	ProviderPage        ProviderKind = "page"
)

// DefaultProviderHook 未显式指定 Hook 时，宿主用这个钩子调用该能力。
var DefaultProviderHook = map[ProviderKind]HookName{
	ProviderStorage:     HookStorageResolve,
	ProviderAuth:        HookAuthProviderResolve,
	ProviderNotify:      HookNotificationSend,
	ProviderPreview:     HookFilePreview,
	ProviderPayment:     HookPaymentCallback,
	ProviderContentScan: HookContentScan,
	ProviderBackup:      HookBackup,
	ProviderRestore:     HookRestore,
	ProviderCLI:         HookCLI,
	ProviderSearch:      HookStatsCollect,
	ProviderTheme:       HookUIThemeApply,
	ProviderPage:        HookUIPageOpen,
}

// ValidProviderKinds 允许的类别（manifest/co.BEGG 校验用）。
func ValidProviderKinds() []string {
	ks := make([]string, 0, len(DefaultProviderHook))
	for k := range DefaultProviderHook {
		ks = append(ks, string(k))
	}
	sort.Strings(ks)
	return ks
}

// Registration 一条注册声明。可由 co.BEGG 静态声明，也可由插件在 init
// 握手时通过 InitResult.Providers 动态上报（后者优先级更高）。
type Registration struct {
	Kind   ProviderKind    `json:"kind"`
	Name   string          `json:"name"`
	Label  string          `json:"label,omitempty"`
	Hook   HookName        `json:"hook,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

// Validate 校验注册项。
func (r Registration) Validate() error {
	if _, ok := DefaultProviderHook[r.Kind]; !ok {
		return fmt.Errorf("未知能力类别 %q，可选：%v", r.Kind, ValidProviderKinds())
	}
	if r.Name == "" {
		return errors.New("注册项缺少 name")
	}
	if len(r.Name) > 64 {
		return errors.New("注册项 name 过长")
	}
	if r.Hook != "" {
		if _, ok := MetaOf(r.Hook); !ok {
			return fmt.Errorf("注册项 %s 的 hook %q 未在钩子表中登记", r.Name, r.Hook)
		}
	}
	return nil
}

// EffectiveHook 返回实际用于调用该能力的钩子。
func (r Registration) EffectiveHook() HookName {
	if r.Hook != "" {
		return r.Hook
	}
	return DefaultProviderHook[r.Kind]
}

// PageDecl 插件注册的 HTTP 页面。
//
// 宿主把 /ext/<plugin>/<name> 反代到插件的 HTTP 服务，
// 前端侧边栏点开后用 iframe 嵌入（或按 Embed=newtab 新标签打开）。
type PageDecl struct {
	Name   string `json:"name"`             // 页面标识，URL 末段
	Home   string `json:"home"`             // 主页文件，相对 co.BEGG 所在目录，如 "in.php" / "ASS/in.php"
	Area   string `json:"area"`             // admin | main | public
	Title  string `json:"title"`            // 侧边栏显示名
	Icon   string `json:"icon,omitempty"`   // lucide 图标名
	Role   string `json:"role,omitempty"`   // admin | user | any，默认 any
	Order  int    `json:"order,omitempty"`  // 排序，小的在前
	Embed  string `json:"embed,omitempty"`  // iframe | newtab，默认 iframe
	Public bool   `json:"public,omitempty"` // true=宿主不做鉴权（透传，插件自负责）
	URL    string `json:"url,omitempty"`    // 外部 https:// 页面直连，不反代
}

// Validate 校验页面声明。
func (p PageDecl) Validate() error {
	if p.Name == "" {
		return errors.New("页面缺少 name")
	}
	for _, r := range p.Name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("页面 name %q 含非法字符（只允许小写字母/数字/-/_）", p.Name)
		}
	}
	switch p.Area {
	case "admin", "main", "public":
	default:
		return fmt.Errorf("页面 %s 的 area %q 非法（admin|main|public）", p.Name, p.Area)
	}
	switch p.Role {
	case "", "admin", "user", "any":
	default:
		return fmt.Errorf("页面 %s 的 role %q 非法", p.Name, p.Role)
	}
	switch p.Embed {
	case "", "iframe", "newtab":
	default:
		return fmt.Errorf("页面 %s 的 embed %q 非法", p.Name, p.Embed)
	}
	return nil
}

// SidebarItem 前端侧边栏项（由 PageDecl 转换）。
type SidebarItem struct {
	Plugin string `json:"plugin"`
	Name   string `json:"name"`
	Title  string `json:"title"`
	Icon   string `json:"icon,omitempty"`
	Area   string `json:"area"`
	Role   string `json:"role"`
	Order  int    `json:"order"`
	Embed  string `json:"embed"`
	URL    string `json:"url"`
	Public bool   `json:"public"`
}

// ThemeDecl 插件主题包。
type ThemeDecl struct {
	Name      string            `json:"name"`
	Label     string            `json:"label,omitempty"`
	CSS       string            `json:"css,omitempty"`       // 相对插件目录的 CSS 文件
	Variables map[string]string `json:"variables,omitempty"` // CSS 变量覆盖
	Preview   string            `json:"preview,omitempty"`
}

// SettingField 插件设置面板字段。
type SettingField struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Type    string `json:"type"` // text|password|number|bool|select|textarea
	Default string `json:"default,omitempty"`
	Options string `json:"options,omitempty"` // select 用，逗号分隔
	Help    string `json:"help,omitempty"`
}

// UIDecl co.BEGG 中的 UI 扩展点声明。
type UIDecl struct {
	Sidebar  []SidebarConfig `json:"sidebar,omitempty"`
	Theme    *ThemeDecl      `json:"theme,omitempty"`
	Settings []SettingField  `json:"settings,omitempty"`
}

// SidebarConfig 侧边栏声明（静态版，宿主会转成 SidebarItem）。
type SidebarConfig struct {
	Area  string `json:"area"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
	Page  string `json:"page"` // 对应 PageDecl.Name
	Role  string `json:"role,omitempty"`
	Order int    `json:"order,omitempty"`
}

// ---- 全局注册表 ----

// RegisteredProvider 带插件归属的注册项。
type RegisteredProvider struct {
	Registration
	Plugin string `json:"plugin"`
}

// Registry 线程安全的注册型能力表。
type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderKind][]RegisteredProvider
	pages     map[string][]SidebarItem // plugin -> 侧边栏项
	themes    map[string]ThemeDecl     // plugin -> 主题
	settings  map[string][]SettingField
}

var globalRegistry = &Registry{
	providers: map[ProviderKind][]RegisteredProvider{},
	pages:     map[string][]SidebarItem{},
	themes:    map[string]ThemeDecl{},
	settings:  map[string][]SettingField{},
}

// GlobalRegistry 返回全局注册表。
func GlobalRegistry() *Registry { return globalRegistry }

// Add 登记某插件的能力与 UI 扩展点（追加语义）。
//
// 一个插件的声明可能来自两处：co.BEGG 静态声明、init 握手动态上报。
// 两处都要能登记，所以 Add 只追加；卸载时由调用方显式 Remove。
func (r *Registry) Add(pluginName string, regs []Registration, pages []PageDecl, ui *UIDecl, baseURL string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, reg := range regs {
		r.providers[reg.Kind] = append(r.providers[reg.Kind], RegisteredProvider{Registration: reg, Plugin: pluginName})
	}
	for _, p := range pages {
		url := p.URL
		if url == "" {
			// p.Home 可能已经是 "/in.php"（PagePath 转换过），
			// 直接 base+"/"+home 会拼出 //，这里统一去/补斜杠。
			page := p.Name
			if p.Home != "" {
				page = p.Home
			}
			url = strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(page, "/")
		}
		role := p.Role
		if role == "" {
			role = "any"
		}
		embed := p.Embed
		if embed == "" {
			embed = "iframe"
		}
		r.pages[pluginName] = append(r.pages[pluginName], SidebarItem{
			Plugin: pluginName, Name: p.Name, Title: p.Title, Icon: p.Icon,
			Area: p.Area, Role: role, Order: p.Order, Embed: embed,
			URL: url, Public: p.Public,
		})
	}
	if ui != nil {
		if ui.Theme != nil {
			r.themes[pluginName] = *ui.Theme
		}
		if len(ui.Settings) > 0 {
			r.settings[pluginName] = append([]SettingField(nil), ui.Settings...)
		}
		// 静态 sidebar 声明：按 Page 名找到对应页面项并覆盖标题/排序
		for _, s := range ui.Sidebar {
			items := r.pages[pluginName]
			for i := range items {
				if items[i].Name != s.Page {
					continue
				}
				if s.Title != "" {
					items[i].Title = s.Title
				}
				if s.Icon != "" {
					items[i].Icon = s.Icon
				}
				if s.Area != "" {
					items[i].Area = s.Area
				}
				if s.Role != "" {
					items[i].Role = s.Role
				}
				if s.Order != 0 {
					items[i].Order = s.Order
				}
			}
			r.pages[pluginName] = items
		}
	}
	for k := range r.providers {
		sort.SliceStable(r.providers[k], func(i, j int) bool {
			return r.providers[k][i].Name < r.providers[k][j].Name
		})
	}
}

func (r *Registry) removeLocked(pluginName string) {
	for k, list := range r.providers {
		out := list[:0]
		for _, p := range list {
			if p.Plugin != pluginName {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			delete(r.providers, k)
		} else {
			r.providers[k] = out
		}
	}
	delete(r.pages, pluginName)
	delete(r.themes, pluginName)
	delete(r.settings, pluginName)
}

// Remove 卸载某插件的全部注册。
func (r *Registry) Remove(pluginName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(pluginName)
}

// Resolve 按类别与名字精确查找能力。
func (r *Registry) Resolve(kind ProviderKind, name string) (RegisteredProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers[kind] {
		if p.Name == name {
			return p, true
		}
	}
	return RegisteredProvider{}, false
}

// Providers 返回某类别的全部能力。
func (r *Registry) Providers(kind ProviderKind) []RegisteredProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]RegisteredProvider(nil), r.providers[kind]...)
}

// AllProviders 返回全部注册项（管理后台展示用）。
func (r *Registry) AllProviders() []RegisteredProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RegisteredProvider, 0)
	for _, list := range r.providers {
		out = append(out, list...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Sidebar 返回指定区域、指定角色可见的侧边栏项（已排序）。
func (r *Registry) Sidebar(area, role string) []SidebarItem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SidebarItem, 0)
	for _, list := range r.pages {
		for _, it := range list {
			if it.Area != area {
				continue
			}
			if it.Role == "admin" && role != "admin" {
				continue
			}
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// Themes 返回全部主题。
func (r *Registry) Themes() map[string]ThemeDecl {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]ThemeDecl, len(r.themes))
	for k, v := range r.themes {
		out[k] = v
	}
	return out
}

// Settings 返回全部插件设置字段。
func (r *Registry) Settings() map[string][]SettingField {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string][]SettingField, len(r.settings))
	for k, v := range r.settings {
		out[k] = append([]SettingField(nil), v...)
	}
	return out
}

// CallProvider 点名调用某个注册型能力。
//
// ctx 会被原地修改（插件通过 CtxPatch 回写），返回值交给 DecisionOf 判定。
// 找不到注册项时返回 nil（业务侧应自行 fallback 到内置实现）。
func CallProvider(kind ProviderKind, name string, ctx map[string]any) []error {
	p, ok := GlobalRegistry().Resolve(kind, name)
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = map[string]any{}
	}
	ctx["provider"] = p.Name
	ctx["providerKind"] = string(kind)
	return FireIf(p.EffectiveHook(), ctx)
}
