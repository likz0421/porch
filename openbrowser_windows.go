//go:build windows

package main

import "os/exec"

// openBrowser 在 Windows 上用默认浏览器打开指定 URL
func openBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
