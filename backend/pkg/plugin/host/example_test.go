package host_test

import (

	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/host"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
)

// 这组测试用**真实示例插件**（examples/plugins/referer-guard）跑通完整链路：
// 构建 → 安装（走真实的 InstallFromDir）→ 加载 → 拦截 → 卸载。
//
// 目的是防止"测试假插件能过但真插件不行"：示例插件不 import 宿主包，
// 协议结构独立声明，编译方式也和用户实际使用的一致。

const refPluginDir = "../../../../examples/plugins/referer-guard"

// buildRefGuard 编译示例插件并安装到临时插件目录。
func buildRefGuard(t *testing.T) (*install.Installer, string) {
	t.Helper()
	if _, err := os.Stat(refPluginDir); err != nil {
		t.Skipf("示例插件源码不存在: %v", err)
	}
	pluginsDir := t.TempDir()

	// 把示例插件源码拷到暂存目录并就地编译。
	// 不直接改 examples/ 下的目录 —— 测试不该污染仓库。
	staged := t.TempDir()
	copyDirFiles(t, refPluginDir, staged)

	entry := "referer-guard"
	if runtime.GOOS == "windows" {
		entry = "referer-guard.exe"
	}
	buildInto(t, staged, entry)

	ins := install.New(pluginsDir)
	res, err := ins.InstallFromDir(staged, "test")
	if err != nil {
		t.Fatalf("安装示例插件失败: %v", err)
	}
	if res.Name != "referer-guard" {
		t.Fatalf("name = %q", res.Name)
	}
	if len(res.Manifest.Hooks) != 1 ||
		res.Manifest.Hooks[0] != string(plugin.HookAntiLeech) {
		t.Fatalf("hooks = %v", res.Manifest.Hooks)
	}
	return ins, res.Dir
}

// copyDirFiles 递归复制目录。
func copyDirFiles(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			copyDirFiles(t, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()))
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// buildInto 在 dir 里 go build，产物名为 out。
func buildInto(t *testing.T, dir, out string) {
	t.Helper()
	full := filepath.Join(dir, out)
	cmd := exec.Command("go", "build", "-o", full, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=", "CGO_ENABLED=0")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译示例插件失败: %v\n%s", err, o)
	}
	st, err := os.Stat(full)
	if err != nil || st.Size() == 0 {
		t.Fatalf("编译产物异常: %v", err)
	}
}


func TestRefererGuard_EndToEnd(t *testing.T) {
	ins, dir := buildRefGuard(t)

	m := host.New(host.Config{
		PluginsDir:   ins.PluginsDir,
		DataDir:      t.TempDir(),
		HostVersion:  "test",
		StartTimeout: 30 * time.Second,
		FireTimeout:  5 * time.Second,
		Log:          t.Logf,
	})
	defer m.StopAll()

	man, err := install.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Load(host.LoadSpec{Name: man.Name, Dir: dir, Manifest: man}); err != nil {
		t.Fatalf("加载示例插件失败: %v", err)
	}
	if got := m.Count(plugin.HookAntiLeech); got != 1 {
		t.Fatalf("Count = %d, want 1", got)
	}

	// 默认配置只允许 localhost，所以：
	cases := []struct {
		name    string
		ctx     map[string]any
		blocked bool
	}{
		{"本地来源放行", map[string]any{"fileName": "a.zip", "referer": "http://localhost:3000/x"}, false},
		{"外站拦截", map[string]any{"fileName": "a.zip", "referer": "https://evil.example.net/x"}, true},
		{"无 Referer 默认放行", map[string]any{"fileName": "a.zip", "referer": ""}, false},
		{"豁免用户放行", map[string]any{"fileName": "a.zip", "referer": "https://evil.example.net/x",
			"userId": float64(1)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := c.ctx["userId"]; !ok {
				c.ctx["userId"] = float64(99) // 非豁免用户
			}
			errs := m.Dispatch(plugin.HookAntiLeech, c.ctx)
			blocked := false
			for _, e := range errs {
				if plugin.IsBlockedMessage(e.Error()) {
					blocked = true
				}
			}
			if blocked != c.blocked {
				t.Fatalf("blocked = %v, want %v (errs=%v)", blocked, c.blocked, errs)
			}
			// 插件应回报判定结果
			if c.ctx["refererGuard"] == nil {
				t.Fatalf("插件未回写判定结果: %v", c.ctx)
			}
		})
	}
}

func TestRefererGuard_LogsAndUninstall(t *testing.T) {
	ins, dir := buildRefGuard(t)
	dataDir := t.TempDir()

	m := host.New(host.Config{
		PluginsDir:   ins.PluginsDir,
		DataDir:      dataDir,
		HostVersion:  "test",
		StartTimeout: 30 * time.Second,
		FireTimeout:  5 * time.Second,
		Log:          func(string, ...any) {},
	})
	defer m.StopAll()

	man, err := install.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Load(host.LoadSpec{Name: man.Name, Dir: dir, Manifest: man}); err != nil {
		t.Fatal(err)
	}
	m.Dispatch(plugin.HookAntiLeech, map[string]any{
		"fileName": "x.zip", "referer": "https://evil.example.net/", "userId": float64(9),
	})

	// 宿主侧应记录拦截日志
	found := false
	for _, l := range m.Logs("referer-guard", 200) {
		if strings.Contains(l.Message, "拦截下载") {
			found = true
		}
	}
	if !found {
		t.Fatalf("宿主日志缺少拦截记录: %+v", m.Logs("referer-guard", 200))
	}

	// 卸载：目录应消失，主服务不受影响
	m.Unload(man.Name)
	if m.Get(man.Name) != nil {
		t.Fatal("卸载后仍在 registry")
	}
	ok, err := ins.Uninstall(man.Name)
	if err != nil || !ok {
		t.Fatalf("卸载目录失败: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("插件目录仍存在")
	}
	// 卸载后再派发必须安全
	if errs := m.Dispatch(plugin.HookAntiLeech, map[string]any{"fileName": "y"}); len(errs) != 0 {
		t.Fatalf("卸载后派发报错: %v", errs)
	}
}
