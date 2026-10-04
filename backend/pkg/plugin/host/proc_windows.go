//go:build windows

package host

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// sysProcAttr 让插件进程独立成组且不弹控制台。
//
// CREATE_NEW_PROCESS_GROUP 让插件能自成一个进程组，
// CREATE_NO_WINDOW 避免服务环境下插件弹出黑框（后台进程不该有控制台）。
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000200 | 0x08000000, // CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
	}
}

// terminate 请求进程终止。
//
// Windows 的 TerminateProcess 不可被拦截，插件没有机会执行清理逻辑。
// 所以调用顺序必须先 RPC shutdown，这里只作兜底。
func terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process not started")
	}
	return cmd.Process.Kill()
}

// killTree 强杀进程树。
//
// Windows 没有进程组 kill，需要 taskkill /T /F 递归杀子孙进程 ——
// 插件可能自己拉起辅助进程，只杀父进程会留下孤儿进程占着端口/文件。
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process not started")
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	path, err := exec.LookPath("taskkill")
	if err != nil {
		// PATH 里没有就试 System32 —— 服务环境下继承的 PATH 常常很精简
		if _, serr := os.Stat(`C:\Windows\System32\taskkill.exe`); serr != nil {
			return err
		}
		path = `C:\Windows\System32\taskkill.exe`
	}
	out, err := exec.Command(path, "/T", "/F", "/PID", pid).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return errors.New(msg + ": " + err.Error())
	}
	return nil
}

func stdenv() []string { return os.Environ() }

func ensureDir(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }

func isClosedErr(err error) bool {
	return errors.Is(err, os.ErrClosed) || strings.Contains(err.Error(), "file already closed")
}

// PlatformName 当前平台名（写入日志，便于管理员定位问题）。
const PlatformName = "windows"
