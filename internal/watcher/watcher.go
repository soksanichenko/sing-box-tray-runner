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

// DirWatcher polls a directory listing (via list) and fires onChange whenever
// it differs from the previous poll — e.g. a config file was added, renamed,
// or removed. Unlike Watcher, it doesn't compare mtimes: the caller's list
// func decides what belongs in the listing (see config.ListConfigFiles).
type DirWatcher struct {
	dir      string
	list     func(dir string) ([]string, error)
	names    []string
	onChange func()
	stop     chan struct{}
}

func NewDir(dir string, list func(dir string) ([]string, error), onChange func()) *DirWatcher {
	names, _ := list(dir)
	return &DirWatcher{
		dir:      dir,
		list:     list,
		names:    names,
		onChange: onChange,
		stop:     make(chan struct{}),
	}
}

func (w *DirWatcher) Start() {
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

func (w *DirWatcher) Stop() {
	close(w.stop)
}

func (w *DirWatcher) check() {
	names, err := w.list(w.dir)
	if err != nil || sameNames(names, w.names) {
		return
	}
	w.names = names
	w.onChange()
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
