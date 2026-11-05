//go:build !windows

package updater

import (
	"os/exec"
	"syscall"
)

// startDetached 让更新脚本脱离当前会话，App 退出后继续执行。
func startDetached(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	return cmd.Start()
}
