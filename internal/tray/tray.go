//go:build windows

package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"github.com/go-toast/toast"
	"golang.org/x/sys/windows"

	"github.com/zelgray/sing-box-tray/assets"
	"github.com/zelgray/sing-box-tray/internal/autostart"
	"github.com/zelgray/sing-box-tray/internal/config"
	"github.com/zelgray/sing-box-tray/internal/elevation"
	"github.com/zelgray/sing-box-tray/internal/logbuf"
	"github.com/zelgray/sing-box-tray/internal/logwin"
	"github.com/zelgray/sing-box-tray/internal/process"
	"github.com/zelgray/sing-box-tray/internal/proxy"
	"github.com/zelgray/sing-box-tray/internal/settings"
	"github.com/zelgray/sing-box-tray/internal/state"
	"github.com/zelgray/sing-box-tray/internal/tun"
	"github.com/zelgray/sing-box-tray/internal/watcher"
)

var (
	user32         = windows.NewLazySystemDLL("user32.dll")
	procMsgBox     = user32.NewProc("MessageBoxW")
)

type menuItems struct {
	settings  *systray.MenuItem
	start     *systray.MenuItem
	stop      *systray.MenuItem
	restart   *systray.MenuItem
	modeOff   *systray.MenuItem
	modeProxy *systray.MenuItem
	modeTUN   *systray.MenuItem
	autostart *systray.MenuItem
	viewLogs  *systray.MenuItem
	quit      *systray.MenuItem
}

// App orchestrates the sing-box process, proxy settings, and tray UI.
type App struct {
	cfg          *config.TrayConfig
	exeDir       string
	st           *state.Manager
	proc         *process.Manager
	logBuf       *logbuf.Buffer
	logFile      *os.File
	releaseMutex func()

	mu           sync.Mutex
	items        menuItems
	tempCfg      string
	pendingStart bool
}

func NewApp(cfg *config.TrayConfig, exeDir string, initialMode state.ProxyMode, releaseMutex func()) *App {
	a := &App{
		cfg:          cfg,
		exeDir:       exeDir,
		st:           state.NewManager(initialMode),
		logBuf:       logbuf.New(cfg.LogLines),
		releaseMutex: releaseMutex,
	}

	logPath := filepath.Join(exeDir, "sing-box-tray.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		a.logFile = f
		a.logBuf.SetFileOutput(f)
	}

	a.proc = process.NewManager(cfg.SingBoxPath, a.logBuf, a.onCrash)

	a.log("--- sing-box-tray started ---")
	a.log("exe dir:      %s", exeDir)
	a.log("sing-box:     %s", cfg.SingBoxPath)
	a.log("config:       %s", cfg.ConfigPath)
	a.log("default mode: %s", cfg.DefaultMode)
	a.log("initial mode: %s", initialMode)

	return a
}

func (a *App) log(format string, args ...any) {
	a.logBuf.Append(fmt.Sprintf("[tray] "+format, args...))
}

func (a *App) OnReady() {
	systray.SetIcon(assets.IconGrey)
	systray.SetTooltip("sing-box is stopped")

	mSettings := systray.AddMenuItem("Settings...", "Edit paths")
	systray.AddSeparator()
	mStart := systray.AddMenuItem("Start", "Start sing-box")
	mStop := systray.AddMenuItem("Stop", "Stop sing-box")
	mRestart := systray.AddMenuItem("Restart", "Restart sing-box")
	systray.AddSeparator()

	mMode := systray.AddMenuItem("Mode", "")
	mModeOff := mMode.AddSubMenuItem("Off", "")
	mModeProxy := mMode.AddSubMenuItem("System Proxy", "")
	mModeTUN := mMode.AddSubMenuItem("TUN (requires admin)", "")
	systray.AddSeparator()

	mAuto := systray.AddMenuItemCheckbox("Autostart", "Run on Windows logon", autostart.IsEnabled())
	systray.AddSeparator()

	mLogs := systray.AddMenuItem("View Logs", "")
	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Exit", "")

	mStop.Disable()
	mRestart.Disable()

	_, mode := a.st.Get()
	setModeChecks(mModeOff, mModeProxy, mModeTUN, mode)

	a.items = menuItems{
		settings:  mSettings,
		start:     mStart,
		stop:      mStop,
		restart:   mRestart,
		modeOff:   mModeOff,
		modeProxy: mModeProxy,
		modeTUN:   mModeTUN,
		autostart: mAuto,
		viewLogs:  mLogs,
		quit:      mQuit,
	}

	go a.watchState()
	go a.handleClicks()
	go a.watchConfigFiles()

	if a.cfg.StartOnLaunch {
		a.log("start_on_launch=true, starting...")
		go a.start()
	}
}

func (a *App) OnExit() {
	_, mode := a.st.Get()
	if a.proc.IsRunning() {
		a.log("exit: stopping process")
		a.proc.Stop(5 * time.Second)
	}
	a.cleanup(mode)
	a.log("--- sing-box-tray stopped ---")
	if a.logFile != nil {
		a.logFile.Close()
	}
}

// onCrash is called by the process manager when sing-box exits unexpectedly.
func (a *App) onCrash() {
	a.mu.Lock()
	_, mode := a.st.Get()
	tmp := a.tempCfg
	a.tempCfg = ""
	a.mu.Unlock()

	a.log("sing-box crashed (mode=%s)", mode)

	if mode == state.ModeSystemProxy {
		a.log("clearing system proxy after crash")
		_ = proxy.Clear()
	}
	if tmp != "" {
		_ = os.Remove(tmp)
	}

	a.st.Set(state.StateCrashed, mode)

	n := toast.Notification{
		AppID:   "sing-box-tray",
		Title:   "sing-box stopped",
		Message: "sing-box exited unexpectedly.",
	}
	_ = n.Push()
}

func (a *App) start() {
	a.mu.Lock()
	appState, mode := a.st.Get()
	if appState == state.StateStopping {
		// Will be started by stop() after it finishes.
		a.pendingStart = true
		a.mu.Unlock()
		return
	}
	if appState != state.StateStopped && appState != state.StateCrashed {
		a.mu.Unlock()
		return
	}
	a.st.Set(state.StateStarting, mode)
	a.mu.Unlock()

	a.log("starting (mode=%s)", mode)

	if mode == state.ModeTUN && !elevation.IsElevated() {
		a.log("not elevated, re-launching as admin")
		if err := elevation.RelaunchAsAdmin(fmt.Sprintf("--force-mode=%s", mode)); err != nil {
			a.log("elevation failed: %s", err)
			a.st.Set(state.StateCrashed, mode)
			return
		}
		a.releaseMutex() // Release mutex before exit so the elevated instance can acquire it.
		os.Exit(0)
	}

	cfgPath, err := a.prepareConfig(mode)
	if err != nil {
		a.log("prepare config failed: %s", err)
		a.cleanup(mode)
		a.st.Set(state.StateCrashed, mode)
		return
	}

	a.log("launching: %s run -c %s", a.cfg.SingBoxPath, cfgPath)

	if err := a.proc.Start(cfgPath); err != nil {
		a.log("process start failed: %s", err)
		a.cleanup(mode)
		a.st.Set(state.StateCrashed, mode)
		return
	}

	a.log("process started successfully")
	a.st.Set(state.StateRunning, mode)
}

func (a *App) stop() {
	a.mu.Lock()
	appState, mode := a.st.Get()
	if appState != state.StateRunning && appState != state.StateStarting && appState != state.StateCrashed {
		a.mu.Unlock()
		return
	}
	a.st.Set(state.StateStopping, mode)
	a.mu.Unlock()

	a.log("stopping (mode=%s)", mode)
	a.proc.Stop(5 * time.Second)
	a.cleanup(mode)
	a.st.Set(state.StateStopped, mode)
	a.log("stopped")

	a.mu.Lock()
	pending := a.pendingStart
	a.pendingStart = false
	a.mu.Unlock()

	if pending {
		a.log("pending start detected, starting...")
		a.start()
	}
}

func (a *App) switchMode(newMode state.ProxyMode) {
	a.mu.Lock()
	appState, curMode := a.st.Get()
	if curMode == newMode {
		a.mu.Unlock()
		return
	}
	wasRunning := appState == state.StateRunning
	a.mu.Unlock()

	a.log("switching mode: %s -> %s", curMode, newMode)

	if wasRunning {
		a.mu.Lock()
		a.st.Set(state.StateStopping, curMode)
		a.mu.Unlock()
		a.proc.Stop(5 * time.Second)
		a.cleanup(curMode)
	}

	a.st.Set(state.StateStopped, newMode)

	if wasRunning {
		a.start()
	}
}

func (a *App) prepareConfig(mode state.ProxyMode) (string, error) {
	switch mode {
	case state.ModeTUN:
		a.log("injecting TUN inbound into temp config")
		tmpPath, err := tun.InjectTUN(a.cfg.ConfigPath, a.cfg.TUN, a.cfg.SingBoxPath)
		if err != nil {
			return "", fmt.Errorf("inject TUN config: %w", err)
		}
		a.log("temp config written: %s", tmpPath)

		if err := tun.EnsureWintunDll(a.cfg.WintunDllPath, filepath.Dir(a.cfg.SingBoxPath)); err != nil {
			a.log("wintun.dll warning: %s", err)
		}
		a.mu.Lock()
		a.tempCfg = tmpPath
		a.mu.Unlock()
		return tmpPath, nil

	case state.ModeSystemProxy:
		tag := a.cfg.SystemProxyInbound
		a.log("looking for http/mixed inbound (tag=%q) in %s", tag, a.cfg.ConfigPath)
		host, port, err := config.FindInboundAddr(a.cfg.ConfigPath, tag)
		if err != nil {
			return "", fmt.Errorf("find proxy inbound: %w", err)
		}
		a.log("found inbound: %s:%d", host, port)
		if err := proxy.Set(host, fmt.Sprintf("%d", port)); err != nil {
			return "", fmt.Errorf("set system proxy: %w", err)
		}
		a.log("system proxy set to %s:%d", host, port)
		return a.cfg.ConfigPath, nil

	default:
		return a.cfg.ConfigPath, nil
	}
}

func (a *App) cleanup(mode state.ProxyMode) {
	if mode == state.ModeSystemProxy {
		a.log("clearing system proxy")
		if err := proxy.Clear(); err != nil {
			a.log("proxy.Clear error: %s", err)
		}
	}
	a.mu.Lock()
	tmp := a.tempCfg
	a.tempCfg = ""
	a.mu.Unlock()
	if tmp != "" {
		a.log("removing temp config: %s", tmp)
		_ = os.Remove(tmp)
	}
}

func (a *App) toggleAutostart() {
	_, mode := a.st.Get()
	if autostart.IsEnabled() {
		if err := autostart.Disable(); err != nil {
			a.log("disable autostart: %s", err)
			return
		}
		a.items.autostart.Uncheck()
		a.cfg.Autostart = false
		a.log("autostart disabled")
	} else {
		elevated := mode == state.ModeTUN
		if err := autostart.Enable(elevated); err != nil {
			a.log("enable autostart: %s", err)
			return
		}
		a.items.autostart.Check()
		a.cfg.Autostart = true
		a.log("autostart enabled (elevated=%v)", elevated)
	}
	_ = a.cfg.Save(a.exeDir)
}

func (a *App) openSettings() {
	settings.Show(a.cfg, func(updated *config.TrayConfig) {
		a.log("settings saved: sing-box=%s config=%s", updated.SingBoxPath, updated.ConfigPath)
		a.proc.SetSingBoxPath(updated.SingBoxPath)
		if err := updated.Save(a.exeDir); err != nil {
			a.log("save settings: %s", err)
		}
	})
}

// watchConfigFiles shows a restart prompt when config.json or tray-config.json change.
func (a *App) watchConfigFiles() {
	paths := []string{a.cfg.ConfigPath, filepath.Join(a.exeDir, "tray-config.json")}
	w := watcher.New(paths, func(path string) {
		a.log("file changed: %s", path)
		appState, _ := a.st.Get()
		if appState != state.StateRunning {
			return
		}
		msg := fmt.Sprintf("%s changed.\nRestart sing-box to apply changes?", filepath.Base(path))
		if msgBox(msg, "sing-box-tray") {
			go func() { a.stop(); a.start() }()
		}
	})
	w.Start()
}

func (a *App) watchState() {
	for range a.st.Subscribe() {
		appState, mode := a.st.Get()
		a.updateUI(appState, mode)
	}
}

func (a *App) updateUI(appState state.AppState, mode state.ProxyMode) {
	isRunning := appState == state.StateRunning || appState == state.StateStarting
	isBusy := appState == state.StateStarting || appState == state.StateStopping

	if isRunning {
		a.items.start.Disable()
		a.items.stop.Enable()
		a.items.restart.Enable()
	} else {
		a.items.start.Enable()
		a.items.stop.Disable()
		a.items.restart.Disable()
	}

	if isBusy {
		a.items.modeOff.Disable()
		a.items.modeProxy.Disable()
		a.items.modeTUN.Disable()
	} else {
		a.items.modeOff.Enable()
		a.items.modeProxy.Enable()
		a.items.modeTUN.Enable()
	}

	switch appState {
	case state.StateRunning:
		systray.SetIcon(assets.IconGreen)
		systray.SetTooltip("sing-box is running (" + mode.String() + ")")
	case state.StateCrashed:
		systray.SetIcon(assets.IconRed)
		systray.SetTooltip("sing-box crashed")
	default:
		systray.SetIcon(assets.IconGrey)
		systray.SetTooltip("sing-box is stopped")
	}

	setModeChecks(a.items.modeOff, a.items.modeProxy, a.items.modeTUN, mode)
}

func (a *App) handleClicks() {
	for {
		select {
		case <-a.items.settings.ClickedCh:
			a.openSettings()
		case <-a.items.start.ClickedCh:
			go a.start()
		case <-a.items.stop.ClickedCh:
			go a.stop()
		case <-a.items.restart.ClickedCh:
			go func() { a.stop(); a.start() }()
		case <-a.items.modeOff.ClickedCh:
			go a.switchMode(state.ModeOff)
		case <-a.items.modeProxy.ClickedCh:
			go a.switchMode(state.ModeSystemProxy)
		case <-a.items.modeTUN.ClickedCh:
			go a.switchMode(state.ModeTUN)
		case <-a.items.autostart.ClickedCh:
			go a.toggleAutostart()
		case <-a.items.viewLogs.ClickedCh:
			logwin.Show(a.logBuf, a.cfg.LogLines)
		case <-a.items.quit.ClickedCh:
			systray.Quit()
		}
	}
}

func setModeChecks(mOff, mProxy, mTUN *systray.MenuItem, mode state.ProxyMode) {
	mOff.Uncheck()
	mProxy.Uncheck()
	mTUN.Uncheck()
	switch mode {
	case state.ModeOff:
		mOff.Check()
	case state.ModeSystemProxy:
		mProxy.Check()
	case state.ModeTUN:
		mTUN.Check()
	}
}

// msgBox shows a Yes/No dialog and returns true if the user clicked Yes.
func msgBox(text, title string) bool {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	textPtr, _ := windows.UTF16PtrFromString(text)
	const mbYesNo = 0x04
	const idYes = 6
	ret, _, _ := procMsgBox.Call(0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		mbYesNo,
	)
	return int(ret) == idYes
}
