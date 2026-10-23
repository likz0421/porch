package logger

import (
	"testing"
	"time"
)

func newTestBuffer() *Buffer {
	return &Buffer{
		entries: make([]Entry, 0, 16),
		subs:    make(map[uint64]chan Entry),
		level:   LevelInfo,
	}
}

func TestSinceReturnsNewerEntries(t *testing.T) {
	b := newTestBuffer()
	for i := 0; i < 5; i++ {
		b.write(LevelInfo, "test", "第 %d 条", i)
	}
	all := b.Since(0, 0)
	if len(all) != 5 {
		t.Fatalf("应返回 5 条，实际 %d", len(all))
	}
	// 序号必须严格递增
	for i := 1; i < len(all); i++ {
		if all[i].Seq <= all[i-1].Seq {
			t.Fatalf("日志序号未递增: %d <= %d", all[i].Seq, all[i-1].Seq)
		}
	}
	last3 := b.Since(0, 3)
	if len(last3) != 3 {
		t.Fatalf("limit=3 应返回 3 条，实际 %d", len(last3))
	}
	if last3[2].Message != "第 4 条" {
		t.Errorf("最后一条应是最新日志，实际 %q", last3[2].Message)
	}
	// since 过滤
	mid := all[2].Seq
	after := b.Since(mid, 0)
	if len(after) != 2 {
		t.Errorf("since=%d 应返回 2 条，实际 %d", mid, len(after))
	}
}

func TestRingBufferCapacity(t *testing.T) {
	b := newTestBuffer()
	for i := 0; i < bufSize+300; i++ {
		b.write(LevelInfo, "test", "x")
	}
	b.mu.Lock()
	n := len(b.entries)
	b.mu.Unlock()
	if n != bufSize {
		t.Errorf("环形缓冲应限制在 %d 条，实际 %d", bufSize, n)
	}
}

func TestLevelFilterAffectsSubscribers(t *testing.T) {
	b := newTestBuffer()
	b.SetLevel(LevelWarn)
	ch, cancel := b.Subscribe(4)
	defer cancel()

	b.write(LevelInfo, "test", "info 应被过滤")

	select {
	case e := <-ch:
		t.Fatalf("INFO 日志不应推送给订阅者，收到: %+v", e)
	case <-time.After(80 * time.Millisecond):
	}

	b.write(LevelError, "test", "error 必须送达")
	select {
	case e := <-ch:
		if e.Level != "ERROR" {
			t.Errorf("应为 ERROR，实际 %q", e.Level)
		}
	case <-time.After(time.Second):
		t.Fatal("ERROR 日志未送达订阅者")
	}

	// 被过滤的日志仍需写入缓冲，便于事后查看
	if got := b.Since(0, 0); len(got) != 2 {
		t.Errorf("缓冲应保留全部 2 条，实际 %d", len(got))
	}
}

func TestSubscribeCancel(t *testing.T) {
	b := newTestBuffer()
	ch, cancel := b.Subscribe(1)
	cancel()
	// 取消后写入不应 panic，也不应阻塞
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			b.write(LevelError, "test", "x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消订阅后写入发生阻塞")
	}
	select {
	case <-ch:
		t.Fatal("取消订阅后不应再收到日志")
	default:
	}
}

func TestLevelString(t *testing.T) {
	cases := map[Level]string{
		LevelDebug: "DEBUG",
		LevelInfo:  "INFO",
		LevelWarn:  "WARN",
		LevelError: "ERROR",
		Level(99):  "INFO",
	}
	for lv, want := range cases {
		if got := lv.String(); got != want {
			t.Errorf("Level(%d).String() = %q，期望 %q", int(lv), got, want)
		}
	}
}

func TestEntryHasTimestamp(t *testing.T) {
	b := newTestBuffer()
	b.write(LevelInfo, "mod", "hello")
	e := b.Since(0, 1)[0]
	if e.Time == "" || e.UnixMilli == 0 {
		t.Errorf("日志条目缺少时间信息: %+v", e)
	}
	if e.Module != "mod" || e.Message != "hello" {
		t.Errorf("日志字段不正确: %+v", e)
	}
}
