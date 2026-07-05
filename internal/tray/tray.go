//go:build windows

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"github.com/go-toast/toast"
	"golang.org/x/sys/windows"

	"github.com/zelgray/sing-box-tray/assets"
	"github.com/zelgray/sing-box-tray/internal/autostart"
	"github.com/zelgray/sing-box-tray/internal/config"
	"github.com/zelgray/sing-box-tray/internal/elevation"
	"github.com/zelgray/sing-box-tray/internal/i18n"
	"github.com/zelgray/sing-box-tray/internal/logbuf"
	"github.com/zelgray/sing-box-tray/internal/logwin"
	"github.com/zelgray/sing-box-tray/internal/process"
	"github.com/zelgray/sing-box-tray/internal/proxy"
	"github.com/zelgray/sing-box-tray/internal/selfupdate"
	"github.com/zelgray/sing-box-tray/internal/settings"
	"github.com/zelgray/sing-box-tray/internal/state"
	"github.com/zelgray/sing-box-tray/internal/tun"
	"github.com/zelgray/sing-box-tray/internal/updater"
	"github.com/zelgray/sing-box-tray/internal/version"
	"github.com/zelgray/sing-box-tray/internal/watcher"
)

const (
	appTitle = "sing-box-tray"

	singBoxOwner = "SagerNet"
	singBoxRepo  = "sing-box"
	singBoxName  = "sing-box"

	launcherOwner     = "soksanichenko"
	launcherRepo      = "sing-box-tray-runner"
	launcherAssetName = "sing_box_tray_runner.exe"

	// languagesMenuTitle is deliberately not translated — it's the control
	// that changes the language, so it must stay findable regardless of the
	// current UI language. Same reasoning for the language names themselves:
	// each is shown in its own script, not translated.
	languagesMenuTitle = "Languages"
	langLabelAuto      = "Auto"
	langLabelEN        = "English"
	langLabelRU        = "Русский"
	langLabelUA        = "Українська"
)

var (
	user32     = windows.NewLazySystemDLL("user32.dll")
	procMsgBox = user32.NewProc("MessageBoxW")
)

type menuItems struct {
	settings  *systray.MenuItem
	start     *systray.MenuItem
	stop      *systray.MenuItem
	restart   *systray.MenuItem
	mode      *systray.MenuItem
	modeOff   *systray.MenuItem
	modeProxy *systray.MenuItem
	modeTUN   *systray.MenuItem

	updates             *systray.MenuItem
	launcherCheckUpdate *systray.MenuItem
	launcherAutoUpdate  *systray.MenuItem
	singBoxCheckUpdate  *systray.MenuItem
	singBoxAutoUpdate   *systray.MenuItem
	singBoxPrerelease   *systray.MenuItem

	langAuto *systray.MenuItem
	langEN   *systray.MenuItem
	langRU   *systray.MenuItem
	langUA   *systray.MenuItem

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
	strs         i18n.Strings

	mu           sync.Mutex
	items        menuItems
	tempCfg      string
	pendingStart bool
}

func NewApp(cfg *config.TrayConfig, exeDir string, initialMode state.ProxyMode, releaseMutex func(), strs i18n.Strings) *App {
	a := &App{
		cfg:          cfg,
		exeDir:       exeDir,
		st:           state.NewManager(initialMode),
		logBuf:       logbuf.New(cfg.LogLines),
		releaseMutex: releaseMutex,
		strs:         strs,
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
	systray.SetTooltip(a.strs.TooltipStopped)

	mSettings := systray.AddMenuItem(a.strs.MenuSettings, a.strs.MenuSettingsTip)
	systray.AddSeparator()
	mStart := systray.AddMenuItem(a.strs.MenuStart, a.strs.MenuStartTip)
	mStop := systray.AddMenuItem(a.strs.MenuStop, a.strs.MenuStopTip)
	mRestart := systray.AddMenuItem(a.strs.MenuRestart, a.strs.MenuRestartTip)
	systray.AddSeparator()

	mMode := systray.AddMenuItem(a.strs.MenuMode, "")
	mModeOff := mMode.AddSubMenuItem(a.strs.ModeOff, "")
	mModeProxy := mMode.AddSubMenuItem(a.strs.ModeSystemProxy, "")
	mModeTUN := mMode.AddSubMenuItem(a.strs.ModeTUN, "")
	systray.AddSeparator()

	mUpdates := systray.AddMenuItem(a.strs.MenuUpdates, "")
	mUpdatesLauncher := mUpdates.AddSubMenuItem(appTitle, "")
	mLauncherCheck := mUpdatesLauncher.AddSubMenuItem(a.strs.MenuCheckUpdate, "")
	mLauncherAuto := mUpdatesLauncher.AddSubMenuItemCheckbox(a.strs.MenuAutoUpdate, "", a.cfg.LauncherUpdate.AutoUpdate)
	mUpdatesSingBox := mUpdates.AddSubMenuItem(singBoxName, "")
	mSingBoxCheck := mUpdatesSingBox.AddSubMenuItem(a.strs.MenuCheckUpdate, "")
	mSingBoxAuto := mUpdatesSingBox.AddSubMenuItemCheckbox(a.strs.MenuAutoUpdate, "", a.cfg.Update.AutoUpdate)
	mSingBoxPrerelease := mUpdatesSingBox.AddSubMenuItemCheckbox(a.strs.UsePrereleaseLabel, "", a.cfg.Update.Channel == "alpha")
	systray.AddSeparator()

	mLanguages := systray.AddMenuItem(languagesMenuTitle, "")
	mLangAuto := mLanguages.AddSubMenuItem(langLabelAuto, "")
	mLangEN := mLanguages.AddSubMenuItem(langLabelEN, "")
	mLangRU := mLanguages.AddSubMenuItem(langLabelRU, "")
	mLangUA := mLanguages.AddSubMenuItem(langLabelUA, "")
	systray.AddSeparator()

	mAuto := systray.AddMenuItemCheckbox(a.strs.MenuAutostart, a.strs.MenuAutostartTip, autostart.IsEnabled())
	systray.AddSeparator()

	mLogs := systray.AddMenuItem(a.strs.MenuViewLogs, "")
	systray.AddSeparator()

	mQuit := systray.AddMenuItem(a.strs.MenuExit, "")

	mStop.Disable()
	mRestart.Disable()

	_, mode := a.st.Get()
	setModeChecks(mModeOff, mModeProxy, mModeTUN, mode)
	setLanguageChecks(mLangAuto, mLangEN, mLangRU, mLangUA, a.cfg.Language)

	a.items = menuItems{
		settings:  mSettings,
		start:     mStart,
		stop:      mStop,
		restart:   mRestart,
		mode:      mMode,
		modeOff:   mModeOff,
		modeProxy: mModeProxy,
		modeTUN:   mModeTUN,

		updates:             mUpdates,
		launcherCheckUpdate: mLauncherCheck,
		launcherAutoUpdate:  mLauncherAuto,
		singBoxCheckUpdate:  mSingBoxCheck,
		singBoxAutoUpdate:   mSingBoxAuto,
		singBoxPrerelease:   mSingBoxPrerelease,

		langAuto: mLangAuto,
		langEN:   mLangEN,
		langRU:   mLangRU,
		langUA:   mLangUA,

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

	go a.checkSingBoxUpdate(false)
	go a.checkLauncherUpdate(false)
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
		AppID:   appTitle,
		Title:   a.strs.ToastCrashedTitle,
		Message: a.strs.ToastCrashedMsg,
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
		a.log("injecting system-proxy inbound into temp config")
		tmpPath, err := config.InjectSystemProxy(a.cfg.ConfigPath, a.cfg.SystemProxy)
		if err != nil {
			return "", fmt.Errorf("inject system-proxy config: %w", err)
		}
		a.log("temp config written: %s", tmpPath)

		tag := a.cfg.SystemProxyInbound
		a.log("looking for http/mixed inbound (tag=%q) in %s", tag, tmpPath)
		host, port, err := config.FindInboundAddr(tmpPath, tag)
		if err != nil {
			return "", fmt.Errorf("find proxy inbound: %w", err)
		}
		a.log("found inbound: %s:%d", host, port)
		if err := proxy.Set(host, fmt.Sprintf("%d", port)); err != nil {
			return "", fmt.Errorf("set system proxy: %w", err)
		}
		a.log("system proxy set to %s:%d", host, port)

		a.mu.Lock()
		a.tempCfg = tmpPath
		a.mu.Unlock()
		return tmpPath, nil

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

// managedSingBoxRoot is the directory the updater installs sing-box versions
// into, as managedRoot/<tag>/sing-box.exe.
func (a *App) managedSingBoxRoot() string {
	return filepath.Join(a.exeDir, "sing-box")
}

// checkSingBoxUpdate fetches the latest sing-box release for the configured
// channel. When interactive is false (startup check), it installs silently
// if cfg.Update.AutoUpdate is set, otherwise it only pushes a toast if a
// different version is available. When interactive is true (manual "Check
// for Updates" click) it always prompts via a Yes/No dialog before installing.
func (a *App) checkSingBoxUpdate(interactive bool) {
	rel, err := updater.FetchLatest(singBoxOwner, singBoxRepo, a.cfg.Update.Channel)
	if err != nil {
		a.log("sing-box update check failed: %s", err)
		if interactive {
			infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		}
		return
	}

	installed := updater.InstalledVersion(a.cfg.SingBoxPath, a.managedSingBoxRoot())
	if installed == rel.Tag {
		a.log("sing-box up to date (%s)", rel.Tag)
		if interactive {
			infoBox(fmt.Sprintf(a.strs.DialogUpdateNoneFmt, singBoxName, rel.Tag), appTitle)
		}
		return
	}

	a.log("sing-box update available: %s -> %s", installed, rel.Tag)

	if !interactive {
		if a.cfg.Update.AutoUpdate {
			a.log("auto-update: installing sing-box %s", rel.Tag)
			a.installSingBoxUpdate(rel, true)
			return
		}
		n := toast.Notification{
			AppID:   appTitle,
			Title:   a.strs.ToastUpdateTitle,
			Message: fmt.Sprintf(a.strs.ToastUpdateMsgFmt, singBoxName, rel.Tag),
		}
		_ = n.Push()
		return
	}

	current := installed
	if current == "" {
		current = a.strs.DialogVersionUnknown
	}
	if !msgBox(fmt.Sprintf(a.strs.DialogUpdateAvailableFmt, singBoxName, rel.Tag, current), appTitle) {
		return
	}
	a.installSingBoxUpdate(rel, false)
}

// installSingBoxUpdate downloads rel into the managed sing-box folder and
// switches the config to point at it. When silent is true (auto-update), a
// running sing-box is restarted without prompting; otherwise a Yes/No dialog
// asks first.
func (a *App) installSingBoxUpdate(rel *updater.Release, silent bool) {
	asset, err := rel.WindowsAmd64Asset()
	if err != nil {
		a.log("sing-box update failed: %s", err)
		if !silent {
			infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		}
		return
	}

	a.log("downloading sing-box %s", rel.Tag)
	exePath, err := updater.DownloadAndInstall(rel, asset, a.managedSingBoxRoot())
	if err != nil {
		a.log("sing-box update failed: %s", err)
		if !silent {
			infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		}
		return
	}

	a.cfg.SingBoxPath = exePath
	a.proc.SetSingBoxPath(exePath)
	if err := a.cfg.Save(a.exeDir); err != nil {
		a.log("save config after update: %s", err)
	}
	a.log("sing-box updated to %s", rel.Tag)

	if !a.proc.IsRunning() {
		return
	}
	if silent {
		a.log("auto-update: restarting sing-box")
		go func() { a.stop(); a.start() }()
		return
	}
	if msgBox(fmt.Sprintf(a.strs.DialogRestartNowFmt, rel.Tag), appTitle) {
		go func() { a.stop(); a.start() }()
	}
}

// checkLauncherUpdate fetches the latest tray launcher release from its own
// repo. When interactive is false (startup check), it self-updates silently
// if cfg.LauncherUpdate.AutoUpdate is set, otherwise it only pushes a toast.
// When interactive is true it prompts via a Yes/No dialog first.
func (a *App) checkLauncherUpdate(interactive bool) {
	rel, err := updater.FetchLatest(launcherOwner, launcherRepo, "stable")
	if err != nil {
		a.log("tray launcher update check failed: %s", err)
		if interactive {
			infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		}
		return
	}

	if version.Version == rel.Tag {
		a.log("tray launcher up to date (%s)", rel.Tag)
		if interactive {
			infoBox(fmt.Sprintf(a.strs.DialogUpdateNoneFmt, appTitle, rel.Tag), appTitle)
		}
		return
	}

	a.log("tray launcher update available: %s -> %s", version.Version, rel.Tag)

	if !interactive {
		if a.cfg.LauncherUpdate.AutoUpdate {
			a.log("auto-update: installing tray launcher %s", rel.Tag)
			a.installLauncherUpdate(rel)
			return
		}
		n := toast.Notification{
			AppID:   appTitle,
			Title:   a.strs.ToastUpdateTitle,
			Message: fmt.Sprintf(a.strs.ToastUpdateMsgFmt, appTitle, rel.Tag),
		}
		_ = n.Push()
		return
	}

	if !msgBox(fmt.Sprintf(a.strs.DialogUpdateAvailableFmt, appTitle, rel.Tag, version.Version), appTitle) {
		return
	}
	a.installLauncherUpdate(rel)
}

// installLauncherUpdate downloads the launcher's own release asset next to
// the running exe (same volume — required for the rename in selfupdate.Apply
// to work), stops sing-box if it's running (it would otherwise be orphaned
// once this process exits), swaps the exe, and relaunches. Mirrors the
// spawn-then-release-mutex-then-exit sequence already used by the UAC
// elevation relaunch in start().
func (a *App) installLauncherUpdate(rel *updater.Release) {
	asset, err := rel.AssetNamed(launcherAssetName)
	if err != nil {
		a.log("tray launcher update failed: %s", err)
		infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		a.log("tray launcher update failed: %s", err)
		infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		return
	}
	newPath := exePath + ".new"

	a.log("downloading tray launcher %s", rel.Tag)
	if err := updater.DownloadFile(asset.URL, newPath); err != nil {
		a.log("tray launcher update failed: %s", err)
		infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		return
	}

	if a.proc.IsRunning() {
		a.log("stopping sing-box before tray launcher self-update")
		a.stop()
	}

	if err := selfupdate.Apply(exePath, newPath); err != nil {
		a.log("tray launcher update failed: %s", err)
		infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		return
	}

	a.log("tray launcher updated to %s, relaunching", rel.Tag)
	cmd := exec.Command(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		a.log("relaunch failed: %s", err)
		infoBox(fmt.Sprintf(a.strs.DialogErrorFmt, err), appTitle)
		return
	}
	a.releaseMutex() // Release mutex before exit so the new instance can acquire it.
	os.Exit(0)
}

func (a *App) toggleLauncherAutoUpdate() {
	a.cfg.LauncherUpdate.AutoUpdate = !a.cfg.LauncherUpdate.AutoUpdate
	checkOrUncheck(a.items.launcherAutoUpdate, a.cfg.LauncherUpdate.AutoUpdate)
	_ = a.cfg.Save(a.exeDir)
	a.log("tray launcher auto-update: %v", a.cfg.LauncherUpdate.AutoUpdate)
}

func (a *App) toggleSingBoxAutoUpdate() {
	a.cfg.Update.AutoUpdate = !a.cfg.Update.AutoUpdate
	checkOrUncheck(a.items.singBoxAutoUpdate, a.cfg.Update.AutoUpdate)
	_ = a.cfg.Save(a.exeDir)
	a.log("sing-box auto-update: %v", a.cfg.Update.AutoUpdate)
}

func (a *App) toggleSingBoxPrerelease() {
	if a.cfg.Update.Channel == "alpha" {
		a.cfg.Update.Channel = "stable"
	} else {
		a.cfg.Update.Channel = "alpha"
	}
	checkOrUncheck(a.items.singBoxPrerelease, a.cfg.Update.Channel == "alpha")
	_ = a.cfg.Save(a.exeDir)
	a.log("sing-box update channel: %s", a.cfg.Update.Channel)
}

// applyLanguage recomputes a.strs for langCode, retitles the menu, and
// refreshes the dynamic tooltip/icon. Shared by the tray Languages submenu
// and the Settings window's Save handler.
func (a *App) applyLanguage(langCode string) {
	a.strs = i18n.Get(i18n.Resolve(langCode))
	a.refreshMenuTexts()
	setLanguageChecks(a.items.langAuto, a.items.langEN, a.items.langRU, a.items.langUA, langCode)
	appState, mode := a.st.Get()
	a.updateUI(appState, mode)
}

// switchLanguage persists langCode as the new UI language and applies it live.
func (a *App) switchLanguage(langCode string) {
	if a.cfg.Language == langCode {
		return
	}
	a.cfg.Language = langCode
	if err := a.cfg.Save(a.exeDir); err != nil {
		a.log("save config after language switch: %s", err)
	}
	a.log("language switched to %s", langCode)
	a.applyLanguage(langCode)
}

// refreshMenuTexts re-applies a.strs to every menu item with translated text,
// after a live language switch. Items with literal (untranslated) labels —
// proper nouns and the Languages picker itself — are left alone.
func (a *App) refreshMenuTexts() {
	a.items.settings.SetTitle(a.strs.MenuSettings)
	a.items.settings.SetTooltip(a.strs.MenuSettingsTip)
	a.items.start.SetTitle(a.strs.MenuStart)
	a.items.start.SetTooltip(a.strs.MenuStartTip)
	a.items.stop.SetTitle(a.strs.MenuStop)
	a.items.stop.SetTooltip(a.strs.MenuStopTip)
	a.items.restart.SetTitle(a.strs.MenuRestart)
	a.items.restart.SetTooltip(a.strs.MenuRestartTip)
	a.items.mode.SetTitle(a.strs.MenuMode)
	a.items.modeOff.SetTitle(a.strs.ModeOff)
	a.items.modeProxy.SetTitle(a.strs.ModeSystemProxy)
	a.items.modeTUN.SetTitle(a.strs.ModeTUN)
	a.items.updates.SetTitle(a.strs.MenuUpdates)
	a.items.launcherCheckUpdate.SetTitle(a.strs.MenuCheckUpdate)
	a.items.launcherAutoUpdate.SetTitle(a.strs.MenuAutoUpdate)
	a.items.singBoxCheckUpdate.SetTitle(a.strs.MenuCheckUpdate)
	a.items.singBoxAutoUpdate.SetTitle(a.strs.MenuAutoUpdate)
	a.items.singBoxPrerelease.SetTitle(a.strs.UsePrereleaseLabel)
	a.items.autostart.SetTitle(a.strs.MenuAutostart)
	a.items.autostart.SetTooltip(a.strs.MenuAutostartTip)
	a.items.viewLogs.SetTitle(a.strs.MenuViewLogs)
	a.items.quit.SetTitle(a.strs.MenuExit)
}

func (a *App) openSettings() {
	prevLang := a.cfg.Language
	settings.Show(a.cfg, a.strs, func(updated *config.TrayConfig) {
		a.log("settings saved: sing-box=%s config=%s", updated.SingBoxPath, updated.ConfigPath)
		a.proc.SetSingBoxPath(updated.SingBoxPath)
		if err := updated.Save(a.exeDir); err != nil {
			a.log("save settings: %s", err)
		}
		if updated.Language != prevLang {
			a.applyLanguage(updated.Language)
		}
		// Keep the tray menu checkboxes in sync with whatever Settings changed.
		checkOrUncheck(a.items.launcherAutoUpdate, updated.LauncherUpdate.AutoUpdate)
		checkOrUncheck(a.items.singBoxAutoUpdate, updated.Update.AutoUpdate)
		checkOrUncheck(a.items.singBoxPrerelease, updated.Update.Channel == "alpha")
	})
}

func checkOrUncheck(item *systray.MenuItem, checked bool) {
	if checked {
		item.Check()
	} else {
		item.Uncheck()
	}
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
		msg := fmt.Sprintf(a.strs.DialogConfigChangedFmt, filepath.Base(path))
		if msgBox(msg, appTitle) {
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
		systray.SetTooltip(fmt.Sprintf(a.strs.TooltipRunningFmt, a.modeLabel(mode)))
	case state.StateCrashed:
		systray.SetIcon(assets.IconRed)
		systray.SetTooltip(a.strs.TooltipCrashed)
	default:
		systray.SetIcon(assets.IconGrey)
		systray.SetTooltip(a.strs.TooltipStopped)
	}

	setModeChecks(a.items.modeOff, a.items.modeProxy, a.items.modeTUN, mode)
}

// modeLabel returns the localized display name for mode.
func (a *App) modeLabel(mode state.ProxyMode) string {
	switch mode {
	case state.ModeSystemProxy:
		return a.strs.ModeSystemProxy
	case state.ModeTUN:
		return a.strs.ModeTUN
	default:
		return a.strs.ModeOff
	}
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
		case <-a.items.launcherCheckUpdate.ClickedCh:
			go a.checkLauncherUpdate(true)
		case <-a.items.launcherAutoUpdate.ClickedCh:
			go a.toggleLauncherAutoUpdate()
		case <-a.items.singBoxCheckUpdate.ClickedCh:
			go a.checkSingBoxUpdate(true)
		case <-a.items.singBoxAutoUpdate.ClickedCh:
			go a.toggleSingBoxAutoUpdate()
		case <-a.items.singBoxPrerelease.ClickedCh:
			go a.toggleSingBoxPrerelease()
		case <-a.items.langAuto.ClickedCh:
			go a.switchLanguage("auto")
		case <-a.items.langEN.ClickedCh:
			go a.switchLanguage("en")
		case <-a.items.langRU.ClickedCh:
			go a.switchLanguage("ru")
		case <-a.items.langUA.ClickedCh:
			go a.switchLanguage("ua")
		case <-a.items.autostart.ClickedCh:
			go a.toggleAutostart()
		case <-a.items.viewLogs.ClickedCh:
			logwin.Show(a.logBuf, a.cfg.LogLines, a.strs)
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

func setLanguageChecks(mAuto, mEN, mRU, mUA *systray.MenuItem, langCode string) {
	mAuto.Uncheck()
	mEN.Uncheck()
	mRU.Uncheck()
	mUA.Uncheck()
	switch langCode {
	case "en":
		mEN.Check()
	case "ru":
		mRU.Check()
	case "ua":
		mUA.Check()
	default:
		mAuto.Check()
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

// infoBox shows an OK-only informational dialog.
func infoBox(text, title string) {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	textPtr, _ := windows.UTF16PtrFromString(text)
	const mbOK = 0x00
	procMsgBox.Call(0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		mbOK,
	)
}
