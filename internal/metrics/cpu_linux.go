//go:build linux

package metrics

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

var (
	cpuMu     sync.Mutex
	lastTotal uint64
	lastIdle  uint64
	cpuSeen   bool
)

// cpuPercent 读取 /proc/stat 首行，计算两次采样之间的 CPU 使用率。
func cpuPercent() (float64, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	line := strings.SplitN(string(b), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, false
	}
	var total uint64
	vals := make([]uint64, 0, len(fields)-1)
	for _, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, false
		}
		vals = append(vals, v)
		total += v
	}
	// 第 4 列为 idle，第 5 列为 iowait（若有）
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4]
	}

	cpuMu.Lock()
	defer cpuMu.Unlock()
	if !cpuSeen {
		lastTotal, lastIdle = total, idle
		cpuSeen = true
		return 0, true
	}
	dTotal := total - lastTotal
	dIdle := idle - lastIdle
	lastTotal, lastIdle = total, idle
	if dTotal == 0 {
		return 0, true
	}
	usage := float64(dTotal-dIdle) / float64(dTotal) * 100
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage, true
}
