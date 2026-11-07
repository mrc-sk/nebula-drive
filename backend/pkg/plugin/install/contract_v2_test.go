package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nebula-drive/nebula/pkg/plugin"
)

// 这三个文件与 pkg/plugin 的契约测试用同一份样本：v2 纯页面插件，
// 没有 entry，页面由 co.BEGG 拉起；三文件可以散落在不同子目录。
const v2Manifest = `{
  "name": "ass-demo",
  "version": "1.0.0",
  "protocolVersion": 2,
  "pages": [{"name":"home","area":"main","title":"ASS 面板","home":"in.php"}]
}`

const v2RegisterGo = `package ass

var Spec = NotARealType{
	Name:            "ass-demo",
	Version:         "1.0.0",
	ProtocolVersion: 2,
	Hooks:           []string{},
	Runtime:         "php",
	Home:            "in.php",
}
`

const v2Begg = `{
  "runtime": "php",
  "home": "in.php",
  "pages": [{"name":"home","area":"main","title":"ASS 面板","home":"in.php"}]
}`

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallFromDirKeepsArbitraryLayout(t *testing.T) {
	root := t.TempDir()
	pluginsDir := t.TempDir()
	src := filepath.Join(root, "ass-plugin-main")

	// 用户说的形态：一个 ASS/ 目录里一堆 .php + 注册型 .go + co.BEGG，
	// plugin.json 在上一层。安装器必须整体复制，不能裁层或只挑根目录文件。
	put(t, filepath.Join(src, plugin.ManifestFileName), v2Manifest)
	put(t, filepath.Join(src, "ASS", plugin.GoDeclFileName), v2RegisterGo)
	put(t, filepath.Join(src, "ASS", plugin.BeggFileName), v2Begg)
	put(t, filepath.Join(src, "ASS", "in.php"), `<?php echo "hello"; ?>`)
	put(t, filepath.Join(src, "ASS", "lib.php"), `<?php // helper ?>`)

	ins := New(pluginsDir)
	res, err := ins.InstallFromDir(src, "test")
	if err != nil {
		t.Fatalf("任意目录布局应可安装: %v", err)
	}
	if res.Name != "ass-demo" {
		t.Fatalf("name = %q", res.Name)
	}
	dst := filepath.Join(pluginsDir, "ass-demo")
	for _, rel := range []string{
		plugin.ManifestFileName,
		filepath.Join("ASS", plugin.GoDeclFileName),
		filepath.Join("ASS", plugin.BeggFileName),
		filepath.Join("ASS", "in.php"),
		filepath.Join("ASS", "lib.php"),
	} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("安装后缺文件 %s: %v", rel, err)
		}
	}
	// 安装完的目录必须仍是一份合法契约（后续启用阶段会再走一次 LoadContract）
	c, err := plugin.LoadContract(dst, false)
	if err != nil {
		t.Fatalf("安装产物契约无效: %v", err)
	}
	if c.BeggDir != filepath.Join(dst, "ASS") {
		t.Errorf("BeggDir = %q, want ASS/", c.BeggDir)
	}
}

func TestFindPluginRootRecursive(t *testing.T) {
	base := t.TempDir()
	// GitHub 源码 zip 常见形态：顶层目录里再嵌一层 ASS/ 放清单
	target := filepath.Join(base, "ass-plugin-main", "ASS")
	put(t, filepath.Join(target, plugin.ManifestFileName), v2Manifest)

	got, err := findPluginRoot(base)
	if err != nil {
		t.Fatalf("递归定位失败: %v", err)
	}
	if got != target {
		t.Fatalf("findPluginRoot = %q, want %q", got, target)
	}
}

func TestFindPluginRootRejectsAmbiguous(t *testing.T) {
	base := t.TempDir()
	put(t, filepath.Join(base, "a", plugin.ManifestFileName), v2Manifest)
	put(t, filepath.Join(base, "b", plugin.ManifestFileName), v2Manifest)
	if _, err := findPluginRoot(base); err == nil {
		t.Fatal("多个候选插件目录时应报错，不能猜一个")
	}
}

func TestReadManifestHandlesPurePagePlugin(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, plugin.ManifestFileName), v2Manifest)
	put(t, filepath.Join(dir, "ASS", plugin.GoDeclFileName), v2RegisterGo)
	put(t, filepath.Join(dir, "ASS", plugin.BeggFileName), v2Begg)
	put(t, filepath.Join(dir, "ASS", "in.php"), "x")

	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("纯页面插件不应被误判为缺 entry: %v", err)
	}
	if m.Name != "ass-demo" || len(m.Pages) != 1 {
		t.Fatalf("清单解析异常: %+v", m)
	}
}

func TestV1InstallNeedsNoThreeFiles(t *testing.T) {
	root, pluginsDir := t.TempDir(), t.TempDir()
	src := filepath.Join(root, "legacy")
	put(t, filepath.Join(src, plugin.ManifestFileName), `{
		"name":"legacy","version":"1.0.0","entry":"legacy-bin",
		"protocolVersion":1,"hooks":["onAntiLeech"]
	}`)
	put(t, filepath.Join(src, "legacy-bin"), "#!/bin/sh\nexit 0\n")

	ins := New(pluginsDir)
	if _, err := ins.InstallFromDir(src, "test"); err != nil {
		t.Fatalf("v1 插件应仍能安装（自动兼容）: %v", err)
	}
}
