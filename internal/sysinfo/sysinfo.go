// Package sysinfo 采集主机基础运行信息（跨平台，优先读取 /proc，非 Linux 环境回退到运行时数据）。
package sysinfo

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"porch/internal/netutil"
)

var startTime = time.Now()

// Info 运行状态
type Info struct {
	Hostname   string           `json:"hostname"`
	OS         string           `json:"os"`
	Arch       string           `json:"arch"`
	GoVersion  string           `json:"goVersion"`
	Uptime     string           `json:"uptime"`
	CPUCount   int              `json:"cpuCount"`
	LoadAvg    string           `json:"loadAvg"`
	MemoryUsed uint64           `json:"memoryUsed"` // MB
	MemoryAll  uint64           `json:"memoryAll"`  // MB
	Interfaces []netutil.IfInfo `json:"interfaces"`
}

// Collect 采集运行信息
func Collect() Info {
	info := Info{
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		GoVersion:  runtime.Version(),
		CPUCount:   runtime.NumCPU(),
		Uptime:     formatDuration(time.Since(startTime)),
		Interfaces: netutil.Interfaces(),
	}
	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}
	if load, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(load))
		if len(fields) >= 3 {
			info.LoadAvg = strings.Join(fields[:3], " ")
		}
	}
	used, total := readMemInfo()
	info.MemoryUsed = used
	info.MemoryAll = total
	if total == 0 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		info.MemoryUsed = ms.Sys / 1024 / 1024
	}
	return info
}

// MemUsage 返回物理内存已用量/总量（MB），供监控采样复用
func MemUsage() (usedMB, totalMB uint64) { return readMemInfo() }

// Uptime 返回进程已运行时长
func Uptime() time.Duration { return time.Since(startTime) }

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	// 只输出有意义的单位：刚启动时显示"29 秒"，而不是"0 分 29 秒"
	if days > 0 {
		s := strconv.Itoa(days) + " 天"
		if hours > 0 {
			s += " " + strconv.Itoa(hours) + " 小时"
		}
		if minutes > 0 {
			s += " " + strconv.Itoa(minutes) + " 分"
		}
		return s
	}
	if hours > 0 {
		s := strconv.Itoa(hours) + " 小时"
		if minutes > 0 {
			s += " " + strconv.Itoa(minutes) + " 分"
		}
		return s
	}
	if minutes > 0 {
		return strconv.Itoa(minutes) + " 分 " + strconv.Itoa(seconds) + " 秒"
	}
	return strconv.Itoa(seconds) + " 秒"
}

// Version 程序版本
var Version atomic.Value

func init() {
	Version.Store("1.0.0")
}
