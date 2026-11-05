package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestNotificationCommand(t *testing.T) {
	cmd := notificationCommand("批量结束", `账号「张三」"完成"`)
	if runtime.GOOS != "darwin" {
		if cmd != nil {
			t.Fatal("非 macOS 不应构造通知命令")
		}
		return
	}
	if cmd == nil {
		t.Fatal("macOS 应构造通知命令")
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "display notification") || !strings.Contains(joined, "批量结束") {
		t.Fatalf("通知命令不对：%s", joined)
	}
	if !strings.Contains(joined, `\"完成\"`) {
		t.Fatalf("引号未转义：%s", joined)
	}
}
