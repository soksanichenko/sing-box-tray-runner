# sing-box-tray

A minimal Windows system tray launcher for [sing-box](https://sing-box.sagernet.org/).

## Features

- **Three proxy modes** switchable from the tray menu
  - **Off** — sing-box runs without touching system settings
  - **System Proxy** — sets Windows HTTP proxy from the sing-box config inbound
  - **TUN** — injects a TUN inbound into a temp config, requires elevation
- Tray icon reflects state: grey = stopped, green = running, red = crashed
- Settings window to configure paths (sing-box, wintun.dll, config.json)
- Log viewer with live updates
- Crash detection with desktop notification
- File watcher — prompts to restart when config files change
- Autostart via Task Scheduler (`/RL HIGHEST` for TUN mode)
- Single instance enforced via named kernel mutex

## Requirements

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
  "tun": {
    "interface_name": "singbox-tun",
    "address": ["172.19.0.1/30"],
    "mtu": 9000
  }
}
```

| Field | Description |
|---|---|
| `sing_box_path` | Path to `sing-box.exe`. Relative paths are resolved from the tray exe directory. |
| `wintun_dll_path` | Path to `wintun.dll`. Copied next to `sing-box.exe` on TUN start if not already present. |
| `config_path` | Path to the sing-box `config.json`. This file is never modified. |
| `system_proxy_inbound` | Tag of the `http` or `mixed` inbound to read the proxy address from. Leave empty to use the first one found. |
| `default_mode` | Starting mode: `off`, `system_proxy`, or `tun`. |
| `start_on_launch` | If `true`, sing-box starts automatically when the tray app launches. |
| `log_lines` | Size of the in-memory log buffer shown in the log viewer. |
| `tun.*` | TUN interface settings injected at runtime. The base `config.json` does not need a TUN section. |

### System Proxy mode

The tray reads `listen` and `listen_port` from the sing-box config's `http` or `mixed` inbound and sets the Windows system proxy in the registry. `system_proxy_inbound` is the inbound tag to use if there are multiple.

### TUN mode

The tray injects a `tun` inbound and a `route.auto_detect_interface: true` setting into a temporary copy of the config, then starts sing-box with that copy. The original config is untouched. Elevation is requested automatically via UAC.

The tray also prepends a route rule that sends sing-box's own process traffic via `direct`, preventing it from being re-captured by the TUN interface.

## Building

Requires Go 1.19+. Cross-compile from Linux or build on Windows:

```sh
make build
```

The output is `build/sing_box_tray_runner.exe`.

The `rsrc.syso` file in the repo root embeds a Windows manifest (Common Controls v6). It is linked automatically by the Go toolchain and enables proper visual styling for the Settings and Log windows. Regenerate it if the manifest changes:

```sh
go install github.com/akavel/rsrc@latest
rsrc -manifest app.manifest -o rsrc.syso
```

## License

MIT — see [LICENSE](LICENSE).
