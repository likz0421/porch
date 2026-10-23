package metrics

import (
	"testing"
)

func TestRound1(t *testing.T) {
	cases := map[float64]float64{
		1.24:  1.2,
		1.25:  1.3,
		1.26:  1.3,
		0:     0,
		-5:    0,
		99.99: 100,
	}
	for in, want := range cases {
		if got := round1(in); got != want {
			t.Errorf("round1(%v) = %v，期望 %v", in, got, want)
		}
	}
}

func TestCollectKeepsHistoryBounded(t *testing.T) {
	Reset()
	defer Reset()
	for i := 0; i < histSize+30; i++ {
		collect()
	}
	snap := Current()
	if len(snap.History) != histSize {
		t.Fatalf("历史采样点应被限制为 %d，实际 %d", histSize, len(snap.History))
	}
	// 采样点时间必须单调不减
	for i := 1; i < len(snap.History); i++ {
		if snap.History[i].T < snap.History[i-1].T {
			t.Fatalf("采样时间未单调递增: %d < %d", snap.History[i].T, snap.History[i-1].T)
		}
	}
}

func TestCurrentFields(t *testing.T) {
	Reset()
	defer Reset()
	collect()
	snap := Current()
	if snap.Goroutines <= 0 {
		t.Error("Goroutine 数应大于 0")
	}
	if snap.MemAllMB > 0 && snap.MemPercent > 100.5 {
		t.Errorf("内存占用率异常: %v", snap.MemPercent)
	}
	if snap.History == nil {
		t.Error("历史不应为 nil")
	}
	if len(snap.History) != 1 {
		t.Errorf("采样一次后应有 1 个历史点，实际 %d", len(snap.History))
	}
}

func TestStartIdempotent(t *testing.T) {
	Reset()
	Start()
	Start() // 重复调用不应重复启动采样协程或 panic
	Reset()
	snap := Current()
	if len(snap.History) > histSize {
		t.Errorf("历史长度异常: %d", len(snap.History))
	}
}

func TestSampleWithinRange(t *testing.T) {
	Reset()
	defer Reset()
	collect()
	s := Current().History[0]
	if s.CPU < 0 || s.CPU > 100 {
		t.Errorf("CPU 使用率应在 0-100 之间，实际 %v", s.CPU)
	}
	if s.Mem < 0 || s.Mem > 100.5 {
		t.Errorf("内存使用率应在 0-100 之间，实际 %v", s.Mem)
	}
	if s.Heap < 0 {
		t.Errorf("堆内存不应为负: %v", s.Heap)
	}
	if s.T <= 0 {
		t.Errorf("采样时间戳异常: %d", s.T)
	}
}
