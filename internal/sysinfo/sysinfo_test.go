package sysinfo

import (
	"runtime"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30 秒"},
		{5*time.Minute + 9*time.Second, "5 分 9 秒"},
		{2*time.Hour + 5*time.Minute, "2 小时 5 分"},
		{3 * time.Hour, "3 小时"},
		{26*time.Hour + 3*time.Minute, "1 天 2 小时 3 分"},
		{24 * time.Hour, "1 天"},
		{50*time.Hour + 7*time.Minute, "2 天 2 小时 7 分"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v) = %q，期望 %q", c.d, got, c.want)
		}
	}
}

func TestCollectBasicFields(t *testing.T) {
	info := Collect()
	if info.OS != runtime.GOOS {
		t.Errorf("OS 应为 %q，实际 %q", runtime.GOOS, info.OS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch 应为 %q，实际 %q", runtime.GOARCH, info.Arch)
	}
	if info.CPUCount <= 0 {
		t.Error("CPU 核心数应大于 0")
	}
	if info.Uptime == "" {
		t.Error("运行时长不应为空")
	}
	if info.GoVersion == "" {
		t.Error("Go 版本不应为空")
	}
	if info.MemoryUsed > info.MemoryAll && info.MemoryAll != 0 {
		t.Errorf("已用内存不能大于总量: %d > %d", info.MemoryUsed, info.MemoryAll)
	}
}

func TestMemUsage(t *testing.T) {
	used, total := MemUsage()
	if total == 0 {
		t.Skip("当前平台无法读取物理内存，跳过")
	}
	if used > total {
		t.Errorf("已用内存不能大于总量: %d > %d", used, total)
	}
}

func TestUptimePositive(t *testing.T) {
	if Uptime() <= 0 {
		t.Error("进程运行时长应大于 0")
	}
}
