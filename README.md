# sing-box-tray

A minimal Windows system tray launcher for [sing-box](https://sing-box.sagernet.org/).

## Features

- **Three proxy modes** switchable from the tray menu
  - **Off** — sing-box runs without touching system settings
  - **System Proxy** — sets Windows HTTP proxy from the sing-box config inbound
  - **TUN** — injects a TUN inbound into a temp config, requires elevation
- Tray icon reflects state: grey = stopped, green = running, red = crashed
- Settings window to configure paths (sing-box, wintun.dll, config.json) and the update channel
- Log viewer with live updates
- Crash detection with desktop notification
- File watcher — prompts to restart when config files change
- Autostart via Task Scheduler (`/RL HIGHEST` for TUN mode)
- Single instance enforced via named kernel mutex
- Auto-updates the `sing-box` binary itself from GitHub Releases (stable or alpha channel)
- UI in English, Russian, or Ukrainian — auto-detected from the Windows locale

## Build requirements

Only the Go toolchain is needed, on either host platform — no C compiler, no Windows SDK. Nothing in this project uses cgo (`CGO_ENABLED=0` in the Makefile/scripts is mandatory, not just a preference: all Windows API calls go through `syscall`/`golang.org/x/sys/windows`, never cgo), so there's nothing to cross-compile besides Go itself.

- **Go** — matching or newer than the version pinned in `go.mod` (currently `1.25.9`). Since Go 1.21, the `go` directive in `go.mod` is an enforced minimum: an older toolchain on `PATH` will auto-fetch the pinned one via `GOTOOLCHAIN=auto` if network access is available.
- **On Linux/macOS/WSL**: `./scripts/build.sh` cross-compiles to `build/sing_box_tray_runner.exe`.
- **On native Windows**: `scripts\build.ps1` (PowerShell) builds the same output, no `make` required.
- **`make`** is optional — only needed if you use `make build` instead of the scripts above.
- Regenerating `rsrc.syso` (only if `app.manifest` changes) needs `github.com/akavel/rsrc` — a pure-Go tool, installable and runnable on Linux too (see [Building](#building)).

## Runtime requirements

- Windows 10/11 x64
- [sing-box](https://github.com/SagerNet/sing-box) binary
- [wintun.dll](https://www.wintun.net/) — required only for TUN mode

## Installation

1. Place `sing_box_tray_runner.exe` anywhere on disk.
2. On first launch, `tray-config.json` is created next to the executable with default values.
3. Right-click the tray icon → **Settings...** and set the paths.

## Configuration

`tray-config.json` (created automatically on first run):

```json
{
  "sing_box_path": "sing-box.exe",
  "wintun_dll_path": "wintun.dll",
  "config_path": "config.json",
  "system_proxy_inbound": "",
  "default_mode": "system_proxy",
  "start_on_launch": false,
  "log_lines": 200,
  "language": "auto",
  "system_proxy": {
    "tag": "mixed-in",
    "listen": "127.0.0.1",
    "listen_port": 2080
  },
  "update": {
    "channel": "stable"
  },
  "tun": {
    "interface_name": "singbox-tun",
    "address": ["172.19.0.1/30"],
    "mtu": 9000
  }
}
```

| Field | Description |
|---|---|
| `sing_box_path` | Path to `sing-box.exe`. Relative paths are resolved from the tray exe directory. Rewritten automatically after an auto-update. |
| `wintun_dll_path` | Path to `wintun.dll`. Copied next to `sing-box.exe` on TUN start if not already present. |
| `config_path` | Path to the sing-box `config.json`. This file is never modified. |
| `system_proxy_inbound` | Tag of the `http` or `mixed` inbound to read the proxy address from. Leave empty to use the first one found. |
| `default_mode` | Starting mode: `off`, `system_proxy`, or `tun`. |
| `start_on_launch` | If `true`, sing-box starts automatically when the tray app launches. |
| `log_lines` | Size of the in-memory log buffer shown in the log viewer. |
| `language` | UI language: `auto` (detect from Windows), `en`, `ru`, or `ua`. |
| `system_proxy.*` | Default `mixed` inbound injected when running in System Proxy mode and the base `config.json` has none. |
| `update.channel` | `stable` or `alpha` — which sing-box releases the updater considers. |
| `tun.*` | TUN interface settings injected at runtime. The base `config.json` does not need a TUN section. |

### System Proxy mode

The tray reads `listen` and `listen_port` from the sing-box config's `http` or `mixed` inbound and sets the Windows system proxy in the registry. `system_proxy_inbound` is the inbound tag to use if there are multiple.

### TUN mode

The tray injects a `tun` inbound and a `route.auto_detect_interface: true` setting into a temporary copy of the config, then starts sing-box with that copy. The original config is untouched. Elevation is requested automatically via UAC.

The tray also prepends a route rule that sends sing-box's own process traffic via `direct`, preventing it from being re-captured by the TUN interface.

### Auto-update

The tray checks GitHub for a new sing-box release on startup (a toast notification appears if one is available) and via **Check for Updates** in the tray menu at any time. Updates are downloaded into a tray-managed `sing-box/<version>/` folder next to the executable; `sing_box_path` is switched to point at the new version automatically, and older versions are removed. `update.channel` (or the checkbox in Settings) picks between `stable` releases and `alpha` pre-releases.

### Localization

The UI (tray menu, dialogs, notifications, Settings/Log windows) is available in English, Russian, and Ukrainian. `language: "auto"` detects the language from the Windows UI locale; set it to `en`, `ru`, or `ua` in `tray-config.json` to override. Log output stays in English regardless of UI language.

## Building

```sh
make build          # any host with `make`
./scripts/build.sh   # Linux/macOS/WSL, no `make` required
scripts\build.ps1    # native Windows (PowerShell), no `make` required
```

The output is `build/sing_box_tray_runner.exe`.

The `rsrc.syso` file in the repo root embeds a Windows manifest (Common Controls v6). It is linked automatically by the Go toolchain and enables proper visual styling for the Settings and Log windows. Regenerate it if the manifest changes:

```sh
go install github.com/akavel/rsrc@latest
rsrc -manifest app.manifest -o rsrc.syso
```

## CI/CD

- **CI** (`.github/workflows/ci.yml`) — on every push/PR: `golangci-lint` (with `GOOS=windows`) plus a build matrix on `ubuntu-latest` and `windows-latest` to verify both `scripts/build.sh` and `scripts/build.ps1` work.
- **Release** (`.github/workflows/release.yml`) — pushing a `v*` tag (e.g. `git tag v1.0.0 && git push --tags`) builds the exe and publishes it as a GitHub Release with auto-generated notes.
- **Dependabot** (`.github/dependabot.yml`) — weekly PRs for Go module and GitHub Actions updates.

## License

MIT — see [LICENSE](LICENSE).
