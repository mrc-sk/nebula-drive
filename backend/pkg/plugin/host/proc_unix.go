//go:build !windows

package host

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// sysProcAttr 让插件进程独立成进程组。
//
// Setpgid 是关键：只有独立进程组，kill(-pgid) 才能连带干掉插件自己
// 拉起的孙进程。少了它，插件挂掉会在系统里留下孤儿进程。
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// terminate 发 SIGTERM，给插件机会做清理。
func terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process not started")
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil && pgid > 0 {
		// 负 pgid = 整个进程组
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err == nil {
			return nil
		}
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}

// killTree 发 SIGKILL 强杀进程组（SIGTERM 失效时的兜底）。
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process not started")
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil && pgid > 0 {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			return nil
		}
	}
	return cmd.Process.Kill()
}

func stdenv() []string { return os.Environ() }

func ensureDir(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }

func isClosedErr(err error) bool {
	return errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EPIPE) ||
		strings.Contains(err.Error(), "file already closed")
}

// PlatformName 当前平台名。
const PlatformName = "unix"
