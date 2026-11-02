# Skein Desktop GUI — Research and Feasibility Report

Date: 2026-11-03
Status: research complete, feeding the design spec and implementation plan
Scope: Windows and Linux desktop application for the existing Skein mesh VPN

## 1. Executive summary

Skein already has the hard part: a working userspace mesh, end-to-end Noise
sessions, relayed routing, a daemon, an authenticated local API, and a TUI.
What it does not have is a desktop product: no installer that a normal person
can use, no GUI, no service-control API, no peer-credential IPC, no invite
generation endpoint, no log API, and no packaging pipeline.

The recommended stack is **Wails v3.1 + Go + React 19 + TypeScript + Vite +
Tailwind CSS 4 + shadcn/ui + Lucide + React Flow + Motion**. Wails v3 is now
stable (v3.0.0 on 2026-10-26, v3.1.0 on 2026-11-01), has a system tray, native
notifications, a single-instance lock, an in-app updater, typed bindings, and
Linux DEB/RPM/AppImage/Arch plus Windows NSIS/MSIX packaging. NetBird's new
desktop app is the strongest production precedent: Wails + React frontend,
Go daemon over a local socket, typed bindings, and kernel-verified caller
identity.

The GUI must **not** run elevated. The privileged daemon owns TUN, routes,
firewall, and service lifecycle; the GUI owns presentation and calls a
restricted local API. The current loopback HTTP bearer token is not an
acceptable control plane for a root daemon: a local process can squat the port,
harvest the token, and drive the daemon. The design replaces it for the GUI with
a Unix domain socket / Windows named pipe whose caller identity comes from the
kernel (`SO_PEERCRED` / pipe client token), keeps the HTTP handler, and moves
the service config into a root-owned location.

The current engine does **not** support exit nodes, user-defined subnet routes,
invitation expiry/roles, pending membership requests, or arbitrary firewall
rules. Those screens must either be omitted or shown as explicitly unsupported;
they must not be faked. The GUI will cover every capability the engine actually
has, plus the management APIs needed to expose it.

## 2. Method and sources

Reviewed the repository at `c572813` (Go 1.27, 13k non-test lines), the CLI,
daemon, API, config, firewall, service, TUN, and platform code, plus the
existing `Logo.png` (1254×1254, RGB, white background, black woven infinity
mark). Ran three read-only subagent audits (backend feature inventory,
OS integration, security threat model) and web research against primary
sources.

Primary sources:

- Wails v3 status and release: <https://wails.io/status/>, <https://wails.io/blog/wails-v3-released/>
- Wails v3 features: <https://wails.io/whats-new/>
- Wails application/window APIs: <https://wails.io/reference/application/>, <https://wails.io/reference/window/>
- Wails services and bindings: <https://v3.wails.io/features/bindings/services/>
- Wails events: <https://v3.wails.io/guides/events-reference/>
- Wails single instance: <https://wails.io/guides/single-instance/>
- Wails notifications: <https://wails.io/features/notifications/overview/>
- Wails updater: <https://wails.io/guides/updater/>
- Wails Linux packaging: <https://wails.io/guides/build/linux/>
- Wails Windows packaging: <https://v3.wails.io/guides/build/windows/>
- NetBird desktop app: <https://docs.netbird.io/client/desktop-app>
- NetBird gRPC socket and privileged operations: <https://docs.netbird.io/client/grpc-socket>
- NetBird daemon IPC security fix: <https://netbird.io/knowledge-hub/netbird-v0-76-daemon-ipc-security>
- Tailscale client features: <https://tailscale.com/docs/features/client>
- Tailscale Linux tray: <https://tailscale.com/docs/features/client/linux-systray>
- Tailscale exit nodes: <https://tailscale.com/docs/features/exit-nodes>
- React Flow performance: <https://reactflow.dev/learn/advanced-use/performance>
- pkexec: <https://www.freedesktop.org/software/polkit/docs/latest/pkexec.1.html>
- Windows named-pipe security: <https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights>
- Exo visual reference: <https://github.com/exo-explore/exo>, `dashboard/src/app.css`, `dashboard/src/lib/components/TopologyGraph.svelte`

## 3. Existing product audit

### 3.1 What exists and works

| Capability | Backend | API today | Notes |
|---|---|---|---|
| Node identity, network secret, config | `internal/identity`, `internal/conf` | read-only redacted `/config` | `skein init` writes `identity.key` + `skein.toml` |
| Create/join network | `skein init`, `skein join` | none | invite strings: `skein:1:` peer, `skein:2:` network |
| Peer list, status, RTT, transport | `peer.Table`, `control` | `GET /peers` | real state |
| Add peer from invite | `daemon.AddPeer` | `POST /peers` | persists config |
| Remove peer | `daemon.RemovePeer` | `DELETE /peers/{id}` | drops session/routes |
| Probe peer | `control.Probe` | `POST /peers/{id}/probe` | starts handshake / challenge |
| Routes | `route.Table` | `GET /routes` | distance vector, max 4 hops |
| Activity samples | `activity.Cache` | `GET /activities` | aggregate, payload-free |
| Live events | `daemon.emit` | `GET /events` (SSE) | peer up/down, discovered, name |
| Local name | `daemon.SetName` | `POST /name` | advertised in STATE |
| Firewall automation | `internal/firewall` | none | opens underlay port, trusts overlay interface |
| Service install | `internal/service` | none | systemd / SCM |
| Daemon logs | stderr / dashboard log file | none | no ring buffer, no API |
| Diagnostics | CLI ping, probe, routes | partial | no export, no redaction |
| TUN / Wintun | `internal/tun` | n/a | Wintun DLL required on Windows |

### 3.2 What the engine does not support

These are protocol/product gaps, not GUI gaps. The GUI must not fabricate them.

- Exit nodes (default route via a peer) and user-selected exit nodes.
- User-defined subnet routers / advertised routes.
- Invitation expiry, roles, permissions, pending membership requests, approval.
- Per-peer access control / firewall rules inside the mesh.
- Remote device rename (names are self-advertised; only the local name is mutable).
- Account/login system (there is no account server; membership is the network secret).
- DNS / magic DNS for peer names.
- Telemetry history and retention (only live in-memory samples).
- Log access, diagnostic bundle export, update metadata.
- Service start/stop/restart/status API.
- Config mutation API (only name).

### 3.3 Feature inventory

**A. Already supported by the backend**

Node identity; network secret; peer invites; network join; peer add/remove;
peer probe; local name; peer list with state/RTT/transport/direct/relay;
routes; activity; events; firewall ensure/remove; service install/uninstall;
daemon lifecycle (foreground); TUI; config validation; identity key perms.

**B. Supported but inaccessible from a GUI today**

Invite generation (peer and network); join; init/reset; config read (API) and
edit (file only); firewall status/repair; service install/uninstall/start/stop;
logs; diagnostics; update; node public key/ID; config path; mDNS/STUN state.

**C. Requires additional backend implementation**

- Peer-credential IPC (Unix socket / named pipe) and capability split.
- Root-owned service config and identity, with migration.
- Service control broker (fixed verbs; polkit/UAC).
- Invite generation endpoints.
- Config patch endpoint with restart-required flags.
- Log ring buffer and diagnostic bundle endpoint.
- Firewall status/repair endpoint; constrained mesh ACL if desired.
- Audit log for mutations.
- Local history store for telemetry (GUI side or daemon side).

**D. Requires platform-specific implementation**

- Linux: systemd service hardening, `/etc/skein` ownership, polkit action,
  GTK4/WebKitGTK 6.0 dependency, AppImage bundling, `.desktop` entry.
- Windows: named-pipe DACL and client token verification, service LocalSystem
  or virtual account, UAC helper, Wintun staging, WebView2 bootstrapper,
  NSIS/MSIX, firewall via argv not PowerShell string interpolation.

**E. Requires investigation / architectural decision**

- Whether to keep loopback TCP for legacy CLI or migrate all clients to the
  socket/pipe (recommended: socket/pipe only for the service; TCP opt-in for
  development).
- Whether the daemon runs as root or as a dedicated `skein` user with
  `CAP_NET_ADMIN`/`CAP_NET_RAW` (recommended: dedicated user + capabilities on
  Linux; LocalSystem or virtual service account on Windows).
- Whether exit nodes/subnet routes become a later engine milestone (out of
  scope for this GUI release).
- Whether telemetry history lives in the daemon (survives GUI restart) or the
  GUI (privacy, no daemon storage). Recommended: GUI-owned bounded history,
  opt-in, exportable.

## 4. Competitive analysis

### 4.1 Exo Explore (visual reference only)

Exo's dashboard is a command-center composition: near-black neutral surfaces,
one saturated accent, monospace type, thin borders, a central topology canvas,
a left rail, and a right inspector. Its `app.css` uses OKLCH tokens
(`--exo-black: oklch(0.12 0 0)`, `--exo-yellow: oklch(0.85 0.18 85)`), a
monospace stack, 0.375rem radius, scanline/grid utilities, and motion based on
stroke dash flow, pulse, and radar sweep. The topology is D3/SVG with a
radial node layout and animated dashed links.

What to borrow: composition, density, monospace data labels, radial topology,
one accent, restrained surfaces, link-flow motion.

What not to borrow: Exo's yellow/black identity, CRT/scanline kitsch, chat
product structure, the exact node-card shape, or D3-specific code. Skein gets
its own palette, type, logo motion, and graph language.

### 4.2 NetBird desktop (closest functional peer)

NetBird v0.75 moved to a Wails + React desktop app. The default view is
compact (profile, connection, hostname, overlay IP, exit node); the advanced
view adds peers/resources with search and per-peer details. The tray shows
state color, profile switching, exit-node selection, settings, and quit.
Settings are tabbed (General, Network, Security, Profiles, SSH, Advanced,
Troubleshoot, About). The Troubleshoot tab captures a debug bundle with
optional anonymization. Their daemon exposes gRPC over `unix:///var/run/netbird.sock`
and `npipe://netbird`; v0.76 added kernel-verified caller identity
(`SO_PEERCRED`, pipe client token) and privileged-operation gating after a
local privilege-escalation advisory.

What to borrow: compact/advanced view concept, tray status, tabbed settings,
debug bundle, real-time status, kernel-verified IPC, explicit privilege errors.

What to avoid: their account/SSO model (Skein has none), profile complexity,
and exposing privileged operations to unprivileged callers.

### 4.3 Tailscale (UX reference)

Tailscale's client is the benchmark for ordinary-user VPN UX: one connection
toggle, a device list, exit-node selection, route approval, tray-first
operation, and plain-language status. Its Linux tray is beta and depends on
StatusNotifierItem/D-Bus; GNOME needs the AppIndicator extension. This is a
real Linux constraint Skein must handle gracefully (tray may be unavailable;
the main window must still work).

What to borrow: connection-first hierarchy, exit-node-style selector (for
future engine support), tray-first lifecycle, plain-language diagnostics.

## 5. Framework assessment

### 5.1 Wails v3 (recommended)

Verified facts:

- v3.0.0 stable released 2026-10-26; v3.1.0 on 2026-11-01; weekly releases
  after v3.0.0. Requires Go 1.26+.
- Windows amd64/arm64 with WebView2; Linux amd64/arm64 with GTK4 +
  WebKitGTK 6.0 by default; GTK3/WebKit2GTK 4.1 legacy via `-tags gtk3` through
  v3.0.x, removed in v3.1.
- Multiple windows, system tray (D-Bus StatusNotifierItem on Linux, no
  libappindicator), native menus/dialogs, notifications, single-instance lock,
  typed bindings generated by static analysis, updater with GitHub/keygen/
  Sparkle/manifest providers and cryptographic verification, NSIS/MSIX and
  DEB/RPM/AppImage/Arch packaging.
- NetBird ships a Wails + React desktop app in production, which validates the
  architecture for a Go VPN daemon.

Risks and mitigations:

- **GTK4/WebKitGTK 6.0 availability**: Ubuntu 22.04, Debian 12, Fedora ≤39,
  RHEL 9 lack it. Mitigation: ship an AppImage that bundles WebKit (Wails
  documents this), plus DEB/RPM for current distros; document the minimum
  versions. Keep a GTK3 build for older distros only while v3.0.x supports it,
  then drop it.
- **WebKit variance**: test on GNOME/Wayland and X11; avoid exotic CSS
  features; use feature detection for backdrop-filter and `@property`.
- **CGO**: Wails requires a C toolchain; CI must install GTK/WebKit dev
  packages. Local machine already has GTK 4.24.1 and WebKitGTK 6.0 2.54.2.
- **WebView2**: Windows 10/11 usually has it; NSIS bootstrapper handles the
  rest. Enterprise offline installs need the Evergreen standalone.
- **Bundle size**: WebKit/GTK dynamic linking keeps the app small; AppImage is
  larger but portable.

### 5.2 Alternatives considered

| Option | Verdict |
|---|---|
| Wails v2 | Stable but single-window, no tray/updater/single-instance in the v3 sense; no reason to start here. |
| Tauri 2 | Strong, but the backend is Rust and the daemon is Go; adds a second systems language and IPC layer. |
| Electron | Heavy, higher memory, Chromium security surface; unnecessary for a Go-first product. |
| Fyne / Gio | Native Go UI, but weaker web-quality motion/design system and fewer rich components (React Flow, shadcn). |
| Web app in browser | Cannot manage services/firewall/TUN or run offline; not a desktop product. |

**Decision: Wails v3.1.**

## 6. Frontend stack assessment

- **React 19 + TypeScript + Vite**: Wails v3's default frontend workflow;
  typed bindings; fast HMR.
- **Tailwind CSS 4**: OKLCH tokens, CSS-first config, small runtime; matches
  the Exo-style token approach without copying it.
- **shadcn/ui + Radix**: accessible primitives (dialog, popover, tabs, select,
  tooltip, switch, toast) adapted for desktop density; copy-in components, no
  runtime lock-in.
- **Lucide**: consistent icon set; tree-shakeable.
- **React Flow (`@xyflow/react`)**: topology graph with custom nodes/edges,
  zoom/pan, minimap, controls. Performance guidance: memoize nodes/edges,
  avoid reading the whole store in node components, simplify styles, collapse
  large trees. Skein meshes are small (2–5 nodes, bounded 4 hops), so React
  Flow is comfortably within budget.
- **Motion (Framer Motion)**: layout transitions, presence, gestures; use
  transform/opacity only; `useReducedMotion`. For continuous link flow use CSS
  animations or WAAPI, not React state at 60 Hz.
- **TanStack Query**: server-state caching, polling, mutations, optimistic
  updates. **Zustand**: local UI state (selection, filters, panel sizes).
- **react-hook-form + zod**: validated forms (settings, firewall, onboarding).
- **qrcode**: render invite QR locally; no network.

## 7. Architecture recommendation

```
┌──────────────────────────────┐        ┌──────────────────────────────┐
│ Skein Desktop (unprivileged) │        │ skeind (privileged service)  │
│                              │        │                              │
│ React UI (WebView)           │        │ HTTP handler (existing)      │
│   │ typed Wails bindings     │        │   + new endpoints            │
│   ▼                          │        │   + peer-credential auth     │
│ Wails Go services            │◄──────►│ Unix socket / named pipe     │
│   (API client, tray, logs,   │  local │                              │
│    history, updates)         │  IPC   │ TUN, routes, firewall,       │
│                              │        │ sessions, control, config    │
└──────────────────────────────┘        └──────────────────────────────┘
             │                                        │
             │ fixed-verb elevation                   │ root-owned config
             ▼                                        ▼
   pkexec / UAC helper  ───────────────────►  /etc/skein (Linux)
   install | uninstall | start | stop          %ProgramData%\Skein (Windows)
```

- **GUI process**: ordinary user, Wails, no TUN/firewall/root.
- **Daemon**: system service, dedicated user + capabilities on Linux
  (`CAP_NET_ADMIN`, `CAP_NET_RAW`) or LocalSystem/virtual account on Windows.
- **IPC**: HTTP/JSON + SSE over Unix socket (`/run/skein/control.sock`) and
  Windows named pipe (`\\.\pipe\skein-control`). Caller identity from the
  kernel; no bearer token in the GUI path. The existing HTTP handler is reused;
  `ConnContext` attaches uid/gid/SID and a middleware authorizes. The loopback
  TCP listener becomes opt-in/dev-only and fails closed.
- **Elevation**: a fixed-verb helper (`skein-helper` or `skein service ...`)
  invoked via pkexec/UAC. It accepts only install/uninstall/start/stop and
  derives paths itself; it never accepts a caller-supplied config path at
  runtime.
- **Config ownership**: service config and identity live in a root-owned
  directory (`/etc/skein`, `%ProgramData%\Skein`), mode 0600, directory 0750,
  parent-chain validated. The GUI never reads the network secret or private
  key; invites are generated daemon-side.
- **Frontend**: never talks to the daemon directly; all calls go through Wails
  Go services, which expose narrow typed methods and emit events. The existing
  API's `Origin` rejection stays; the webview cannot call loopback HTTP.

## 8. Platform matrix

| Capability | Linux | Windows | GUI approach |
|---|---|---|---|
| Install/init | systemd unit, `/etc/skein`, polkit | SCM service, `%ProgramData%\Skein`, UAC | Onboarding wizard + fixed-verb helper |
| TUN | `/dev/net/tun` | Wintun DLL | Installer stages/validates |
| Service control | systemd D-Bus or `systemctl` | SCM (`sc`/`golang.org/x/sys/windows/svc`) | Helper + status API |
| Firewall | ufw/firewalld/iptables | Windows Firewall (argv/netsh or COM) | Status/repair + managed rules |
| Tray | StatusNotifierItem/D-Bus; GNOME needs extension | native tray | Wails tray; fallback window |
| Notifications | D-Bus | toast | Wails notifications |
| Auto-start | systemd user unit / XDG autostart | Run key / startup task | Settings |
| Updates | AppImage self-replace / DEB/RPM | NSIS/MSIX / binary swap | Wails updater |
| Config path | `/etc/skein/skein.toml` | `%ProgramData%\Skein\skein.toml` | Helper owns |
| Logs | journald + ring buffer | Event Log + ring buffer | API + export |
| Elevation | pkexec/polkit | UAC `runas` | Fixed verbs only |

## 9. Design direction

**Identity: "Obsidian weave".** A precise, quiet command surface built around
the woven infinity mark. Deep neutral ink, warm paper light theme, one signal
accent, hairline borders, mono data labels, generous negative space, and a
topology that feels like a living circuit rather than a dashboard of cards.

- **Dark**: background `#08090B`, surface `#101216`, raised `#151A21`,
  border `#232A34`, text `#F4F6F8`, muted `#8C96A5`.
- **Light**: paper `#F6F4EE`, surface `#FFFFFF`, raised `#FBFAF6`, border
  `#DED9CE`, text `#14171C`, muted `#6B7280`.
- **Accent**: `#4FE3C1` (signal mint) for connected/active; `#F2B35D` (relay
  amber) for relayed/degraded; `#FF6B6B` (danger coral); `#7AA7FF` (info).
- **Type**: Geist (UI), Geist Mono (data/labels). Display sizes with tight
  tracking; mono uppercase 10–11 px for metadata.
- **Shape**: 10–14 px radii, 1 px borders, no heavy shadows; glass only for
  overlays/command palette/tray popover.
- **Layout**: 232 px left rail (72 px collapsed), 56 px command bar, content
  gutters 24 px, 320 px right inspector on ≥1280 px. Responsive down to
  900×600 with a compact nav.
- **Logo**: the black weave becomes a transparent SVG with 8–10 strands. On
  connect, strands draw in from the crossings, nodes light at intersections,
  and a single mint pulse travels the weave. Idle: 6 s breathing glow.
  Reduced motion: static mark with a state dot.

## 10. Animation strategy

- UI transitions: 120–200 ms ease-out; panels 220 ms; dialogs 180 ms.
- Topology: node entrance 300 ms stagger 30 ms; edge draw 500 ms; packet
  pulses via CSS `stroke-dashoffset`/`offset-path`, not React state.
- Logo: one-time 900 ms draw on splash/onboarding; connecting state loops a
  1.8 s weave pulse; connected state settles.
- Respect `prefers-reduced-motion` and an in-app motion toggle.
- Animate only `transform`, `opacity`, `stroke-dashoffset`, `filter` (small
  areas). No full-screen blur, no continuous box-shadow on large surfaces.
- Pause animations when the window is hidden or the tab is not visible.

## 11. Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Wails/WebKit distro gaps | App won't launch on older distros | AppImage bundle; DEB/RPM for current; document minimums |
| Loopback token compromise | Root daemon takeover | UDS/npipe + peer credentials; remove token from GUI path |
| Root reads user config | Local privilege escalation | Root-owned `/etc/skein`; helper copies/validates; parent-chain checks |
| Service control needs elevation | Onboarding friction | Fixed-verb pkexec/UAC helper; clear prompts; no terminal |
| Windows named-pipe squatting | Spoofed daemon | DACL + client verifies server PID/token/image |
| Firewall rule complexity | Lockout / broken networking | Constrained model, preview, rollback, no arbitrary shell |
| React Flow performance | Jank on large meshes | Memoized nodes/edges, simple styles, hide offscreen; meshes are small |
| WebView memory | High RAM | Single window, lazy routes, no heavy charts, pause hidden |
| Logo PNG has no alpha | Bad dark-mode mark | Vectorize/recreate as SVG; transparent variants |
| Backend gaps (exit nodes etc.) | Feature promise mismatch | Explicit unsupported states; separate engine milestone |

## 12. Decisions

1. **Wails v3.1**, not v2/Tauri/Electron.
2. **React 19 + TypeScript + Vite + Tailwind 4 + shadcn/ui + Lucide +
   React Flow + Motion.**
3. **HTTP/JSON + SSE over Unix socket / named pipe**, reusing the existing
   handler, with kernel peer credentials. No bearer token in the GUI path.
   This is a demonstrably secure alternative to gRPC and avoids duplicating the
   protocol.
4. **Unprivileged GUI + privileged service**, fixed-verb elevation helper.
5. **Root-owned service config**, daemon-side invite generation, no secrets in
   the GUI.
6. **GUI-owned bounded telemetry history**, opt-in and exportable.
7. **Explicit unsupported states** for exit nodes, subnet routes, invite
   expiry/roles, pending requests, and arbitrary firewall rules.

## 13. Sources and verification notes

Verified: Wails v3 stability and platform matrix, tray/notifications/single
instance/updater/packaging, NetBird's Wails + gRPC + peer-credential design,
Tailscale's client/tray/exit-node UX, React Flow performance guidance, pkexec
semantics, Windows named-pipe security. Local toolchain verified: Go 1.27.3,
GCC 16.2.1, GTK 4.24.1, WebKitGTK 6.0 2.54.2, Node 24.21, pnpm, Bun.

Assumptions (not verified at runtime): Windows named-pipe client-token APIs
and UAC helper behavior; WebKitGTK behavior across specific distro versions;
actual visual performance on low-end GPUs. These are called out in the plan as
platform tests, not claimed as done.
