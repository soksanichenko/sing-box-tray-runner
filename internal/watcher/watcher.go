//go:build windows

package watcher

import (
	"os"
	"time"
)

// Watcher polls a set of files for modification time changes.
type Watcher struct {
	paths    []string
	modTimes map[string]time.Time
	onChange func(path string)
	stop     chan struct{}
}

func New(paths []string, onChange func(path string)) *Watcher {
	w := &Watcher{
		paths:    paths,
		modTimes: make(map[string]time.Time),
		onChange: onChange,
		stop:     make(chan struct{}),
	}
	// Record initial mtimes so first tick doesn't fire spuriously.
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			w.modTimes[p] = info.ModTime()
		}
	}
	return w
}

func (w *Watcher) Start() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.check()
			case <-w.stop:
				return
			}
		}
	}()
}

func (w *Watcher) Stop() {
	close(w.stop)
}

func (w *Watcher) check() {
	for _, p := range w.paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		prev, seen := w.modTimes[p]
		w.modTimes[p] = info.ModTime()
		if seen && info.ModTime().After(prev) {
			w.onChange(p)
		}
	}
}
