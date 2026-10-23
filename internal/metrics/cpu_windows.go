//go:build windows

package metrics

import (
	"sync"
	"syscall"
	"unsafe"
)

// filetime 对应 Windows FILETIME（100 纳秒为单位）
type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func (f filetime) uint64() uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemTimes = kernel32.NewProc("GetSystemTimes")

	cpuMu      sync.Mutex
	lastIdle   uint64
	lastKernel uint64
	lastUser   uint64
	cpuSeen    bool
)

// cpuPercent 通过 GetSystemTimes 计算两次调用之间的系统 CPU 使用率。
// 注意：kernel 时间已包含 idle 时间，因此 busy = (kernel+user) - idle。
func cpuPercent() (float64, bool) {
	var idle, kernel, user filetime
	ret, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		return 0, false
	}
	i, k, u := idle.uint64(), kernel.uint64(), user.uint64()

	cpuMu.Lock()
	defer cpuMu.Unlock()
	if !cpuSeen {
		lastIdle, lastKernel, lastUser = i, k, u
		cpuSeen = true
		return 0, true
	}
	dIdle := float64(i - lastIdle)
	dKernel := float64(k - lastKernel)
	dUser := float64(u - lastUser)
	lastIdle, lastKernel, lastUser = i, k, u

	total := dKernel + dUser
	if total <= 0 {
		return 0, true
	}
	usage := (total - dIdle) / total * 100
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage, true
}
