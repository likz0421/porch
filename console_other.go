//go:build !windows

package main

// initConsole 非 Windows 平台无需切换控制台代码页
func initConsole() {}

// warnIfNotElevated 类 Unix 平台上 80 端口需要 root，启动时给出提示
func warnIfNotElevated() {}
