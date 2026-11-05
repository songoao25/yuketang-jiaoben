//go:build windows

package server

import (
	"errors"
	"os"
)

// terminate 在 Windows 上直接结束进程（SIGTERM 不可用）。
func terminate(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		// Windows 上进程不存在时 FindProcess 会报错，按契约视为成功。
		return nil
	}
	if err := process.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return nil
}

// forceTerminate 在 Windows 上同样调用 TerminateProcess 强制结束（QA L2 兜底）。
func forceTerminate(pid int) error {
	return terminate(pid)
}
