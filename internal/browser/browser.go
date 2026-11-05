// Package browser 用系统默认方式打开 URL、在文件管理器中定位路径。
// 仅使用标准库 os/exec，不引入第三方依赖。
package browser

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OpenURL 用系统默认浏览器打开网址。
//
//	darwin:  open <url>
//	windows: rundll32 url.dll,FileProtocolHandler <url>
//	linux:   xdg-open <url>
func OpenURL(rawURL string) error {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return errors.New("打开的网址不能为空")
	}
	cmd := openCommand(target)
	if cmd == nil {
		return fmt.Errorf("当前平台 %s 不支持打开浏览器", runtime.GOOS)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("打开浏览器失败: %w", err)
	}
	return nil
}

// Reveal 在系统文件管理器中定位路径（选中文件或打开所在目录）。
//
//	darwin:  open -R <path>
//	windows: explorer /select,<path>
//	linux:   xdg-open <dir(path)>
func Reveal(path string) error {
	target := strings.TrimSpace(path)
	if target == "" {
		return errors.New("定位的路径不能为空")
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	cmd := revealCommand(target)
	if cmd == nil {
		return fmt.Errorf("当前平台 %s 不支持定位文件", runtime.GOOS)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("定位文件失败: %w", err)
	}
	return nil
}

// openCommand 按平台构造打开 URL 的命令（不执行，便于测试）。
func openCommand(target string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "linux":
		return exec.Command("xdg-open", target)
	default:
		return nil
	}
}

// revealCommand 按平台构造定位文件的命令（不执行，便于测试）。
func revealCommand(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path)
	case "windows":
		return exec.Command("explorer", "/select,"+path)
	case "linux":
		return exec.Command("xdg-open", filepath.Dir(path))
	default:
		return nil
	}
}
