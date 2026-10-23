//go:build !windows && !linux

package metrics

// cpuPercent 在不支持采样的平台上返回不可用，面板会隐藏 CPU 曲线。
func cpuPercent() (float64, bool) { return 0, false }
