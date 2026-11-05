// Package batch 提供多账号批量运行的核心能力：
// 账号独立浏览器 profile、headless Chrome 启动、CDP(pipe) 驱动与队列执行。
//
// 设计约束（见 .private/batch-plan.md）：
//   - 只使用标准库，不引入第三方依赖；
//   - 每个账号一个 user-data-dir，互不共享 Cookie；
//   - 首次登录用可见窗口，批量运行用 --headless=new；
//   - 不保存密码，登录态由浏览器 profile 承担。
package batch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ChromePath 返回可用于 headless 运行的 Chrome/Chromium 可执行文件路径。
// 可用环境变量 YKT_CHROME 显式指定；找不到时返回错误，由上层决定是否下载/提示。
func ChromePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("YKT_CHROME")); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("YKT_CHROME 指向的文件不存在：%s", override)
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	case "windows":
		roots := []string{
			os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA"),
		}
		for _, root := range roots {
			if root == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(root, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(root, `Chromium\Application\chrome.exe`),
				filepath.Join(root, `Microsoft\Edge\Application\msedge.exe`),
			)
		}
	case "linux":
		candidates = []string{
			"/usr/bin/google-chrome", "/usr/bin/google-chrome-stable",
			"/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/microsoft-edge",
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("没有找到 Chrome/Chromium；可用 YKT_CHROME 指定路径")
}

// browserArgs 组装启动参数；visible=true 时用于首次手动登录，否则无头批量运行。
func browserArgs(profileDir string, visible bool) []string {
	args := []string{
		"--remote-debugging-pipe",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-background-timer-throttling",
		"--disable-sync",
		"--disable-features=Translate,MediaRouter",
		"--disable-component-update",
		"--disable-default-apps",
		"--mute-audio",
	}
	if visible {
		args = append(args, "--window-size=1180,820", "--window-position=80,80")
	} else {
		args = append(args,
			"--headless=new",
			"--disable-gpu",
			"--hide-scrollbars",
			"--window-size=1440,900",
		)
	}
	return append(args, "about:blank")
}

// chromeCommand 构造启动命令（不执行，便于测试参数）。
func chromeCommand(chromePath, profileDir string, visible bool) *exec.Cmd {
	return exec.Command(chromePath, browserArgs(profileDir, visible)...)
}
