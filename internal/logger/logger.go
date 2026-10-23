// Package logger 提供一个进程内环形日志缓冲，Web 界面与 LUCI 均通过该缓冲读取运行日志。
package logger

import (
	"fmt"
	"sync"
	"time"
)

// Level 日志级别
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	}
	return "INFO"
}

// Entry 单条日志
type Entry struct {
	Seq       uint64 `json:"seq"`
	Time      string `json:"time"`
	Level     string `json:"level"`
	Module    string `json:"module"`
	Message   string `json:"message"`
	UnixMilli int64  `json:"unixMilli"`
}

const bufSize = 2000

// Buffer 环形日志缓冲
type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	seq     uint64
	subs    map[uint64]chan Entry
	nextSub uint64
	level   Level
}

var defaultBuffer = &Buffer{
	entries: make([]Entry, 0, bufSize),
	subs:    make(map[uint64]chan Entry),
	level:   LevelInfo,
}

// SetLevel 设置日志级别
func SetLevel(l Level) { defaultBuffer.SetLevel(l) }

// SetLevel 设置日志级别
func (b *Buffer) SetLevel(l Level) {
	b.mu.Lock()
	b.level = l
	b.mu.Unlock()
}

func (b *Buffer) write(l Level, module, format string, args ...interface{}) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	now := time.Now()
	e := Entry{
		Time:      now.Format("2006-01-02 15:04:05"),
		Level:     l.String(),
		Module:    module,
		Message:   msg,
		UnixMilli: now.UnixMilli(),
	}
	b.mu.Lock()
	b.seq++
	e.Seq = b.seq
	b.entries = append(b.entries, e)
	if len(b.entries) > bufSize {
		b.entries = b.entries[len(b.entries)-bufSize:]
	}
	subs := make([]chan Entry, 0, len(b.subs))
	for _, c := range b.subs {
		subs = append(subs, c)
	}
	skip := l < b.level && l != LevelError
	b.mu.Unlock()

	if skip {
		return
	}
	// 控制台输出
	fmt.Printf("[%s][%s][%s] %s\n", e.Time, e.Level, module, msg)

	for _, c := range subs {
		select {
		case c <- e:
		default:
		}
	}
}

// Debug 调试日志
func Debug(module, format string, args ...interface{}) {
	defaultBuffer.write(LevelDebug, module, format, args...)
}

// Info 信息日志
func Info(module, format string, args ...interface{}) {
	defaultBuffer.write(LevelInfo, module, format, args...)
}

// Warn 警告日志
func Warn(module, format string, args ...interface{}) {
	defaultBuffer.write(LevelWarn, module, format, args...)
}

// Error 错误日志
func Error(module, format string, args ...interface{}) {
	defaultBuffer.write(LevelError, module, format, args...)
}

// Since 返回序号大于 since 的日志条目
func Since(since uint64, limit int) []Entry { return defaultBuffer.Since(since, limit) }

// Since 返回序号大于 since 的日志条目
func (b *Buffer) Since(since uint64, limit int) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Entry, 0, len(b.entries))
	for _, e := range b.entries {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Subscribe 订阅实时日志，返回的 cancel 用于取消订阅
func Subscribe(size int) (<-chan Entry, func()) { return defaultBuffer.Subscribe(size) }

// Subscribe 订阅实时日志
func (b *Buffer) Subscribe(size int) (<-chan Entry, func()) {
	if size <= 0 {
		size = 256
	}
	ch := make(chan Entry, size)
	b.mu.Lock()
	b.nextSub++
	id := b.nextSub
	b.subs[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}
