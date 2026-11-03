# Skein Desktop

Wails 3 control surface for a Skein mesh. The Go services talk to the daemon
over the root-owned Unix socket (`/run/skein/control.sock`) or the Windows
named pipe (`\\.\pipe\skein-control`); the frontend never talks to the daemon
directly. Privileged setup, service changes, secret rotation, reset, and signed
updates go through the fixed-verb `skein-helper`.

## Build

```bash
cd gui
wails3 build        # -> bin/skein-desktop
```

Prerequisites: Go, the Wails 3 CLI, and GTK4 + WebKitGTK 6.0 development
packages on Linux; WebView2 on Windows. `wails3 doctor` reports missing
dependencies.

Install the helper root-owned before using onboarding:

```bash
sudo install -m 0755 ../skein-helper /usr/libexec/skein-helper   # Linux
```

Windows expects `C:\Program Files\Skein\skein-helper.exe`.

## Development

```bash
wails3 dev
```

The frontend lives in `frontend/` (React + TypeScript). The bound services are
in `services/`; the Wails app/window/tray/updater hooks are in `main.go`,
`app.go`, `tray.go`, and `updater.go`.

The daemon API contract is in [`../docs/gui-api.md`](../docs/gui-api.md).
