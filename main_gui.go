//go:build !cli

package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"github.com/songoao25/yuketang-jiaoben/internal/cli"
	"github.com/songoao25/yuketang-jiaoben/internal/config"
)

//go:embed yuketang.js
var scriptSource string

//go:embed all:frontend
var frontendAssets embed.FS

func main() {
	args := normalizeArgs(os.Args[1:])
	// 规则（跨平台可预期）：无参数一律进入 GUI（Finder/资源管理器/命令行都一样）；
	// 任何 CLI 行为都必须显式写子命令，例如 `ykt start`、`ykt status`。
	if len(args) == 0 || args[0] == "gui" {
		runGUI()
		return
	}
	os.Exit(runCLI(args))
}

// normalizeArgs 过滤 Finder 等启动器可能追加的参数。
func normalizeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-psn_") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func runCLI(args []string) int {
	store, err := config.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "✖ 初始化配置目录失败："+err.Error())
		return 1
	}
	return cli.Run(args, cli.Env{
		Store:        store,
		ScriptSource: scriptSource,
		AppVersion:   appVersion,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
	})
}

func runGUI() {
	store, err := config.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "✖ 初始化配置目录失败："+err.Error())
		return
	}
	app, err := NewApp(store, scriptSource, appVersion)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✖ 初始化 App 失败："+err.Error())
		return
	}
	assets, err := fs.Sub(frontendAssets, "frontend")
	if err != nil {
		fmt.Fprintln(os.Stderr, "✖ 加载界面资源失败："+err.Error())
		return
	}

	opts := &options.App{
		Title:       "雨课助手",
		Width:       1000,
		Height:      720,
		MinWidth:    880,
		MinHeight:   620,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   app.startup,
		OnShutdown:  app.shutdown,
		Bind:        []interface{}{app},
		// 防止重复双击启动多个实例抢同一个端口（第二个实例会聚焦已有窗口）
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "yuketang-jiaoben-ykt-manager",
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "雨课助手",
				Message: "App v" + appVersion + " · 脚本 v" + app.scriptMeta.Version + "\nGPL-3.0 · 仅供学习交流",
			},
		},
		Windows: &windows.Options{},
		Linux:   &linux.Options{},
	}
	if runtime.GOOS == "darwin" {
		// 让侧栏的毛玻璃透出桌面材质
		opts.BackgroundColour = &options.RGBA{R: 0, G: 0, B: 0, A: 0}
	} else {
		// 其他平台用实色背景，避免透明窗口兼容问题
		opts.BackgroundColour = &options.RGBA{R: 28, G: 28, B: 30, A: 255}
	}

	if err := wails.Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "✖ 启动窗口失败："+err.Error())
	}
}
