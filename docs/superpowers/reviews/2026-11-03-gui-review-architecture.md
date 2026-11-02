# Architecture Review — Skein Desktop GUI (rev. 1)

Date: 2026-11-03
Reviewer: strict architecture/security reviewer agent
Verdict: **REJECT** (rev. 1)

## Blocking defects

- **B1 Privilege/ownership contradiction:** design ran the daemon as `User=skein`
  while config/identity were `0600 root:root`; the daemon could not read/write
  them or create the socket, and firewall commands need root.
- **B2 Connection toggle = service start/stop:** every toggle required
  pkexec/UAC, contradicting “no admin for normal operation”; no daemon
  pause/resume state; autostart reconnected after “disconnect”.
- **B3 “Revoke” was not revocation:** removing a peer only cleared local state;
  the peer could rejoin with the network secret. No rotation/denylist.
- **B4 Secret model false:** GUI necessarily handles the secret-bearing
  `skein:2:` join string; “GUI never reads the network secret” was impossible,
  and same-user malware is outside the IPC boundary.
- **B5 No bootstrap path:** no helper `init`/`join`; CLI writes user-owned
  config; no pre-service daemon mode.
- **B6 Windows IPC incomplete:** no `go-winio`, no impersonation/revert, no
  client-side server verification, no pipe-squat tests; cross-build treated as
  evidence.
- **B7 Packaging unsafe:** AppImage cannot install a root-owned helper/service;
  MSIX cannot install a service/Wintun driver as specified.
- **B8 Migration incomplete:** identity, unit/binPath, CLI client, existing
  service, Windows DACL (current `fileperm.Restrict` grants the user).
- **B9 Updates cannot update system installs/daemon:** Wails stages beside the
  binary; no helper update verb/protocol version/rollback.
- **B10 SSE single-consumer:** events split between clients; no IDs/replay.
- **B11 Firewall infeasible as `skein`; repair destructive:** remove-before-add,
  no rollback/status, ufw forward gaps, PowerShell interpolation.
- **B12 API contract incomplete:** paths only; no schemas/errors/version/audit/
  restart matrix/merge semantics; `ConfigView` missing fields; UDS not
  representable.
- **B13 Fabricated controls:** network name, log level, rotate secret, config
  folder, DNS check, mesh ACL, health, device-invite semantics, overlay ping.
- **B14 Contrast tokens fail WCAG AA.**
- **B15 TCP fatal bind is a port-squat DoS; UDS config missing.**

## Required changes

See review body for the 16 required changes. The revised design/plan must
demonstrate: one consistent ownership model; unprivileged connection toggle;
honest revocation/secret/trust language; pre-service bootstrap; buildable/
testable Windows IPC; safe packaging/update; complete API contract; no fake
controls; AA contrast; hard gates for Windows runtime, migration, firewall
rollback, SSE fan-out, existing-service upgrade.
