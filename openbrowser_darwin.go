//go:build darwin

package main

import "os/exec"

// openBrowser 在 macOS 上用默认浏览器打开指定 URL
func openBrowser(url string) error {
	return exec.Command("open", url).Start()
}
