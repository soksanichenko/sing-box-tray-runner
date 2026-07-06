# sing-box-tray — project notes for Claude

Windows-only today (see below), but a native Linux port is planned — see `docs/linux-port.md` for the agreed scope/architecture before touching anything Linux-related.

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

`scripts/build.sh`/`build.ps1` accept an optional `VERSION` env var, appended as `-ldflags -X .../internal/version.Version=$VERSION` — `release.yml` sets it from the pushed tag; unset (dev builds) leaves `version.Version` at its `"dev"` default.

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
  updater/            — GitHub release lookup + download/extract (sing-box) or fetch (launcher asset)
  selfupdate/         — swaps the running tray exe for a freshly downloaded one
  version/            — Version var, overridden via -ldflags at release build time
  i18n/               — UI string catalog (en/ru/ua), OS locale detection, live menu retitling
assets/
  icons.go            — generates grey/green/red ICO bytes at init (BMP-in-ICO format)
  defaults.go         — embeds tray-config.default.json
  locales.go          — embeds locales/*.json (loaded by internal/i18n)
scripts/
  build.sh            — build wrapper for Linux/macOS/WSL hosts
  build.ps1           — build wrapper for native Windows hosts (no `make` needed)
.github/
  workflows/ci.yml      — golangci-lint + build matrix (ubuntu-latest, windows-latest)
  workflows/release.yml — on `v*` tag push: builds and publishes a GitHub Release
  dependabot.yml         — weekly gomod + github-actions update PRs
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

**Config directory + Config submenu**: `config_dir`/`selected_config` (replacing the old single `config_path`) let a folder hold multiple sing-box configs, switchable without touching Settings. `config.ListConfigFiles` (`internal/config/config.go`) scans `config_dir` non-recursively for `*.json`, excluding `tray-config.json` itself (relevant since the default `config_dir` is `.`, the exe directory, where `tray-config.json` also lives). `OnReady` builds one **Config** submenu item per file found, each checkable and each running its own `for range item.ClickedCh` goroutine (`getlantern/systray`'s `AddSubMenuItemCheckbox`/`ClickedCh` are documented safe to use from any goroutine) — this is unlike every other submenu in the tray (Mode, Updates, Languages), which are fixed-size and folded into the single `select` in `handleClicks`; Config can't be, since the item count depends on what's in the folder at startup. Clicking a config entry calls `switchConfig`, which mirrors `switchMode`: persists the choice, restarts sing-box live if it was running, and restarts the config-file watcher (`restartConfigWatcher`, replacing the old fixed `watchConfigFiles`) so it tracks the newly active file instead of the old one. The Settings window's config picker (a `ComboBox`, see `internal/settings/settings.go`) is intentionally fed the *same* `configNames` slice `OnReady` already scanned (passed into `settings.Show`), rather than re-scanning the disk itself, so the tray submenu and the Settings dropdown can never disagree about what's available. Directory contents are only ever scanned at startup — dropping a new `*.json` file into `config_dir` while the tray is running won't show up until restart; this matches how Mode/Updates/Languages are already static once built, so no live-rescan was added.

**sing-box auto-updater** (`internal/updater/updater.go`, orchestrated by `tray.go`'s `checkSingBoxUpdate`/`installSingBoxUpdate`): `updater.FetchLatest(owner, repo, channel)` hits `GET /repos/<owner>/<repo>/releases?per_page=10` (no auth, requires a `User-Agent` header) and is shared with the launcher self-updater below. Channel selection scans that list rather than using `/releases/latest`: `alpha` takes the first non-draft entry, `stable` takes the first non-draft, non-prerelease entry — this also lets `stable` correctly skip past newer alphas. The Windows asset is matched by exact suffix `-windows-amd64.zip`, which naturally excludes the `-legacy-windows-7.zip` variant (different suffix). The zip's contents are nested one level down (e.g. `sing-box-1.13.14-windows-amd64/{sing-box.exe, libcronet.dll, LICENSE}`); extraction strips whatever that single top-level directory is named (not hardcoded) and copies everything inside it — `libcronet.dll` is a runtime dependency of `sing-box.exe`, not just packaging. Installs go into `<exeDir>/sing-box/<tag>/`, tray.go points `sing_box_path` at the new copy and prunes sibling version directories. There is no `installed_version` config field — `updater.InstalledVersion` derives the current version by checking whether `sing_box_path` matches `<managedRoot>/<tag>/sing-box.exe`, so it can't go stale if the user hand-edits the path. The startup check (`OnReady`) pushes a toast unless `cfg.Update.AutoUpdate` is set, in which case it installs (and restarts sing-box if running) silently, no prompt; the interactive "Check for Updates" menu path always confirms via a Yes/No dialog first regardless of the auto-update setting.

**Tray launcher self-updater** (`internal/selfupdate/selfupdate.go` + `internal/version/version.go`, orchestrated by `tray.go`'s `checkLauncherUpdate`/`installLauncherUpdate`): targets the repo the code actually lives in on GitHub — `soksanichenko/sing-box-tray-runner` per `git remote -v`, **not** the Go module path (`github.com/zelgray/sing-box-tray`), which is just an import-path choice. The release asset is the raw exe `release.yml` uploads (`sing_box_tray_runner.exe`, matched exactly via `updater.AssetNamed`, no zip). Current version comes from `version.Version`, a build-time `-X` override (`"dev"` for any build that didn't set it, e.g. local dev builds — always looks "outdated" against a real release, same as sing-box's fresh-install case). Mechanics, in order: download the new exe to `<exeDir>/sing_box_tray_runner.exe.new` — **same directory as the running exe, not `os.TempDir()`**, because `os.Rename`/`MoveFileW` on Windows can't cross drives; stop sing-box first if it's running (a child process isn't tied to the parent's lifetime here, so it would otherwise be silently orphaned once this process exits); `selfupdate.Apply` renames the running exe to `<exe>.old` (Windows permits renaming a mapped/executing image, just not deleting it) and moves the `.new` file into its place; spawn the new exe, then `releaseMutex()` + `os.Exit(0)` — the same spawn-before-release sequence the existing UAC elevation relaunch in `start()` already uses, reused rather than inventing a different handoff. `main.go` calls `selfupdate.CleanupOld(exePath)` once at startup to remove a leftover `.old` from a previous update. The startup check only auto-installs when `cfg.LauncherUpdate.AutoUpdate` is set; otherwise toast-only. There's no pre-release channel for the launcher (unlike sing-box) — always the latest stable release.

**First-run dependency check** (`tray.go`'s `checkFirstRunDeps`, called synchronously in `OnReady` before `start_on_launch` is honored): on every startup, `os.Stat`s `cfg.SingBoxPath` and `cfg.WintunDllPath`; either being missing triggers a Yes/No dialog offering to download it — this only ever fires in practice on a fresh install (or if the user deleted the file), since both checks are no-ops once the files exist. Missing sing-box.exe reuses the existing updater path (`updater.FetchLatest` + `installSingBoxUpdate(rel, true)`) rather than duplicating the download/extract logic. wintun.dll has no GitHub-releases equivalent, so `updater.DownloadWintunDll` hits a **hardcoded** `wintun.net` zip URL pinned to a specific version (`wintunDownloadURL`/`wintunZipDllEntry` in `internal/updater/updater.go`) — wintun.net has no "latest" alias, but the DLL itself hasn't changed since 2021, so pinning is low-maintenance; bump the version string by hand if wintun.net ever ships a new one. The zip's internal layout is `wintun/bin/<arch>/wintun.dll` (verified by inspecting the archive); only the amd64 entry is extracted, matching this project's Windows/amd64-only scope.

**Linting** (`.golangci.yml`): CI runs `golangci-lint` with `GOOS=windows` (the whole codebase is behind `//go:build windows`, so without it nothing gets analyzed). `errcheck` excludes ignoring the error from `Close()`/`Call()` on file handles, registry keys, `*walk.FormBase`, and Win32 `LazyProc` calls — established convention throughout this codebase, not something to "fix" file-by-file. `staticcheck`'s `ST1001` (no dot-imports) is disabled because `. "github.com/lxn/walk/declarative"` is that library's intended usage; `ST1000` (package doc comments) is disabled because it isn't this project's convention.

**Localization** (`internal/i18n/i18n.go`): `Strings` covers every UI-facing string (tray menu, tooltips, toast/dialog text, Settings/Log window chrome), embedded per-language as `assets/locales/{en,ru,ua}.json` and loaded at `init()`. `tray-config.json`'s `language` field is resolved at `main.go` startup (`i18n.Resolve`, falling back to `i18n.Detect()` which reads `GetUserDefaultUILanguage` via kernel32) and passed into `tray.NewApp`. The tray **Languages** submenu and the Settings language dropdown both switch it live (no restart) via `App.applyLanguage`, which recomputes `a.strs` and calls `refreshMenuTexts()` — this relies on `getlantern/systray`'s `(*MenuItem).SetTitle`/`SetTooltip` (confirmed present in the vendored source), so every menu item whose text is translated must be stored on `App.items` even if never clicked (e.g. the "Mode"/"Updates" submenu parents), specifically so it can be retitled later. Proper nouns (`sing-box-tray`, `sing-box`) and the language names/picker label itself are literal constants, never routed through `Strings` — a language picker translated into a language the user doesn't want is unfindable. Per the user's global logging convention, `a.log(...)` calls are deliberately **not** localized and stay in English always; only `i18n` covers the interactive UI.

## tray-config.json fields

| Field | Default | Notes |
|---|---|---|
| `sing_box_path` | `sing-box.exe` | resolved to absolute on load |
| `wintun_dll_path` | `wintun.dll` | copied to sing-box dir if missing |
| `config_dir` | `.` | folder scanned for `*.json` sing-box configs; resolved to absolute on load |
| `selected_config` | `config.json` | base sing-box config file name inside `config_dir`, not modified — see `TrayConfig.ActiveConfigPath` |
| `system_proxy_inbound` | `""` | tag of the http/mixed inbound; empty = first found |
| `autostart` | `false` | write-only mirror of the Task Scheduler entry, set by `toggleAutostart`; actual state on startup comes from `autostart.IsEnabled()`, not this field |
| `default_mode` | `system_proxy` | `off` / `system_proxy` / `tun` |
| `start_on_launch` | `false` | auto-start sing-box when tray starts |
| `log_lines` | `200` | circular log buffer size |
| `language` | `auto` | `auto` / `en` / `ru` / `ua` — UI language |
| `system_proxy.tag` | `mixed-in` | tag for the auto-generated default mixed inbound |
| `system_proxy.listen` | `127.0.0.1` | |
| `system_proxy.listen_port` | `2080` | |
| `update.channel` | `stable` | `stable` / `alpha` — sing-box release channel for the updater |
| `update.auto_update` | `false` | silently install (and restart sing-box if running) instead of prompting |
| `launcher_update.auto_update` | `false` | silently self-update (and relaunch) instead of prompting |
| `tun.interface_name` | `singbox-tun` | |
| `tun.address` | `["172.19.0.1/30"]` | |
| `tun.mtu` | `9000` | |
