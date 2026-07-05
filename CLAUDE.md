# sing-box-tray — project notes for Claude

## Build

Windows-only project, cross-compiled from Linux:

```sh
make build
# or, without `make`:
./scripts/build.sh    # Linux/macOS/WSL
scripts\build.ps1     # native Windows (PowerShell)
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
  updater/            — GitHub release lookup + download/extract of sing-box.exe
  i18n/               — UI string catalog (en/ru/ua), OS locale detection
assets/
  icons.go            — generates grey/green/red ICO bytes at init (BMP-in-ICO format)
  defaults.go         — embeds tray-config.default.json
  locales.go          — embeds locales/*.json (loaded by internal/i18n)
scripts/
  build.sh            — build wrapper for Linux/macOS/WSL hosts
  build.ps1           — build wrapper for native Windows hosts (no `make` needed)
```

## Key decisions and constraints

**Single instance**: named kernel mutex `Global\SingBoxTray`. On UAC re-launch, the existing process must call `CloseHandle` on the mutex _before_ `os.Exit(0)` — `os.Exit` skips defers. The `releaseMutex` func is passed from `main` → `tray.NewApp`.

**Tray icons**: `getlantern/systray` on Windows calls `LoadImage(LR_LOADFROMFILE, IMAGE_ICON)`. PNG bytes are silently ignored. Icons must be ICO format (ICONDIR + ICONDIRENTRY + BITMAPINFOHEADER + BGRA pixels + AND mask). See `assets/icons.go`.

**lxn/walk windows**: require `runtime.LockOSThread()` on the goroutine that creates them, and a Windows manifest with Common Controls v6 (`rsrc.syso`). Without the manifest the window creation fails silently.

**TUN mode**: requires elevation. When TUN is selected without admin rights, `RelaunchAsAdmin` spawns an elevated instance with `--force-mode=tun`. The non-elevated instance releases the mutex immediately then exits (no sleep).

**Per-mode inbound injection**: `prepareConfig` (`internal/tray/tray.go`) filters the user's sing-box `config.json` down to only the inbound type relevant to the selected mode, so a config that defines both a proxy and a TUN inbound doesn't run both at once. Both paths use the shared helpers in `internal/config/config.go` (`LoadRawSingBoxConfig`, `FilterInbounds`, `WriteRawSingBoxConfig`) to rewrite a temp file — the original `config.json` is never modified. If a matching inbound already exists it's kept as-is; only if none is found is a default one built and appended.
- **TUN** (`internal/tun/tun.go`, `InjectTUN`): keeps only `tun`-type inbounds, appending a default one from `tray-config.json`'s `tun.*` fields if none exists. Also injects `route.auto_detect_interface: true` (required — without it sing-box cannot build Windows routing table entries and TUN captures no browser traffic) and prepends `{ process_name: ["sing-box.exe"], outbound: "direct" }` as first route rule to break the TUN loop for sing-box's own connections.
- **System proxy** (`config.InjectSystemProxy`): keeps only `http`/`mixed`-type inbounds, appending a default `mixed` inbound from `tray-config.json`'s `system_proxy.*` fields if none exists.
- Both write the result to `os.TempDir()`; the temp file is deleted on stop/crash.

**Absolute paths**: Go 1.19+ refuses relative paths in `exec.Command`. All paths from `tray-config.json` are resolved to absolute at load time in `config.Load`.

**`schtasks.exe`**: all calls use `CREATE_NO_WINDOW` (`SysProcAttr{CreationFlags: 0x08000000}`) to prevent console flash on startup.

**Stop→Start race**: `pendingStart bool` in `App`. If `start()` is called while state is `StateStopping`, it sets the flag and returns; `stop()` checks the flag after completing and calls `start()`.

**sing-box auto-updater** (`internal/updater/updater.go`): fetches `GET /repos/SagerNet/sing-box/releases?per_page=10` (no auth, requires a `User-Agent` header). Channel selection scans that list rather than using `/releases/latest`: `alpha` takes the first non-draft entry, `stable` takes the first non-draft, non-prerelease entry — this also lets `stable` correctly skip past newer alphas. The Windows asset is matched by exact suffix `-windows-amd64.zip`, which naturally excludes the `-legacy-windows-7.zip` variant (different suffix). The zip's contents are nested one level down (e.g. `sing-box-1.13.14-windows-amd64/{sing-box.exe, libcronet.dll, LICENSE}`); extraction strips whatever that single top-level directory is named (not hardcoded) and copies everything inside it — `libcronet.dll` is a runtime dependency of `sing-box.exe`, not just packaging. Installs go into `<exeDir>/sing-box/<tag>/`, tray.go points `sing_box_path` at the new copy and prunes sibling version directories. There is no `installed_version` config field — `updater.InstalledVersion` derives the current version by checking whether `sing_box_path` matches `<managedRoot>/<tag>/sing-box.exe`, so it can't go stale if the user hand-edits the path. The startup check (`OnReady`) only ever pushes a toast notification; it never auto-installs — only the interactive "Check for Updates" menu path (`checkForUpdate(true)`) prompts and downloads.

**Localization** (`internal/i18n/i18n.go`): `Strings` covers every UI-facing string (tray menu, tooltips, toast/dialog text, Settings/Log window chrome), embedded per-language as `assets/locales/{en,ru,ua}.json` and loaded at `init()`. `tray-config.json`'s `language` field is resolved once in `main.go` (`i18n.Resolve`, falling back to `i18n.Detect()` which reads `GetUserDefaultUILanguage` via kernel32) and passed into `tray.NewApp` — the `App.strs` field is otherwise never recomputed. Per the user's global logging convention, `a.log(...)` calls are deliberately **not** localized and stay in English always; only `i18n` covers the interactive UI.

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
| `language` | `auto` | `auto` / `en` / `ru` / `ua` — UI language |
| `system_proxy.tag` | `mixed-in` | tag for the auto-generated default mixed inbound |
| `system_proxy.listen` | `127.0.0.1` | |
| `system_proxy.listen_port` | `2080` | |
| `update.channel` | `stable` | `stable` / `alpha` — sing-box release channel for the updater |
| `tun.interface_name` | `singbox-tun` | |
| `tun.address` | `["172.19.0.1/30"]` | |
| `tun.mtu` | `9000` | |
