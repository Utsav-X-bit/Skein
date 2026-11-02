# Skein Desktop — Design Specification (rev. 4)

Date: 2026-11-03
Status: revised after strict architecture and UX review; awaiting re-review
Research: `docs/superpowers/research/2026-11-03-skein-desktop-gui-research.md`
Review history: `docs/superpowers/reviews/2026-11-03-gui-review-*.md`

## 0. Decisions locked after review

1. **Daemon runs as root** (Linux) / **LocalSystem** (Windows). No `User=skein`.
   Config and identity are root-owned `0600`; `/etc/skein` is `0750 root:root`.
   The GUI never reads them.
2. **Control socket is the only service control plane**:
   `/run/skein/control.sock` (`0660 root:skein` + per-user ACL for the
   installing user) and `\\.\pipe\skein-control` (DACL: SYSTEM, Administrators,
   installing user SID). Caller identity is kernel-derived; no bearer token in
   the GUI path.
3. **Connection is a daemon state, not a service state.** `POST /connection`
   pauses/resumes the dataplane (TUN, link, routes) without elevation while the
   service stays running. Service install/start/stop remains a separate,
   elevated operation.
4. **Revocation is network-secret rotation**, not peer removal. Removing a peer
   is local-only and is labelled that way. Rotation is a helper flow with
   backup, stop, rotate, restart, and re-invite.
5. **Bootstrap is a helper flow**: `skein-helper init` and `join` generate
   identity/secret/config root-side and return only public metadata plus the
   join string. The GUI transiently handles join strings, never persists or
   logs them.
6. **Windows named pipes use a direct Win32 implementation (handle-level identity checks)**, server-side
   client SID verification and client-side server image verification.
7. **AppImage is GUI-only** (no service/helper install). DEB/RPM/Arch/NSIS
   install the root-owned helper, service, Wintun, and desktop integration.
   MSIX is not used for the service-installing product.
8. **Unsupported engine features are removed from the UI** rather than shown as
   disabled controls: exit nodes, subnet routes, invite expiry/roles, pending
   requests, accounts, DNS/magic DNS, mesh ACLs, arbitrary firewall rules,
   log levels, update channels, language switching.
9. **SSE is broadcast** with per-subscriber queues and `Last-Event-ID` replay.
10. **Network name is a local GUI label** stored in app data, not shared.

## 1. Product definition

Skein Desktop is the complete graphical control surface for a Skein mesh. A
person who has never opened a terminal can install it, create or join a
network, invite devices, see the mesh, diagnose problems, change settings,
manage the service, and uninstall it. The privileged networking daemon remains
separate; the GUI is an unprivileged client.

**Success test:** a non-technical user completes onboarding, connects two
devices, invites a third, sees the topology, exports a diagnostic bundle, and
uninstalls — using only the GUI, with no terminal and no manual config file.

## 2. Goals and non-goals

Goals: complete coverage of real capabilities; honest states; premium design
and motion; 60 FPS; accessible; unprivileged GUI; kernel-verified IPC; Windows
and Linux installers; tested and documented.

Non-goals: exit nodes, subnet routers, invite expiry/roles, pending requests,
accounts/SSO, per-peer ACLs, arbitrary OS firewall rules, DNS, macOS/iOS/
Android. These are documented as unsupported in About/Help, not faked in the
UI.

## 3. Personas and journeys

**Maya, non-technical home user.** Installs, creates a network, copies one join
string to a laptop, sees green "Connected", removes a lost device (with the
explanation that only rotation revokes access), and invites again.

**Arun, technical operator.** Runs a small mesh. Wants route paths, direct vs
relay, RTT, logs, probe, invite regeneration, config control, and keyboard use.

**Priya, support.** Receives a redacted diagnostic bundle with logs, peer/route
snapshots, platform info, and the last error.

Journeys: first run (Welcome → Identity → Create/Join → Network → Install →
Permissions → Connect → Done); invite; inspect peer; diagnose; settings/restart;
remove device (local) or rotate secret (revoke); uninstall.

## 4. Information architecture

```
Overview (/)
Network (/network)       topology + routes
Peers (/peers)           list + add-from-invite + inspector
Invitations (/invites)   create join/peer invite, QR, copy, rotate
Security (/security)     firewall status/repair, notices
Activity (/activity)     live + bounded history
Diagnostics (/logs)      logs, events, tests, bundle
Settings (/settings)     general, network, security, updates, advanced, about
Onboarding (first run)   modal stepper
Command palette (Ctrl/Cmd+K)
Tray (status, connect/disconnect, peers, invite, settings, quit)
```

Navigation: left rail 232 px (72 px collapsed), 56 px command bar, 320 px
inspector ≥1280 px; sheet below 1100 px. Minimum 900×600; default 1440×900.
At 200% zoom the layout reflows to a single column at ~450 CSS px.

## 5. Visual language

**Obsidian weave.** Quiet ink surfaces, warm paper light theme, one signal
accent, hairline borders, mono data labels, a living topology.

### 5.1 Tokens (AA-checked)

| Token | Dark | Light |
|---|---|---|
| `--bg` | `#08090B` | `#F6F4EE` |
| `--surface` | `#101216` | `#FFFFFF` |
| `--surface-raised` | `#161B22` | `#FBFAF6` |
| `--border` | `#5A6A80` | `#8C8474` |
| `--border-strong` | `#66788F` | `#7C7466` |
| `--text` | `#F4F6F8` | `#14171C` |
| `--text-muted` | `#A7B0BE` | `#4B5563` |
| `--text-faint` | `#8A94A3` | `#66707E` |
| `--accent` | `#4FE3C1` | `#0B6B5A` |
| `--accent-soft` | `color-mix(in oklch, #4FE3C1 12%, transparent)` | `color-mix(in oklch, #0B6B5A 10%, transparent)` |
| `--relay` | `#F2B35D` | `#8A4E0A` |
| `--danger` | `#FF7A7A` | `#B42318` |
| `--info` | `#8FB4FF` | `#1D4ED8` |
| `--success` | `#7BE3A0` | `#17663A` |
| `--warning` | `#F2C94C` | `#7A5600` |

All normal-text pairs meet ≥4.5:1; interactive borders use `--border-strong`
and meet ≥3:1 on every surface (dark 3.83–4.41, light 4.42–4.62). `--border`
is decorative only. `--text-faint` meets ≥4.5:1 on every surface (dark
5.64–6.49, light 4.56–5.02) and is still restricted to metadata. A high-contrast theme uses dark `bg #000000`, `surface #0A0A0A`, `text
#FFFFFF`, `muted #E6E6E6`, `faint #D0D0D0`, `border #FFFFFF`, `border-strong
#FFFFFF`, `accent #00FFD0`, `relay #FFD166`, `danger #FF8A8A`, `info #9CC3FF`,
`success #8CFFB0`, `warning #FFE066`; light `bg #FFFFFF`, `text #000000`,
`border #000000`, `accent #004A3E`, and 2 px borders. Contrast is
tested automatically for every token pair used by text or controls.

Radii 6/10/14/20; spacing 4–72; focus ring 2 px accent + 2 px offset; floating
shadows only. Glass (`backdrop-filter: blur(18px) saturate(1.2)`) only on
palette/dialogs/tray, with an opaque fallback via `@supports`.

Typography: Geist Variable UI; Geist Mono data; display 32/24/20; body 14/20;
small 12/16; mono label 10/14 uppercase 0.08em; tabular numerals.

### 5.2 Logo and assets

`Logo.png` is 1254×1254 RGB, white background, black woven infinity. Create a
transparent SVG recreation with 8–10 stroked strands and explicit over/under
masks at crossings; `currentColor` variants; a simplified two-strand tray glyph
legible at 16/22/32 px; app icons 16–1024; Windows `.ico`; splash. Wordmark
"SKEIN" in Geist Mono 600, 0.18em tracking.

Logo motion: strands draw from crossings outward (900 ms, 40 ms stagger);
crossing nodes light; one mint pulse travels the weave on connect; idle 6 s
opacity breathing on a pseudo-element. Reduced motion: static mark + state dot.

### 5.3 Motion

Allowed properties: `transform`, `opacity`, `stroke-dashoffset`, and
`filter`/`box-shadow` only on small elements. Durations: hover 120 ms, panel
220 ms, dialog 180 ms, page 180 ms, topology node 300 ms stagger 30 ms, edge
draw 500 ms only on initial/path change, logo 900 ms, toast 180 ms. Continuous
link flow uses CSS/WAAPI, never React state. Pause on hidden. Reduced motion
and Settings → Motion = system/reduced/full.

## 6. Component system

shadcn/ui + Radix primitives adapted to desktop density: Button, IconButton,
Toggle, Checkbox, RadioGroup, Select, Input, Textarea, Tabs, Dialog,
AlertDialog, Sheet, Popover, Tooltip, DropdownMenu, Toast, Progress, Skeleton,
Separator, ScrollArea, Command. Skein: StatusDot, ConnectionToggle, Metric,
Sparkline, PeerRow, PeerInspector, RouteRow, TopologyGraph, TopologyNode,
TopologyEdge, LogViewer, QRCode, CopyField, Stepper, EmptyState, ErrorState,
PermissionCard, ServiceCard, FirewallRuleRow, DiagnosticBundleCard, LogoMark,
Wordmark, WindowControls, CommandPalette.

States: default/hover/focus/active/disabled/loading/error; light/dark/high
contrast; reduced motion; keyboard; ARIA. Hit targets ≥44×44 CSS px where
practical (visible control may be smaller with padded hit area). DataTable with
sticky header, selection, virtualization >100 rows.

## 7. Screen specifications

### 7.1 Onboarding

Full-window stepper; left rail collapses at <1100 px; content scrolls at
900×600. Steps:

1. **Welcome** — value prop; Create or Join; existing-config detection offers
   migration/backup before anything is written.
2. **Create or Join** — create: node name, overlay prefix (default), port,
   MTU, mDNS/STUN; join: paste `skein:2:` join string (transient, reveal
   toggle, never logged).
3. **Install and Identity** — helper `init` or `join` generates identity and
   secret root-side, writes `/etc/skein`, installs/starts the service, opens
   the underlay port, and trusts the overlay interface. Then show the node ID
   and public key (public data only), copy. Elevation prompt; cancellation
   leaves no partial state.
4. **Permissions** — TUN/Wintun, firewall, socket access (ACL grant), tray
   availability; plain-language explanations. If the tray is unavailable,
   close-to-tray/start-minimized are disabled.
5. **Connect** — live daemon state; first peer; invite next device.
6. **Done** — dashboard.

No terminal instructions. Errors inline with code, remediation, copy details.

### 7.2 Overview

Hero: connection state (derived, see §8.4), network label (local), uptime,
overlay address, connect toggle, animated weave. Metrics: peers up/down,
direct/relay, throughput, RTT. 60 s sparkline. Topology preview. Recent events
(live ring; labelled live). Empty state with invite CTA. Disconnected state
explains pause and primary Connect.

### 7.3 Network

React Flow canvas: local node centered, peers radial; edges labeled
Direct/Relay; relay nodes amber; path cost shown on the path, not fabricated
per-edge RTT; direct edges show measured link RTT; relay edges show "relay" and
the route cost in the inspector. Packet pulses only on active links, paused on
hidden/reduced motion. Minimap, controls, filters (all/direct/relay/offline).
Inspector: state, overlay, endpoint, transport, RTT, jitter, bytes, path, probe,
copy. Route table: destination, next hop, cost, hops, path; row click
highlights. Accessible list alternative with the same data. Unsupported:
exit node/subnet router panel explains the engine does not support it (no
control).

### 7.4 Peers

Search/filter/sort/group. Rows: status, name/ID, overlay, state, transport,
RTT, traffic, last seen. Inspector: identity, path, candidates, endpoint,
probe, remove. **Add device from invite** (paste `skein:1:`, validate, confirm,
add). Remove is labelled "Remove from this device" and states it does not
revoke access; typed confirmation uses name or peer ID. Probe is async:
`pending` → `up + RTT` or `timeout/error`; no fake result. Empty state with
invite CTA. Rename is not offered (names are self-advertised); local node name
is editable in Settings.

### 7.5 Invitations

Tabs: **Join a device** (`skein:2:` network join, secret-bearing) and **Add a
peer** (`skein:1:` peer invite, no secret; explained as an introduction, not
membership). Generate via daemon/helper; show QR/copy; reveal secret warning;
auto-clear clipboard; never log/export; OS-level screen capture is outside the app's control.
Network join is a snapshot of used
addresses: show "regenerate after each join" and a stale warning. **Rotate
network secret** is the only revocation: typed confirmation, backup, stop,
rotate, restart, all peers must rejoin; shows the new join string. Remove peer
does not revoke. Unsupported: expiry, roles, pending approval.

### 7.6 Security

Firewall card: manager, status, managed rules, last check/error, Repair.
Repair is transactional (add-before-remove, unique rule names, rollback) and
explains impact. No arbitrary OS rule editor. Security notices: identity key
permissions, config ownership, socket ACL, update signature, firewall manager
missing. No mesh ACL in this release. "Open config folder" removed; provide
"Export redacted config" instead.

### 7.7 Activity

Live charts: throughput, RTT/jitter, direct/relay share, per-peer. Ranges
1m/5m/15m/1h from GUI-owned bounded history. Opt-in toggle (default off),
sampling interval, retention (e.g. 24 h/50 MB), clear, export CSV/JSON.
Privacy copy: aggregate, payload-free; no endpoint/IP or destination logging;
per-peer history retains peer IDs locally only when opted in. Table of links.
Charts have data-table alternatives and alt text.

### 7.8 Diagnostics

Tabs: Logs (live ring + optional journald/Event Log where available; search,
pause, follow; no level filter until structured logging exists), Events (live
ring with timestamps), Tests (peer probe, route lookup, service/socket check;
no DNS/ICMP features), Bundle (logs, redacted config, peers/routes, platform;
anonymize endpoints; save/copy; show exactly what is included). Redaction:
network secret, API token, private key, join strings, optionally endpoints.
Bundle never includes the raw config file.

### 7.9 Settings

General: theme (system/light/dark/high-contrast), motion, start at login,
start minimized/close to tray (disabled if no tray), notifications (debounced).
Network: listen port, MTU, interface, mDNS, STUN server, public address,
overlay prefixes (read-only after join), restart-required badges.
Service: status (SCM/systemd, no elevation), Start/Stop/Restart, install/
uninstall, elevation prompt, error remediation. Security: firewall repair,
rotate secret, key/config/socket notices. General
also includes the local node name (`PATCH /config node.name`) and the local
network label (app-data key `networkLabel`, default "My Skein", editable).
Updates: current version, check, auto-check, release notes; per-format update
path (see §15). No channel selector.
Advanced: config path (display only), export redacted config, reset (keep
identity / purge), logs folder (if user-readable), protocol version.
About: version, commit, licenses, links, uninstall.

### 7.10 Tray and notifications

Tray: status dot; Open, Connect/Disconnect (daemon pause/resume), Peers (first
8 + More), Invite, Settings, Quit. Left click opens/focuses. Close hides to tray
only if tray available; otherwise closes the window (service keeps running) and
shows a notice. Start-minimized disabled without tray. Notifications:
connected/disconnected, peer joined/lost (debounced 5 s), service error,
update available; mute toggle. Quit exits the GUI; it does not stop the service
unless the user chooses "Quit and stop service" (elevated). Disconnect is
separate.

## 8. Architecture

### 8.1 Processes

1. **Skein Desktop** (unprivileged): Wails v3, React, Go services.
2. **skeind** (root/LocalSystem): existing daemon + new endpoints + socket/pipe
   + log/event rings + connection pause/resume.
3. **skein-helper** (root-owned, elevated): `init`, `join`, `import`,
   `install`, `uninstall`, `start`, `stop`, `restart`, `status`,
   `rotate-secret`, `reset`, `grant-user`, `set-owner`, `update-daemon`. Fixed
   verbs, validated inputs, no free-form shell. On Windows the UAC helper has
   no inherited stdio: the GUI creates a random helper pipe
   (`\\.\pipe\skein-helper-<nonce>`) with a DACL for the installing user,
   Administrators, and SYSTEM, passes only the pipe name in argv, and the
   helper connects as a client to read the JSON request (including the join
   string) and write the JSON result. Secrets never appear in argv, logs, or
   the process command line; the pipe is closed and the buffer zeroed after
   the call.

### 8.2 IPC

Linux: `/run/skein/control.sock`, dir `0755 root:root` managed by
`RuntimeDirectory=skein`, socket `0660
root:skein` plus a user ACL for the installing/granted user (`setfacl u:<uid>:rw`).
Authorization is the socket DACL (kernel) plus `SO_PEERCRED` uid recorded for
audit; no supplementary-group check is attempted. `SO_PEERGROUPS` may be used
for an explicit allowlist where available. A same-user process is inside the
trust boundary; this is documented.

Windows: `\\.\pipe\skein-control`, DACL SYSTEM/Administrators/installing user
SID (installing/granted user; `set-owner` rewrites it),
`PIPE_REJECT_REMOTE_CLIENTS`, `PIPE_UNLIMITED_INSTANCES` (bounded by the
  server's connection cap, which must exceed max concurrent REST+SSE). Server
  verifies the client
token SID via `ImpersonateNamedPipeClient` + token user; client verifies the
server PID image path and token (SYSTEM/Administrators) to prevent pipe
squatting. Use direct Win32 handles so the pipe handle is available for identity checks.

Before binding, unlink a stale socket only after verifying it is a socket with
no live listener (or let `RuntimeDirectory` recreate it); a SIGKILL restart
must not loop on `EADDRINUSE`.

HTTP/JSON + SSE over the socket/pipe, reusing `api.Server` with
**listener-specific authentication**: UDS/pipe requests carry caller identity
from `ConnContext` and bypass bearer auth; TCP dev requests keep bearer auth.
`api.listen`/`api.token` are optional when TCP is disabled; config adds
`api.socket`/`api.pipe`. `Origin` rejection stays. TCP is dev-only, disabled by
default, non-fatal if enabled and squatted.

### 8.3 Connection lifecycle

The GUI's `ServiceService.Status()` reads service state directly from the OS
(SCM/systemd), because a stopped daemon cannot answer. `daemon.Status` exposes:
- `connection`: `connected|paused|connecting|error`.
- `paused_reason`, `last_error`, `since`.
- `peer_health`: inputs for the derived health.
The GUI derives `health` (`ok|degraded|offline|error`) from the truth table:
  `service=stopped|starting|error` → `offline` (or `error` if service error);
  `connection=paused` → `offline`; `connection=error` → `error`;
  `connected` with any peer `degraded/down` → `degraded`; otherwise `ok`.
  `connecting` is the transient interval of a pause/resume call and is not a
  health value.

Service state is read **without elevation**: Linux via systemd/D-Bus (or
`systemctl is-active`), Windows via SCM `OpenSCManager`/`QueryServiceStatus`.
Only mutations (install/uninstall/start/stop/restart/reset/rotate/update) use
the elevated helper. `POST /api/v1/connection` `{action:"connect"|"disconnect"}`
calls
`daemon.Pause()`/`Resume()`:
- Pause: cancel engine ctx, close TUN/link, remove interface/routes (keep
  firewall rules), drop sessions, persist `paused=true` in
  `/var/lib/skein/state.json` (Windows `%ProgramData%\Skein\state.json`), not
  in the ephemeral runtime directory.
- Resume: reopen TUN/link, reconfigure netcfg, start control/dataplane,
  reconnect peers.
- Service restart honors `paused` (stays paused) until Connect.
Service install/start/stop is separate and elevated; the GUI labels it
"Service" not "Connect".

### 8.4 Wails services and events

`AppService`, `ConnectionService` (pause/resume/status), `PeerService`,
`InviteService`, `NetworkService`, `ConfigService`, `FirewallService`,
`DiagnosticsService`, `ServiceService`, `UpdateService`, `HistoryService`.
Events: `connection:changed`, `peers:changed`, `routes:changed`,
`activity:sample`, `log:entry`, `service:changed`, `update:available`,
`peer:probe` (`{peer_id,state:"pending"|"up"|"timeout"|"error",rtt_ms,error}`).
SSE hub: per-subscriber bounded queues, event IDs, `Last-Event-ID` replay ring,
slow-consumer disconnect. Two clients see every event.

### 8.5 API contract

All responses JSON; errors use
`{code,message,remediation,details,request_id}`. Strict decoding, 16 KiB body
cap, per-caller rate limit, audit of mutations (uid/SID, action, target,
result; never secrets). `GET /capabilities` returns protocol/app version and
feature flags.

```
GET    /api/v1/capabilities
GET    /api/v1/status
GET    /api/v1/identity          {id, public_key, name}
GET    /api/v1/config
PATCH  /api/v1/config            allowlist; {restart_required, applied}
POST   /api/v1/connection        {action}
POST   /api/v1/name              deprecated alias `{name}` → 204; removed in
                                 protocol v2 (CLI migrated in W2)
POST   /api/v1/invites/peer      {invite}
POST   /api/v1/invites/network   {join, warning, snapshot}
POST   /api/v1/peers             {invite}  (existing)
DELETE /api/v1/peers/{id}
POST   /api/v1/peers/{id}/probe  {state:"pending"} + event result
GET    /api/v1/routes
GET    /api/v1/activities
GET    /api/v1/logs?since=&limit=
GET    /api/v1/events            SSE + Last-Event-ID
GET    /api/v1/diagnostics       redacted bundle
GET    /api/v1/security          config/identity owner+mode, socket ACL/uids,
                                 update-signature state, firewall manager
GET    /api/v1/firewall          {manager,status,rules,last_check,last_error}
POST   /api/v1/firewall/repair   transactional
```

`PATCH /config` allowlist: `node.name`, `node.listen_port`, `node.mtu`,
`node.interface`, `node.public_addr`, `discovery.mdns`, `discovery.stun`,
`discovery.stun_server`. Reject `api.*`, `network_secret`, identity, peers,
overlay prefixes (after join), and unknown fields. Cross-field validation;
restart matrix documented; merge under the same config lock as peer
persistence; preflight and rollback on restart failure.

### 8.6 Config ownership and migration

Root-owned `/etc/skein/skein.toml` and `identity.key` (0600, dir 0750
root:root); Windows `%ProgramData%\Skein` with a DACL for SYSTEM,
Administrators, and the service account only (no interactive user). Remove
`ChownToSudoUser` from every root config/identity writer and remove
`fileperm.PreserveOwner` from the daemon's `persistLocked`; add a root-only
writer and a separate Windows service-only DACL writer used by helper
`init`/`import` and daemon persistence. Keep `fileperm.Restrict` for the
user-mode dev CLI. Startup rejects a non-root-owned config on Unix. The CLI
`init`/`join`/`run -c` paths are dev-only and must not be used by the desktop;
production bootstrap is the helper. The helper
`init`/`join` generates secrets and writes these; `import` validates a source
file (owner, mode, regular file, parent chain), copies atomically, writes the
platform `api.socket`/`api.pipe` fields, and disables/removes the legacy
`api.listen`/`api.token` path so the upgraded GUI connects over the socket. Migrate
identity + config, rewrite the systemd unit/SCM binPath to the new path,
migrate the CLI to the socket, remove the "chown back to user" hint, keep a
backup, and test upgrade from an existing service. The GUI never reads these
files.

### 8.7 Secret model

The GUI transiently handles join strings (which contain the network secret) to
display/copy/QR them. It never persists or logs them; reveal-on-demand;
clipboard auto-clear; no screenshots/exports; disclosure is audited and
rate-limited. Same-user processes can also call the socket; the trust boundary
is the OS user, not the GUI. The GUI does not read the config/identity files or
the raw secret.

## 9. Security model

- GUI unprivileged; no normal-operation elevation.
- Kernel-verified IPC; socket/pipe DACL; audit; per-caller limits; SSE caps.
- Root-owned config/identity; service-only Windows DACL; parent-chain checks;
  `O_NOFOLLOW`/fstat.
- Fixed-verb helper; validated inputs; no free-form shell; installed root-owned
  and immutable (not from AppImage).
- Firewall: root daemon, transactional add-before-remove, unique names,
  rollback, status inspection; argv/COM, no PowerShell interpolation.
- Updates: signed manifests, digest verification; per-format paths; protocol
  version compatibility; rollback.
- Secrets: transient handling, redaction, no logs/exports, clipboard clear.
- Webview: no direct daemon access; CSP; no remote content; external links via
  OS; sanitized update notes.
- Same-user malware is outside the isolation boundary; documented.

## 10. Platform integration

### Linux

- systemd unit: root, `ProtectSystem=strict`, `ProtectHome=yes`,
  `PrivateTmp=yes`, `NoNewPrivileges=yes`, `StateDirectory=skein`,
  `RuntimeDirectory=skein`, `ReadWritePaths=/etc/skein /var/lib/skein
  /run/skein /etc/ufw /etc/firewalld /run/ufw /run/xtables.lock` (optional
  paths prefixed `-`), `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
  AF_NETLINK`. Firewall repair is tested under the installed unit.
- `skein` group + socket ACL; polkit actions for helper verbs; sysusers/
  tmpfiles units; `/usr/libexec/skein-helper` root-owned.
- Tray: StatusNotifierItem; if unavailable, disable close-to-tray/start-
  minimized and show a notice. GNOME AppIndicator extension documented.
- Packaging: DEB/RPM/Arch install helper/service/polkit; AppImage is GUI-only.
  Onboarding detects a missing helper and shows a package-install screen
  (Create/Join disabled with copyable package guidance); an AppImage E2E test
  covers this state.

### Windows

- Service: LocalSystem (chosen), config DACL SYSTEM/Administrators/service
  account only; no interactive user read.
- Named pipe via direct Win32 handles; server impersonation + client SID check; client
  server PID/image check; pipe-squatting tests.
- Helper `skein-helper.exe` with `requireAdministrator`; fixed verbs; installed
  root-owned under Program Files; not run from a user-writable path.
- Wintun staged/validated; firewall via argv/netsh or COM; NSIS installer
  (WebView2 bootstrapper, helper, Wintun, service). No MSIX for service
  install.
- Windows runtime install/upgrade/repair/uninstall is a hard release gate; if
  no VM is available, the Windows build is labelled **preview** and the
  production claim is withheld. This release supports one interactive Windows
  user; multi-user requires a future grant flow.

## 11. States and errors

Per-screen matrix: onboarding (existing config, elevation denied, service start
failure, invalid/stale join, socket denied, TUN/Wintun missing, tray missing);
dashboard (service stopped, connection paused, protocol mismatch, stale);
peers (probe pending/timeout, add invalid invite, remove failure); invites
(generation failure, clipboard denied, stale snapshot, rotation failure);
security (no firewall manager, repair failure/rollback); activity (no samples,
history off/full); diagnostics (logs unavailable, bundle failure); settings
(validation, restart failure/rollback, update signature failure).

Errors: code, human message, what happened, what to do, copyable details. No
raw stack traces. Destructive confirmations: remove peer, rotate secret, reset
(keep/purge), uninstall/data choice, join over existing network, restart,
firewall repair, update. Typed confirmation for rotation/reset/uninstall.

## 12. Accessibility

WCAG 2.2 AA; automated contrast tests; high-contrast theme; no color-only
state (glyph + label). Keyboard: Tab/Shift+Tab, roving tabindex in lists and
graph (or list-only alternative), Enter/Space, Escape, Ctrl/Cmd+K; focus trap;
visible focus. 44×44 hit areas where practical; 200% zoom reflow to ~450 CSS px
(single column); charts/topology/QR have data-table/text alternatives;
`aria-live` throttled for connection/peer changes; reduced motion honored;
screen-reader labels for icon buttons and graph summary.

## 13. Performance

60 FPS; transform/opacity/stroke-dashoffset; filter/shadows only on small
areas; no per-frame React state; topology memoized/simple, hide offscreen;
virtualized lists >100; logs capped; charts ≤10 Hz; route-level code splitting;
initial bundle target <450 KB gzip with a size-limit gate; pause hidden;
bounded history; no main-thread block >50 ms.

## 14. Testing and acceptance

Go: IPC auth (uid/SID, ACL, pipe squat), config patch allowlist/merge,
bootstrap/join/rotate, log/event rings, SSE fan-out/replay, firewall
transaction/rollback, migration/upgrade, connection pause/resume, helper verbs;
`-race`. Frontend: Vitest/RTL, MSW, Playwright browser flows, contrast/axe,
keyboard, reduced motion, 200% zoom, bundle size. Integration: daemon on temp
socket; two loopback nodes; invite/join; pause/resume; rotate; logs; firewall
mock. Visual: computer-use screenshots at 1440×900, 1280×800, 900×600, light/
dark/high-contrast/reduced motion. Platform: Linux clean install/uninstall;
Windows VM install/upgrade/repair/uninstall hard gate (else Windows preview).

## 15. Packaging and release

- DEB/RPM/Arch/NSIS: helper + service + polkit/UAC + Wintun + desktop entry;
  full functionality.
- AppImage: GUI-only; no service install; clear "install a package" path.
- Updates: AppImage self-update; DEB/RPM/Arch via package manager (notify,
  never overwrite package files); Windows via signed helper updating service
  binaries with consent and rollback. Protocol version in `/capabilities`;
  mismatch prompts update.
- CI: Go tests/race/vet/staticcheck; frontend lint/typecheck/test/build/size;
  Wails Linux build; Windows cross-build + VM job; package artifacts;
  checksums/signing.

## 16. Acceptance criteria

Functional: install/init without terminal; create/join; invites + QR; add peer
from invite; peers/routes/topology/activity real; pause/resume; firewall
status/repair; logs/events/bundle; settings + restart; service lifecycle;
rotate secret; tray/notifications; updates; uninstall. Visual: cohesive
light/dark/high-contrast, logo animation, designed states, 60 FPS. Security:
GUI unprivileged, kernel IPC, root config, audited mutations, redacted secrets,
same-user boundary documented. Platform: Linux package install/uninstall;
Windows VM gate or preview label.

## 17. Risks

IPC/config migration, Windows pipe/service behavior, firewall safety, GTK/
WebKit distribution coverage, update ownership, and honest unsupported states.
Mitigations are gates in the plan, not follow-ups.
