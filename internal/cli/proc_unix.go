//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// detach 让子进程脱离当前会话（关闭终端后仍继续运行）。
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// terminationSignals 返回前台服务应响应并优雅退出的信号。
func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
