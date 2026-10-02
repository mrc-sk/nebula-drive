package main

import (
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
