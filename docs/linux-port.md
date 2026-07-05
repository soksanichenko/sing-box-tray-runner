# Linux port — spec

Status: **draft, not yet implemented**. This documents scope and architecture decisions agreed on before writing any Linux code, so implementation can proceed without re-litigating them. Everything below "Stage v2" is Stage v1 — the initial port; Stage v2 is deferred follow-up work, not blocking v1.

## Scope decisions (confirmed)

| Question | Decision |
|---|---|
| GUI parity | Full: tray menu + Settings window + Log window, same feature set as Windows |
| Settings/Log window toolkit | GTK via `gotk3` |
| System Proxy mode | GNOME only (`gsettings`) — other desktops get Off/TUN, System Proxy is disabled/hidden |
| TUN privilege escalation | `pkexec` per launch (polkit), mirroring the existing Windows UAC-per-action model |

## Tray icon support by desktop environment

**KDE Plasma: no extra requirement.** Plasma's system tray has been a native StatusNotifierItem (SNI) host since KDE 4 — `libappindicator`/`libayatana-appindicator` (what `getlantern/systray` links against on Linux) talk to the desktop over that same SNI D-Bus protocol, so as soon as the runtime library is present (already a hard build/runtime dependency of this app on every desktop), the icon just shows up. No package, no extension, no user action.

**GNOME: needs the AppIndicator extension, and it doesn't auto-enable itself.** Since GNOME Shell 3.26 removed the legacy XEmbed tray, `libappindicator`/`ayatana-appindicator` icons only appear with the "AppIndicator and KStatusNotifierItem Support" extension (`appindicatorsupport@rgcjonas.gmail.com`, packaged as `gnome-shell-extension-appindicator` on both Debian and Fedora — confirmed via `packages.debian.org`/Fedora package search). Two important caveats even once it's installed:
- **Ubuntu ships and enables an equivalent extension by default** (Ubuntu's stock GNOME session bundles its own "Ubuntu AppIndicators" extension pre-enabled), so Ubuntu users need nothing extra. This still wants confirming on an actual Ubuntu box before relying on it in the packaging/docs.
- **Vanilla GNOME (Fedora Workstation, Debian's plain GNOME session, Arch+GNOME, etc.) does not** — a package dependency gets it *installed*, but GNOME Shell extensions still need enabling (`gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com`, or via the Extensions app), and on a Wayland session that only takes effect after a full logout/login (extensions can't be hot-reloaded on Wayland the way `Alt+F2 r` used to work under X11). A package post-install script can't reliably do this for the user (it runs outside their logged-in session), so the honest answer is: depending on the package reduces friction but a first-run notice or README callout ("if the tray icon doesn't appear, enable the AppIndicator extension and log back in") is still needed.

## Verified facts (checked live, not assumed)

- `getlantern/systray` already supports Linux (`systray_linux.go`, `systray_linux_appindicator.go`, `systray_linux_ayatana.go` exist in the module) but **requires `CGO_ENABLED=1`** on Linux — unlike the current Windows build, which is `CGO_ENABLED=0`. This means the clean "just Go, no C compiler" build story from the README's *Build requirements* section holds for Windows only; the Linux build needs a C toolchain plus GTK/AppIndicator dev headers.
- sing-box's Linux amd64 release asset is `sing-box-<version>-linux-amd64.tar.gz` (glibc/musl variants also exist, e.g. `-linux-amd64-glibc.tar.gz` — matching on the exact suffix `-linux-amd64.tar.gz` excludes those, same pattern already used for the Windows `-windows-amd64.zip` match). Verified via `gh api repos/SagerNet/sing-box/releases/tags/v1.13.14`.
- The tarball has the same nested-directory shape as the Windows zip: `sing-box-<version>-linux-amd64/{sing-box, libcronet.so, LICENSE}` — no `.exe`, and `libcronet.so` instead of `libcronet.dll`, but otherwise the existing "strip the one top-level dir, copy everything inside it" extraction logic in `internal/updater` applies unchanged, just swapping `archive/zip` for `archive/tar`+`compress/gzip` (both stdlib, no new dependency).

## Per-subsystem approach

Go's `_windows.go` / `_linux.go` filename suffix convention replaces the current blanket `//go:build windows` header on every file — the compiler picks the right file automatically, so `internal/tray` (the orchestrator) doesn't need any `runtime.GOOS` branching as long as each platform variant exposes the same function signatures.

| Package | Windows (today) | Linux (planned) |
|---|---|---|
| `config`, `state`, `logbuf`, `i18n` (catalog/`Strings`), `updater` (HTTP/channel logic), `version` | Already OS-agnostic logic (some just carry the tag out of habit) | No change, or just drop the tag |
| `updater` (archive extraction) | `archive/zip` | Add a `archive/tar`+`gzip` path for `.tar.gz` assets |
| `i18n.Detect()` | `GetUserDefaultUILanguage` (kernel32) | Parse `$LANG`/`$LC_ALL` (e.g. `ru_RU.UTF-8` → `ru`) |
| `proxy` | Registry + WinInet | `gsettings set org.gnome.system.proxy ...`; no-op/error (System Proxy hidden) if `$XDG_CURRENT_DESKTOP` isn't GNOME |
| `autostart` | `schtasks.exe`, `/RL HIGHEST` for TUN | XDG autostart entry at `~/.config/autostart/sing-box-tray.desktop`; no elevation needed for autostart itself (TUN elevation happens per-launch regardless) |
| `elevation` | `ShellExecuteW "runas"`, token-elevation check | `pkexec <exe> --force-mode=tun` re-exec; `IsElevated()` → `os.Geteuid() == 0` |
| `process` (stop signal) | `CTRL_BREAK_EVENT` via `GenerateConsoleCtrlEvent` | `SIGTERM` via `cmd.Process.Signal` — sing-box already handles this the same way `sing-box run` does under systemd |
| `tun` | `EnsureWintunDll` copies `wintun.dll` | No-op — Linux TUN needs no separate driver, sing-box opens `/dev/net/tun` directly |
| `selfupdate` | Rename-while-running trick | **No change needed** — `os.Rename` over a running executable is even more permissive on POSIX (unlink/rename of an open/mapped file is always allowed); the existing `Apply`/`CleanupOld` code is already portable as-is |
| `settings`, `logwin` | `lxn/walk` declarative | Rewritten in `gotk3` (imperative API, not declarative — this is the single largest chunk of new code) — same `Show(cfg, strs, onSave)` / `Show(buf, maxLines, strs)` signatures so `tray.go` doesn't change |
| `tray` (menu/orchestration, i18n wiring, updater orchestration) | — | Mostly reusable as-is; only the elevation call and any Windows-specific spawn flags (`CREATE_NO_WINDOW`) need a Linux-side equivalent (or omission — Linux doesn't have a console-window-flash problem the same way) |

## Config/log file locations

Windows keeps everything portable, next to the exe. Linux will follow the XDG Base Directory spec instead (standard convention, not a portable-app style):
- Config: `~/.config/sing-box-tray/tray-config.json`
- Logs: `~/.local/state/sing-box-tray/sing-box-tray.log`

## Build requirements (Linux target)

- Go (same `go.mod` minimum as today)
- `CGO_ENABLED=1` (mandatory — both `gotk3` and the Linux `systray` backend need it, unlike the Windows build)
- `pkg-config`
- GTK3 + AppIndicator dev headers — on Ubuntu/Debian (the CI target): `libgtk-3-dev`, `libayatana-appindicator3-dev` (see the dependency table above for the Fedora equivalent, still unconfirmed)

## CI/release impact

- `ci.yml` needs a new Linux build job that `apt-get install`s the packages above before `go build` — the current job only cross-compiles the Windows exe from `ubuntu-latest`, it doesn't natively build a Linux binary yet.
- `release.yml` needs a second build+upload step producing a Linux binary asset, so both the sing-box updater (already Linux-aware once the tar.gz path lands) and — down the line — a Linux self-updater for the tray launcher itself have something to fetch.

## Explicitly out of scope for v1

Ship a plain tarball (binary + `.desktop` file + a short install note) first. Packaging, other desktops' System Proxy support, and additional architectures are all deferred — see **Stage v2** below.

## Stage v2

Deferred follow-up work, not blocking the v1 port above.

### `.deb`/`.rpm` packaging

| Distro family | Runtime lib | Build (-dev) lib | GNOME extension package |
|---|---|---|---|
| Debian/Ubuntu (.deb) | `libayatana-appindicator3-1` | `libayatana-appindicator3-dev` | `gnome-shell-extension-appindicator` — confirmed present in bookworm, trixie, sid |
| Fedora (.rpm) | *unconfirmed* — `libappindicator` package exists but its exact gtk3/-devel subpackage split wasn't resolvable from package search; needs checking on an actual Fedora box (`dnf provides '*/libappindicator3.so*'`) before writing the spec file | same caveat | `gnome-shell-extension-appindicator` — confirmed present in Fedora 43/44/Rawhide, EPEL 9 |

Declare the GNOME extension package as a `Recommends:` (Debian)/weak dependency, not a hard `Depends:` — it's irrelevant on KDE/Ubuntu, and per the caveats in "Tray icon support by desktop environment" above it doesn't fully solve the problem on its own anyway (still needs enabling, and a Wayland logout/login). A first-run notice or README callout ("if the tray icon doesn't appear, enable the AppIndicator extension and log back in") is worth keeping regardless of packaging.

Also worth confirming on a real Ubuntu box before writing the packaging scripts: whether Ubuntu's default GNOME session already covers this via its own pre-enabled extension, making the dependency a no-op there (see "Tray icon support by desktop environment").

AppImage is a third option worth considering alongside `.deb`/`.rpm` — sidesteps distro-specific package names entirely, at the cost of bundling GTK/AppIndicator into the image.

### Other desktop environments' System Proxy support

KDE (`kwriteconfig5`/`kioslaverc`) and others (XFCE, etc.) are Off/TUN-only in v1. Adding System Proxy there means a second `proxy` backend per desktop, detected via `$XDG_CURRENT_DESKTOP` — same shape as the GNOME one, just more of them, and more combinations to test.

### Additional architectures

v1 targets `amd64` only (matching the Windows build's `GOARCH=amd64`). `arm64` is the obvious next target given how common it is for Linux desktops/SBCs; sing-box already publishes `linux-arm64` release assets so the updater side needs no new work, just a second CI build leg.
