package host_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/host"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
)

// 这组测试回答一个原本只写在 README 里、没人验证过的问题：
// 示例插件到底能不能装上、启用、收到事件？
//
// 断言的是**协议不变量**，而不是某个插件的业务结果：
//
//  1. 空配置也能启用（init 握手通过）—— 否则管理员的体验是
//     "装了却启不起来"，连日志都看不到。
//  2. 声明的钩子在宿主侧计数为 1。
//  3. 事件真的被处理了（能观测到插件的回写或它的拦截决定）。
//
// # wantBlocked：为什么两个插件的期望相反
//
// 这一条是**故意逐个指定**的，不是笔误：
//
//   - `smtp-email` 实现 onEmail（异步、不可拦截）。它**绝不能**返回含
//     "blocked" 的错误 —— 邮件发不出去不该让站内通知创建失败。
//   - `hmac-auth` 实现 onApiAuth（同步、可拦截）。空配置时它**必须**拦截
//     （fail closed）：没有密钥就不存在鉴权，退回"放行"等于把开放 API 敞开。
//
// 早先这组测试统一断言"插件绝不能返回拦截错误"，那是 onApiAuth 还处于
// 未接线的异步占位状态时的写法。接通后该断言对 hmac-auth 就是**错的期望**。
func TestExamplePlugins_LoadAndDispatch(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		hook        plugin.HookName
		ctx         map[string]any
		patchKey    string
		async       bool
		wantBlocked bool
		why         string
	}{
		{
			name: "smtp-email", src: "../../../../examples/plugins/smtp-email",
			hook: plugin.HookEmail,
			ctx:  map[string]any{"to": "a@b.c", "subject": "hello", "body": "world"},
			// 无配置时不发信，但必须回写状态；绝不拦截
			patchKey: "smtpEmail", async: true, wantBlocked: false,
			why: "onEmail 是异步不可拦截钩子：邮件失败不该让站内通知创建失败",
		},
		{
			name: "hmac-auth", src: "../../../../examples/plugins/hmac-auth",
			hook: plugin.HookApiAuth,
			ctx:  map[string]any{"method": "GET", "path": "/api/v1/files", "headers": map[string]string{}},
			// 无配置（无密钥）→ 必须拦截
			patchKey: "hmacAuth", async: false, wantBlocked: true,
			why: "未配置密钥时必须 fail closed：退回放行等于把开放 API 敞开",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := loadExamplePlugin(t, c.src, c.name)
			defer m.StopAll()

			// 1+2：能启用（init 握手通过），且声明的钩子被宿主认到
			if got := m.Count(c.hook); got != 1 {
				t.Fatalf("Count(%s) = %d, want 1 —— init 握手或 hooks 声明有问题；日志：%+v",
					c.hook, got, m.Logs(c.name, 50))
			}

			// 异步钩子的热路径语义：立即返回、不报错
			if c.async {
				hot := copyCtx(c.ctx)
				if errs := m.Dispatch(c.hook, hot); len(errs) != 0 {
					t.Fatalf("异步钩子 %s 派发返回了错误（热路径不该等也不该报错）: %v", c.hook, errs)
				}
				// 给在途的异步写一点时间，免得 StopAll 打断它、在日志里刷出
				// "file already closed" 这种会误导排查的噪音。
				time.Sleep(300 * time.Millisecond)
			}

			// 3：强制同步观测插件的实际决定
			sync := copyCtx(c.ctx)
			var blocked bool
			for _, e := range m.DispatchSync(c.hook, sync) {
				if plugin.IsBlockedMessage(e.Error()) {
					blocked = true
				}
			}
			if blocked != c.wantBlocked {
				t.Fatalf("拦截行为不符合预期: blocked=%v want=%v —— %s；ctx=%v 日志：%+v",
					blocked, c.wantBlocked, c.why, sync, m.Logs(c.name, 50))
			}
			// 事件真的被处理了：要么回写了 ctx，要么明确拦截（拦截本身就证明
			// 插件读到了内容并做了判断）。
			if !blocked && sync[c.patchKey] == nil {
				t.Fatalf("插件未回写 %q —— 事件可能被静默丢弃；ctx=%v 日志：%+v",
					c.patchKey, sync, m.Logs(c.name, 50))
			}

			// 未声明的钩子不该被派发（宿主按 manifest 过滤），派发也不该报错
			if errs := m.DispatchSync(plugin.HookCLI, map[string]any{}); len(errs) != 0 {
				t.Fatalf("未声明的钩子派发报错: %v", errs)
			}
		})
	}
}

func copyCtx(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// loadExamplePlugin 编译示例插件、走真实安装器装入临时插件目录、
// 再用真实宿主加载（即一次完整的 init 握手）。
//
// 全程不传配置（ConfigFor 为 nil）—— 这正是要验证的场景：
// 管理员刚装好插件、还没填任何配置时能不能启用，以及此时的行为是否 fail closed。
func loadExamplePlugin(t *testing.T, srcDir, name string) *host.Manager {
	t.Helper()
	// 源码不在时跳过而不是失败：示例插件目录可能被单独打包分发。
	// 但目录在、清单缺了就必须报错（下面 InstallFromDir / ReadManifest 会报）。
	if _, err := os.Stat(filepath.Join(srcDir, plugin.ManifestFileName)); err != nil {
		t.Skipf("示例插件源码不存在: %v", err)
	}

	staged := t.TempDir()
	copyDirFiles(t, srcDir, staged)

	entry := name
	if runtime.GOOS == "windows" {
		entry += ".exe"
	}
	buildInto(t, staged, entry)

	ins := install.New(t.TempDir())
	res, err := ins.InstallFromDir(staged, "test")
	if err != nil {
		t.Fatalf("安装 %s 失败: %v", name, err)
	}
	if res.Name != name {
		t.Fatalf("安装后的 name = %q, want %q", res.Name, name)
	}

	man, err := install.ReadManifest(res.Dir)
	if err != nil {
		t.Fatalf("读取 %s 清单失败: %v", name, err)
	}

	m := host.New(host.Config{
		PluginsDir:   ins.PluginsDir,
		DataDir:      t.TempDir(),
		HostVersion:  "test",
		StartTimeout: 30 * time.Second,
		FireTimeout:  5 * time.Second,
		Log:          t.Logf,
	})
	if err := m.Load(host.LoadSpec{Name: man.Name, Dir: res.Dir, Manifest: man}); err != nil {
		t.Fatalf("加载 %s 失败（init 握手未通过）: %v", name, err)
	}
	return m
}
