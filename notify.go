//go:build !cli

package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// notificationCommand 构造系统通知命令（便于测试；不执行）。
// macOS 用 osascript display notification；其他平台暂不实现。
func notificationCommand(title, message string) *exec.Cmd {
	if runtime.GOOS != "darwin" {
		return nil
	}
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, applescriptEscape(message), applescriptEscape(title))
	return exec.Command("osascript", "-e", script)
}

func applescriptEscape(text string) string {
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, `"`, `\"`)
	text = strings.ReplaceAll(text, "\n", " ")
	return text
}

// notifyUser 异步发送系统通知；失败静默（不影响批量）。
func notifyUser(title, message string) {
	cmd := notificationCommand(title, message)
	if cmd == nil {
		return
	}
	go func() {
		if err := cmd.Run(); err != nil {
			_ = err // 通知失败不阻塞任务
		}
	}()
}
