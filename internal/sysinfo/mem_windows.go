//go:build windows

package sysinfo

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx 对应 Windows 的 MEMORYSTATUSEX
type memoryStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

// readMemInfo 在 Windows 上通过 GlobalMemoryStatusEx 获取物理内存总量与可用量（MB）
func readMemInfo() (usedMB, totalMB uint64) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	if proc == nil {
		return 0, 0
	}
	var ms memoryStatusEx
	ms.dwLength = uint32(unsafe.Sizeof(ms))
	ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&ms)))
	if ret == 0 {
		return 0, 0
	}
	total := ms.ullTotalPhys / 1024 / 1024
	avail := ms.ullAvailPhys / 1024 / 1024
	if total == 0 {
		return 0, 0
	}
	return total - avail, total
}
