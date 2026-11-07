// Package hooks 是业务侧调用插件钩子的统一入口。
//
// 单独成包的原因：controllers / middleware / webdav 不该直接依赖
// plugin 包的派发细节（同步/异步、拦截判定、CtxPatch 合并），
// 也不该每个调用点各写一遍「有插件才构造 ctx」的轻量门。
//
// 本包只依赖 gin 与 plugin，不依赖 middleware / controllers，
// 因此 middleware 可以反过来 import 本包而不产生循环。
package hooks

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// Emit 纯通知（异步钩子）。无订阅者时零开销。
func Emit(h plugin.HookName, ctx map[string]any) {
	plugin.FireIf(h, ctx)
}

// Decide 决策型钩子：返回是否放行与拒绝原因。
//
// 内部走 FireIf 轻量门 —— 没有插件声明该钩子时立即放行，
// 不会产生 IPC 往返，因此高频路径也能安全调用。
func Decide(h plugin.HookName, ctx map[string]any) (allowed bool, reason string) {
	return plugin.DecisionOf(plugin.FireIf(h, ctx))
}

// Mutate 变换型钩子：同步派发后返回被插件改写过的 ctx。
// 业务侧用 Str/Int64/Bool 读回字段并应用。
func Mutate(h plugin.HookName, ctx map[string]any) map[string]any {
	plugin.FireIf(h, ctx)
	return ctx
}

// DecideMutate 同时需要决策与变换时用这个（一次派发，两件事都拿到）。
func DecideMutate(h plugin.HookName, ctx map[string]any) (allowed bool, reason string, out map[string]any) {
	errs := plugin.FireIf(h, ctx)
	allowed, reason = plugin.DecisionOf(errs)
	return allowed, reason, ctx
}

// Call 点名调用注册型能力（storage/auth/notify/...）。
func Call(kind plugin.ProviderKind, name string, ctx map[string]any) (bool, string) {
	return plugin.DecisionOf(plugin.CallProvider(kind, name, ctx))
}

const ctxKeyPrefix = "plugin_ctx_"

// Gate 决策/变换型钩子的统一入口：派发一次，同时拿到拦截结论与被改写过的 ctx。
//
// 结果会存进 gin.Context，业务侧可用 From(c, h) 读回插件的 CtxPatch。
// 这样即便某个调用点暂时不消费变换，插件的写入也不会丢失 ——
// 后续加业务消费时不需要改派发代码。
func Gate(h plugin.HookName, c *gin.Context, extras map[string]any) (bool, string, map[string]any) {
	ctx := ReqCtx(c, extras)
	errs := plugin.FireIf(h, ctx)
	allowed, reason := plugin.DecisionOf(errs)
	if c != nil {
		c.Set(ctxKeyPrefix+string(h), ctx)
	}
	return allowed, reason, ctx
}

// From 取回 Gate 保存的（可能已被插件改写过的）ctx。
func From(c *gin.Context, h plugin.HookName) map[string]any {
	if c == nil {
		return nil
	}
	v, ok := c.Get(ctxKeyPrefix + string(h))
	if !ok {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

// ReqCtx 构造通用请求上下文，再合并业务自定义字段。
//
// 不读 middleware.CurrentUser 是为了避免 import 循环；
// 用户身份等字段由调用方通过 extras 传入。
func ReqCtx(c *gin.Context, extras map[string]any) map[string]any {
	ctx := map[string]any{
		"method": "GET", "path": "", "ip": "", "ua": "", "requestId": "",
	}
	if c != nil {
		ctx["method"] = c.Request.Method
		ctx["path"] = c.FullPath()
		if ctx["path"] == "" {
			ctx["path"] = c.Request.URL.Path
		}
		ctx["ip"] = c.ClientIP()
		ctx["ua"] = c.GetHeader("User-Agent")
		ctx["requestId"] = c.GetHeader("X-Request-Id")
	}
	for k, v := range extras {
		ctx[k] = v
	}
	return ctx
}

// ---- 变换结果的安全读取 ----
// 插件通过 JSON 回写，数字会变成 float64、布尔可能变字符串，
// 这里统一做宽松转换，避免业务侧到处写类型断言。

func Str(ctx map[string]any, key, def string) string {
	if ctx == nil {
		return def
	}
	v, ok := ctx[key]
	if !ok || v == nil {
		return def
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return def
}

func Int64(ctx map[string]any, key string, def int64) int64 {
	if ctx == nil {
		return def
	}
	v, ok := ctx[key]
	if !ok || v == nil {
		return def
	}
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case string:
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func Bool(ctx map[string]any, key string, def bool) bool {
	if ctx == nil {
		return def
	}
	v, ok := ctx[key]
	if !ok || v == nil {
		return def
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		if b, err := strconv.ParseBool(x); err == nil {
			return b
		}
	case float64:
		return x != 0
	}
	return def
}

func StrSlice(ctx map[string]any, key string) []string {
	if ctx == nil {
		return nil
	}
	v, ok := ctx[key]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{x}
	}
	return nil
}
