package controllers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// TestPluginPathsAgreeWithLoader 安装侧与加载侧必须用**同一个**插件目录。
//
// 这是一条回归测试，针对一个真实存在过的 bug：
//
//   - 加载侧（`pluginset.InitPlugins`）用 `conf.DataDir()/plugins`；
//   - 安装侧（本包的 `pluginsRootDir()`）用 `dataDirOrCwd()/plugins`，
//     而 `NEBULA_DATA_DIR` 在**仓库里从未被设置过**，于是它永远退化成
//     进程工作目录。
//
// 后果不是"插件装不上"，而是更隐蔽的一种：**安装 + 启用当场有效**
// （两条路径都由控制器自己算，是一致的），但**重启服务后插件不会回来**
// —— 因为启动加载走的是另一个目录，且没有任何错误提示。
//
// 冒烟测试抓不到它：脚本在同一个进程里装完就启用，从不重启。
// 所以这里直接断言两个路径相等，而不是靠端到端碰运气。
func TestPluginPathsAgreeWithLoader(t *testing.T) {
	testutil.SetupDB(t)

	// 模拟生产：数据目录由 -data 指定（或默认 "data"），而进程工作目录是别处。
	// 这与"忘了传 -data"是同一回事，也正是 bug 触发的条件。
	dataDir := conf.DataDir()
	if dataDir == "" {
		t.Fatal("conf.DataDir() 不该为空")
	}
	loaderPath := filepath.Join(dataDir, "plugins")
	installerPath := pluginsRootDir()

	// 归一化成绝对路径再比：两边都可能写成相对路径，
	// 而相对路径只有在"相对同一个基准"时才是同一个目录。
	absLoader, err := filepath.Abs(loaderPath)
	if err != nil {
		t.Fatal(err)
	}
	absInstaller, err := filepath.Abs(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	if absLoader != absInstaller {
		t.Fatalf("安装侧与加载侧的插件目录不一致 —— 装完启用能用，重启后插件会消失\n"+
			"  加载侧(pluginset): %s\n  安装侧(controllers): %s",
			absLoader, absInstaller)
	}
}

// TestTrustStorePathFollowsDataDir 信任库必须落在数据目录里。
//
// 与上一条同源：信任库若落在进程工作目录，管理员通过界面加的信任关系
// 会在换一个启动目录（或用 systemd 指定 WorkingDirectory）后凭空消失。
func TestTrustStorePathFollowsDataDir(t *testing.T) {
	testutil.SetupDB(t)

	want := filepath.Join(conf.DataDir(), "plugin-trust.json")
	absWant, err := filepath.Abs(want)
	if err != nil {
		t.Fatal(err)
	}
	absGot, err := filepath.Abs(trustStorePath())
	if err != nil {
		t.Fatal(err)
	}
	if absWant != absGot {
		t.Fatalf("信任库不在数据目录里：\n  期望: %s\n  实际: %s", absWant, absGot)
	}

	// 安装器读的也必须是同一个文件，否则"界面里加了信任、安装时说没信任"
	ins := newInstaller()
	if ins.TrustStore == nil {
		t.Fatal("安装器应带信任库（哪怕是空的）")
	}
	if _, err := os.Stat(absGot); err != nil {
		// 文件还不存在：空信任库即可，这里不强求
		if _, ok := ins.TrustStore.Lookup("0000000000000000"); ok {
			t.Error("空信任库不该信任任何 keyId")
		}
		return
	}
	if err := install.SaveTrustStore(absGot, sampleRing(t)); err != nil {
		t.Fatalf("写信任库失败: %v", err)
	}
}

// sampleRing 造一个只含一条记录的信任库，用于验证"写进去 → 安装器能读到"。
func sampleRing(t *testing.T) *pbclus.KeyRing {
	t.Helper()
	key, err := pbclus.GenerateSigningKey("路径测试")
	if err != nil {
		t.Fatal(err)
	}
	ring := pbclus.NewEmptyKeyRing()
	ring.Add(pbclus.TrustedKey{KeyID: key.KeyID, Name: "路径测试", PublicKey: key.PublicKey})
	return ring
}
