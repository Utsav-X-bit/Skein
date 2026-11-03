# Windows tests

Run these from an **elevated PowerShell** on a disposable Windows VM or a
machine where stopping the Skein service is acceptable. The scripts do not
change the service configuration, but the daemon smoke test opens temporary
firewall rules and creates temporary TUN adapters.

## Build and run everything

```powershell
.\test\windows\build.ps1
.\test\windows\run-all.ps1 -Bin .\dist
```

`build.ps1` builds `skein.exe`, `skeind.exe`, `skein-helper.exe`, the Wails
desktop app, and fetches `wintun.dll`. Use `-SkipDesktop` or `-SkipWintun` when
those artifacts already exist.

`run-all.ps1` runs the non-destructive suite:

- `smoke.ps1` — two daemons on loopback, direct link, overlay ping, cleanup.
- `cli.ps1` — `skein version`, help, `status`, `peers`, `routes`, static TUI.
- `pipe.ps1` — raw HTTP over `\\.\pipe\skein-control`.
- `helper.ps1` — helper status/help and no-verb exit code.
- `service.ps1` — SCM state and binary path.
- `firewall.ps1` — owned Windows Firewall rules.
- `gui.ps1` — desktop window, single-instance launch, close-without-tray.

Use `-SkipSmoke` or `-SkipGui` when the corresponding binaries are not
available.

## Individual checks

```powershell
.\test\windows\smoke.ps1 -Bin .\dist
.\test\windows\cli.ps1 -Bin .\dist
.\test\windows\pipe.ps1 -Bin .\dist
.\test\windows\helper.ps1 -Bin .\dist
.\test\windows\service.ps1 -Bin .\dist
.\test\windows\firewall.ps1 -Bin .\dist
.\test\windows\firewall.ps1 -Bin .\dist -Repair
.\test\windows\gui.ps1 -Bin .\dist
```

`firewall.ps1 -Repair` runs `skein setup` against
`%ProgramData%\Skein\skein.toml` before inspecting the rules.

## What is not automated

Onboarding, init/join, secret rotation, and service install/uninstall mutate
`%ProgramData%\Skein` and the `skein` service. Test those manually in a
disposable VM, or use the desktop app and keep a copy of the existing config.
