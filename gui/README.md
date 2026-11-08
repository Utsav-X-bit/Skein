# Skein Desktop

Wails 3 control surface for a Skein mesh. The Go services talk to the daemon
over the root-owned Unix socket (`/run/skein/control.sock`) or the Windows
named pipe (`\\.\pipe\skein-control`); the frontend never talks to the daemon
directly. Privileged setup, service changes, secret rotation, reset, and signed
updates go through the fixed-verb `skein-helper`.

## Build

Prerequisites: Go 1.26.10+, Node.js 22 LTS with npm, the Wails 3 CLI
v3.1.0 (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`),
and GTK4 + WebKitGTK 6.0 development packages on Linux; WebView2 on
Windows. `wails3 doctor` reports missing dependencies.

```bash
cd gui
wails3 build        # -> bin/skein-desktop
```

`wails3 build` builds only the desktop. The daemon, helper, and CLI are
built from the repository root; build and install all four as described
in the root README, Installation Method 2. The helper must end up at
`/usr/libexec/skein-helper` (Linux) or
`C:\Program Files\Skein\skein-helper.exe` (Windows). Its `install` verb
needs an existing system config; on a first install the `init`/`join`
bootstrap registers the service.

## Development

```bash
wails3 dev
```

The frontend lives in `frontend/` (React + TypeScript). The bound services are
in `services/`; the Wails app/window/tray/updater hooks are in `main.go`,
`app.go`, `tray.go`, and `updater.go`.

The daemon API contract is in [`../docs/gui-api.md`](../docs/gui-api.md).
