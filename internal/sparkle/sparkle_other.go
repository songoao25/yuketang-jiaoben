//go:build !darwin || !cgo

package sparkle

import "errors"

func Start() error { return errors.New("当前平台不支持 Sparkle") }

func CheckForUpdates() error { return errors.New("当前平台不支持 Sparkle") }
