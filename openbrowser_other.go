//go:build !windows && !darwin

package main

import "os/exec"

// openBrowser 在类 Unix 系统上尝试用 xdg-open 打开指定 URL
func openBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
}
