package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nebula-drive/nebula/pkg/plugin"
)

// TestExampleManifests 校验 examples/plugins 下每个插件的 plugin.json
// 都能被**真实的**解析器接受。
//
// 为什么需要这组断言：plugin.json 是最容易被写坏又最难察觉的文件 ——
//
//   - 字段名拼错（ParseManifest 开了 DisallowUnknownFields，直接拒载）
//   - 漏写 hooks（校验要求非空）
//   - entry 带了路径分隔符（防任意代码执行，直接拒载）
//   - 目录名与清单 name 不一致（宿主按 name 定位安装目录，装完找不到）
//
// 以上任何一种，go build 与 go vet 都不会有任何反应，插件却完全装不上。
// 发布示例插件前跑一遍这个测试，比手工装一遍快得多。
func TestExampleManifests(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "examples", "plugins")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("示例插件目录不存在: %v", err)
	}

	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		standalone := filepath.Join(root, name, plugin.ManifestFileName)
		raw, err := os.ReadFile(standalone)
		if err != nil {
			// 目录存在但没有清单：要么是误留的目录，要么是还没写完的骨架。
			// 报错而不是跳过 —— 一个装不上的示例插件不该混进仓库。
			t.Errorf("%s: 缺少 %s", name, plugin.ManifestFileName)
			continue
		}

		man, err := plugin.ParseManifest(raw)
		if err != nil {
			t.Errorf("%s 的 plugin.json 无法解析: %v", name, err)
			continue
		}
		if man.Name != name {
			t.Errorf("%s: 清单 name=%q 与目录名不一致（宿主按 name 定位安装目录）",
				name, man.Name)
		}
		// 管理端要展示 title 与 description，为空会显示成一片空白。
		if man.Title == "" || man.Description == "" {
			t.Errorf("%s: title/description 不应为空", name)
		}
		// entry 指向的可执行文件由构建产出，源码目录里本来就没有，
		// 因此这里只校验 entry 的字段合法性（ParseManifest 已做），
		// 不检查文件是否存在 —— 那需要现场编译，属于 example_test.go 的职责。
		checked++
	}

	if checked == 0 {
		t.Skip("没有找到任何示例插件")
	}
	t.Logf("校验了 %d 个示例插件清单", checked)
}
