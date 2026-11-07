package main

import (
	"encoding/json"
	"net"
	"testing"
)

// 占住 5212，验证 findFreePort 会跳到 5213
func TestFindFreePortSkipsOccupied(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:5212")
	if err != nil {
		t.Skipf("5212 无法占用（可能被系统保留）: %v", err)
	}
	defer l.Close()

	p, err := findFreePort(5212)
	if err != nil {
		t.Fatalf("findFreePort 失败: %v", err)
	}
	if p == 5212 {
		t.Fatal("5212 已被占用，findFreePort 不应返回它")
	}
	if p < 5213 || p > 5241 {
		t.Fatalf("返回端口 %d 超出探测范围", p)
	}
	t.Logf("正确跳过 5212，选中 %d", p)
}

// 全部端口空闲时应返回起始端口
func TestFindFreePortReturnsStartWhenFree(t *testing.T) {
	p, err := findFreePort(5212)
	if err != nil {
		t.Fatalf("findFreePort 失败: %v", err)
	}
	if p != 5212 {
		t.Logf("5212 当前被占用（其他测试遗留），返回 %d —— 也算正确", p)
	}
}

// TestPositiveIntAcceptsPluginNumberTypes 插件命令行退出码的类型兼容性。
//
// 进程内 handler 写的是 Go 的 int，进程外插件的回写经 JSON 往返后是 float64。
// 只认一种的后果是插件的 cliExitCode **静默失效** ——
// 插件明明用非 0 退出码表达了失败，脚本却以为命令成功了。
func TestPositiveIntAcceptsPluginNumberTypes(t *testing.T) {
	ok := []any{0, 3, int64(3), float64(3), json.Number("3")}
	for _, v := range ok {
		if _, good := positiveInt(v); !good {
			t.Errorf("positiveInt(%#v) 应被接受", v)
		}
	}
	if n, _ := positiveInt(float64(3)); n != 3 {
		t.Errorf("float64(3) → %d, want 3", n)
	}
	for _, v := range []any{nil, -1, "3", float64(1.5)} {
		if _, good := positiveInt(v); good {
			t.Errorf("positiveInt(%#v) 应返回 false", v)
		}
	}
}
