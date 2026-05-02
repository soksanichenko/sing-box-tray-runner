package state

import (
	"fmt"
	"sync"
)

type AppState int

const (
	StateStopped AppState = iota
	StateStarting
	StateRunning
	StateStopping
	StateCrashed
)

func (s AppState) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateCrashed:
		return "crashed"
	default:
		return "unknown"
	}
}

type ProxyMode int

const (
	ModeOff ProxyMode = iota
	ModeSystemProxy
	ModeTUN
)

func (m ProxyMode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeSystemProxy:
		return "system_proxy"
	case ModeTUN:
		return "tun"
	default:
		return "unknown"
	}
}

func ParseMode(s string) (ProxyMode, error) {
	switch s {
	case "off":
		return ModeOff, nil
	case "system_proxy":
		return ModeSystemProxy, nil
	case "tun":
		return ModeTUN, nil
	default:
		return ModeOff, fmt.Errorf("unknown proxy mode: %q", s)
	}
}

type Manager struct {
	mu   sync.RWMutex
	app  AppState
	mode ProxyMode
	subs []chan struct{}
}

func NewManager(initialMode ProxyMode) *Manager {
	return &Manager{
		app:  StateStopped,
		mode: initialMode,
	}
}

func (m *Manager) Set(app AppState, mode ProxyMode) {
	m.mu.Lock()
	m.app = app
	m.mode = mode
	subs := m.subs
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) SetApp(app AppState) {
	m.mu.RLock()
	mode := m.mode
	m.mu.RUnlock()
	m.Set(app, mode)
}

func (m *Manager) Get() (AppState, ProxyMode) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.app, m.mode
}

func (m *Manager) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 4)
	m.mu.Lock()
	m.subs = append(m.subs, ch)
	m.mu.Unlock()
	return ch
}
