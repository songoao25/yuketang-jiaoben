//go:build !darwin || !cgo

package main

// 非 macOS 或未启用 cgo 时不做 Dock 图标切换（bundle 里仍是静态图标）。

func setDockIconPNG([]byte) {}

func systemIsDarkMode() bool { return false }
