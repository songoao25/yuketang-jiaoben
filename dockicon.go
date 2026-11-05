package main

// dockicon.go：按系统外观切换 Dock / ⌘Tab 图标（与「超星助手」同一套做法）。
// macOS 的 .icns 不支持深浅色外观变体，bundle 图标固定用亮色版；
// 运行时通过 NSApplication.applicationIconImage 切到纸/墨两版，并跟随系统外观变化。
// 图标字标统一用思源宋体「雨」，视觉居中、四边留白一致。

import (
	_ "embed"
	"time"
)

//go:embed resources/icon-light.png
var dockIconLight []byte

//go:embed resources/icon-dark.png
var dockIconDark []byte

func startDockIconSync() {
	apply := func(dark bool) {
		if dark {
			setDockIconPNG(dockIconDark)
		} else {
			setDockIconPNG(dockIconLight)
		}
	}
	dark := systemIsDarkMode()
	apply(dark)
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			current := systemIsDarkMode()
			if current != dark {
				dark = current
				apply(dark)
			}
		}
	}()
}
