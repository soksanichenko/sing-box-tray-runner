package logbuf

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Buffer is a thread-safe circular buffer of log lines that optionally
// mirrors all entries to a file writer with timestamps.
type Buffer struct {
	mu      sync.RWMutex
	data    []string
	cap     int
	subs    []chan struct{}
	fileOut io.Writer
}

func New(capacity int) *Buffer {
	return &Buffer{
		data: make([]string, 0, capacity),
		cap:  capacity,
	}
}

// SetFileOutput enables mirroring all future Append calls to w with a
// timestamp prefix. Safe to call once before the app starts processing.
func (b *Buffer) SetFileOutput(w io.Writer) {
	b.mu.Lock()
	b.fileOut = w
	b.mu.Unlock()
}

func (b *Buffer) Append(line string) {
	b.mu.Lock()
	if len(b.data) >= b.cap {
		b.data = b.data[1:]
	}
	b.data = append(b.data, line)
	subs := b.subs
	out := b.fileOut
	b.mu.Unlock()

	if out != nil {
		ts := time.Now().Format("2006-01-02 15:04:05")
		fmt.Fprintf(out, "[%s] %s\n", ts, line)
	}

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (b *Buffer) Lines() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, len(b.data))
	copy(out, b.data)
	return out
}

// Subscribe returns a channel that receives a signal on every Append call.
func (b *Buffer) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 8)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}
