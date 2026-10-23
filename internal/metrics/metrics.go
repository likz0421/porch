// Package metrics 提供轻量级运行指标采集：CPU 使用率、内存占用、Goroutine 与堆用量，
// 并保留最近若干分钟的采样历史，供面板绘制实时曲线（不依赖任何第三方库）。
package metrics

import (
	"runtime"
	"sync"
	"time"

	"porch/internal/sysinfo"
)

const (
	// histSize 历史采样点数：90 个点 × 2 秒 = 最近 3 分钟
	histSize = 90
	// sampleInterval 采样间隔
	sampleInterval = 2 * time.Second
)

// Sample 单个采样点
type Sample struct {
	T    int64   `json:"t"`    // 采样时刻（Unix 毫秒）
	CPU  float64 `json:"cpu"`  // 系统 CPU 使用率 0-100
	Mem  float64 `json:"mem"`  // 内存使用率 0-100
	Heap float64 `json:"heap"` // Go 堆分配（MB）
}

// Snapshot 指标快照
type Snapshot struct {
	CPUPercent   float64  `json:"cpuPercent"`
	CPUAvailable bool     `json:"cpuAvailable"`
	MemPercent   float64  `json:"memPercent"`
	MemUsedMB    uint64   `json:"memUsedMB"`
	MemAllMB     uint64   `json:"memAllMB"`
	Goroutines   int      `json:"goroutines"`
	HeapMB       float64  `json:"heapMB"`
	UptimeSec    int64    `json:"uptimeSec"`
	History      []Sample `json:"history"`
}

var (
	mu       sync.RWMutex
	history  []Sample
	latest   Sample
	cpuAvail bool
	startAt  = time.Now()
	startOne sync.Once
)

// Start 启动采样循环（幂等，可重复调用）
func Start() {
	startOne.Do(func() {
		collect()
		go func() {
			ticker := time.NewTicker(sampleInterval)
			defer ticker.Stop()
			for range ticker.C {
				collect()
			}
		}()
	})
}

func collect() {
	cpu, ok := cpuPercent()
	used, total := sysinfo.MemUsage()
	var memPct float64
	if total > 0 {
		memPct = float64(used) / float64(total) * 100
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s := Sample{
		T:    time.Now().UnixMilli(),
		CPU:  round1(cpu),
		Mem:  round1(memPct),
		Heap: round1(float64(ms.HeapAlloc) / 1024 / 1024),
	}
	mu.Lock()
	latest = s
	cpuAvail = ok
	history = append(history, s)
	if len(history) > histSize {
		history = history[len(history)-histSize:]
	}
	mu.Unlock()
}

// Current 返回当前指标与历史曲线
func Current() Snapshot {
	mu.RLock()
	h := make([]Sample, len(history))
	copy(h, history)
	last := latest
	avail := cpuAvail
	mu.RUnlock()

	used, total := sysinfo.MemUsage()
	var memPct float64
	if total > 0 {
		memPct = float64(used) / float64(total) * 100
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	return Snapshot{
		CPUPercent:   last.CPU,
		CPUAvailable: avail,
		MemPercent:   round1(memPct),
		MemUsedMB:    used,
		MemAllMB:     total,
		Goroutines:   runtime.NumGoroutine(),
		HeapMB:       round1(float64(ms.HeapAlloc) / 1024 / 1024),
		UptimeSec:    int64(time.Since(startAt).Seconds()),
		History:      h,
	}
}

// Reset 清空历史（主要供测试使用）
func Reset() {
	mu.Lock()
	history = nil
	latest = Sample{}
	mu.Unlock()
}

func round1(v float64) float64 {
	if v < 0 {
		return 0
	}
	return float64(int(v*10+0.5)) / 10
}
