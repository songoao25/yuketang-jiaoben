//go:build cli

// 纯 Go CLI 入口：不依赖 cgo / Wails / 图形环境，可直接交叉编译到
// macOS / Windows / Linux。构建：go build -tags cli -o bin/ykt .
package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/songoao25/yuketang-jiaoben/internal/cli"
	"github.com/songoao25/yuketang-jiaoben/internal/config"
)

//go:embed yuketang.js
var scriptSource string

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "gui" {
		fmt.Fprintln(os.Stderr, "✖ 这是纯 CLI 版本，不含图形界面；请改用 ykt.app，或运行 ykt help 查看命令。")
		os.Exit(2)
	}
	store, err := config.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "✖ 初始化配置目录失败："+err.Error())
		os.Exit(1)
	}
	os.Exit(cli.Run(args, cli.Env{
		Store:        store,
		ScriptSource: scriptSource,
		AppVersion:   appVersion,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
	}))
}
