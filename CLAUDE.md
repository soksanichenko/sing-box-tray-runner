# sing-box-tray — project notes for Claude

## Build

Windows-only project, cross-compiled from Linux:

```sh
make build
# or manually:
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -ldflags="-H windowsgui -s -w" \
  -o build/sing_box_tray_runner.exe .
```

`rsrc.syso` in the repo root is a pre-generated Windows resource object that embeds `app.manifest` (Common Controls v6 dependency). The Go toolchain links it automatically. Without it, `lxn/walk` windows fail silently. Regenerate with:

```sh
~/go/bin/rsrc -manifest app.manifest -o rsrc.syso
```

## Architecture

```
main.go               — mutex, UAC elevation for TUN, systray.Run
internal/
  tray/tray.go        — App struct: orchestrates everything, handles menu clicks
  state/state.go      — thread-safe AppState × ProxyMode state machine with subscriptions
  process/process.go  — sing-box child process: Start/Stop/watch (crash detection)
  config/config.go    — tray-config.json load/save; FindInboundAddr parses sing-box config
  tun/tun.go          — InjectTUN: builds temp config with TUN inbound injected
  proxy/proxy.go      — Windows system proxy via registry + WinInet flush
  elevation/          — IsElevated, RelaunchAsAdmin (ShellExecuteW "runas")
  autostart/          — Task Scheduler via schtasks.exe (CREATE_NO_WINDOW on all calls)
  logbuf/             — circular log buffer, file mirror with timestamps, subscriptions
  logwin/             — lxn/walk log viewer window
  settings/           — lxn/walk settings window
  watcher/            — polls os.Stat every 2s for config file changes
assets/
  icons.go            — generates grey/green/red ICO bytes at init (BMP-in-ICO format)
  defaults.go         — embeds tray-config.default.json
```

## Key decisions and constraints

**Single instance**: named kernel mutex `Global\SingBoxTray`. On UAC re-launch, the existing process must call `CloseHandle` on the mutex _before_ `os.Exit(0)` — `os.Exit` skips defers. The `releaseMutex` func is passed from `main` → `tray.NewApp`.

**Tray icons**: `getlantern/systray` on Windows calls `LoadImage(LR_LOADFROMFILE, IMAGE_ICON)`. PNG bytes are silently ignored. Icons must be ICO format (ICONDIR + ICONDIRENTRY + BITMAPINFOHEADER + BGRA pixels + AND mask). See `assets/icons.go`.

**lxn/walk windows**: require `runtime.LockOSThread()` on the goroutine that creates them, and a Windows manifest with Common Controls v6 (`rsrc.syso`). Without the manifest the window creation fails silently.

**TUN mode**: requires elevation. When TUN is selected without admin rights, `RelaunchAsAdmin` spawns an elevated instance with `--force-mode=tun`. The non-elevated instance releases the mutex immediately then exits (no sleep).

**TUN config injection** (`internal/tun/tun.go`):
- Reads the user's sing-box `config.json`, strips any existing `tun` inbound, appends a new one
- Injects `route.auto_detect_interface: true` (required — without it sing-box cannot build Windows routing table entries and TUN captures no browser traffic)
- Prepends `{ process_name: ["sing-box.exe"], outbound: "direct" }` as first route rule to break the TUN loop for sing-box's own connections
- Writes result to `os.TempDir()`; temp file is deleted on stop/crash

**Absolute paths**: Go 1.19+ refuses relative paths in `exec.Command`. All paths from `tray-config.json` are resolved to absolute at load time in `config.Load`.

**`schtasks.exe`**: all calls use `CREATE_NO_WINDOW` (`SysProcAttr{CreationFlags: 0x08000000}`) to prevent console flash on startup.

**Stop→Start race**: `pendingStart bool` in `App`. If `start()` is called while state is `StateStopping`, it sets the flag and returns; `stop()` checks the flag after completing and calls `start()`.

## tray-config.json fields

| Field | Default | Notes |
|---|---|---|
| `sing_box_path` | `sing-box.exe` | resolved to absolute on load |
| `wintun_dll_path` | `wintun.dll` | copied to sing-box dir if missing |
| `config_path` | `config.json` | base sing-box config, not modified |
| `system_proxy_inbound` | `""` | tag of the http/mixed inbound; empty = first found |
| `default_mode` | `system_proxy` | `off` / `system_proxy` / `tun` |
| `start_on_launch` | `false` | auto-start sing-box when tray starts |
| `log_lines` | `200` | circular log buffer size |
| `tun.interface_name` | `singbox-tun` | |
| `tun.address` | `["172.19.0.1/30"]` | |
| `tun.mtu` | `9000` | |
