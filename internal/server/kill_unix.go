//go:build !windows

package server

import (
	"errors"
	"os"
	"syscall"
)

// terminate 在类 Unix 系统上先发送 SIGTERM，让后台服务有机会清理 server.json。
func terminate(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		// 进程不存在（或已退出）视为成功。
		return nil
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}

// forceTerminate 在类 Unix 系统上发送 SIGKILL，作为 SIGTERM 超时后的兜底（QA L2）。
func forceTerminate(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := process.Signal(syscall.SIGKILL); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}
