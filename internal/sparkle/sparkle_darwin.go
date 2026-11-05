//go:build darwin && cgo

// Package sparkle 用运行时加载的方式接入 Sparkle 2（macOS 标准更新框架）。
// 不在链接期依赖 Sparkle.framework：App 启动后从 Contents/Frameworks 里 dlopen；
// 这样 Wails 构建不需要额外链接参数，框架由签名脚本复制进 .app。
package sparkle

/*
#cgo LDFLAGS: -framework Foundation
int cxSparkleInit(void);
int cxSparkleCheck(void);
const char *cxSparkleLastError(void);
*/
import "C"

import (
	"errors"
	"strings"
)

// Start 初始化 Sparkle updater（读取 Info.plist 里的 SUFeedURL / SUPublicEDKey）。
func Start() error {
	if C.cxSparkleInit() == 0 {
		message := strings.TrimSpace(C.GoString(C.cxSparkleLastError()))
		if message == "" {
			message = "Sparkle 初始化失败"
		}
		return errors.New(message)
	}
	return nil
}

// CheckForUpdates 打开 Sparkle 的检查更新 UI（已是最新/失败也由 Sparkle 展示）。
func CheckForUpdates() error {
	if C.cxSparkleCheck() == 0 {
		message := strings.TrimSpace(C.GoString(C.cxSparkleLastError()))
		if message == "" {
			message = "Sparkle 检查更新失败"
		}
		return errors.New(message)
	}
	return nil
}
