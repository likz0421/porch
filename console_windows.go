//go:build windows

package main

import (
	"os"
	"syscall"
)

// initConsole 切换控制台代码页为 UTF-8，避免 Windows 下中文日志显示为乱码。
func initConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	if proc := kernel32.NewProc("SetConsoleOutputCP"); proc != nil {
		// 65001 = CP_UTF8
		proc.Call(uintptr(65001))
	}
	if proc := kernel32.NewProc("SetConsoleCP"); proc != nil {
		proc.Call(uintptr(65001))
	}
}

// isElevated 判断是否以管理员权限运行：ACME 的 HTTP-01 需要监听 80 端口，
// 1024 以下端口在 Windows 上需要管理员权限。
func isElevated() bool {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	proc := shell32.NewProc("IsUserAnAdmin")
	if proc == nil {
		return false
	}
	ret, _, _ := proc.Call()
	return ret != 0
}

func warnIfNotElevated() {
	if isElevated() {
		return
	}
	_, _ = os.Stderr.WriteString("提示: 当前不是管理员权限，80 端口可能无法监听，ACME 证书申请会失败。\n")
	_, _ = os.Stderr.WriteString("      申请证书时请右键以管理员身份运行。\n")
}
