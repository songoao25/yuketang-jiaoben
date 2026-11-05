//go:build windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// Windows 进程创建标志（syscall 未导出的常用常量，按 Win32 文档定义）。
const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// detach 让子进程脱离当前控制台（关闭终端后仍继续运行）。
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
		HideWindow:    true,
	}
}

// terminationSignals 返回前台服务应响应并优雅退出的信号。
func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
