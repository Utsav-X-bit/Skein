# Skein Desktop GUI — Implementation Plan (rev. 4)

Date: 2026-11-03
Spec: `docs/superpowers/specs/2026-11-03-skein-desktop-gui-design.md`
Research: `docs/superpowers/research/2026-11-03-skein-desktop-gui-research.md`
Reviews: `docs/superpowers/reviews/2026-11-03-gui-review-architecture.md`,
`docs/superpowers/reviews/2026-11-03-gui-review-ux.md`
Status: revised after rejection; awaiting re-review

## 1. Goals and non-goals

Goals: complete GUI for every real Skein capability; unprivileged GUI;
kernel-verified IPC; root-owned config; Windows/Linux installers; premium
design/motion; accessible; tested; documented.

Non-goals: exit nodes, subnet routers, invite expiry/roles, pending requests,
accounts/SSO, per-peer ACLs, arbitrary OS firewall rules, DNS/magic DNS,
macOS/iOS/Android. These are documented as unsupported, not faked.

## 2. Locked decisions

1. Daemon root/LocalSystem; config/identity root-owned; no `User=skein`.
2. UDS/named pipe is the only GUI control plane; loopback TCP dev-only.
3. Connection pause/resume is a daemon state, separate from service lifecycle.
4. Revocation = network-secret rotation; peer removal is local-only.
5. Bootstrap = `skein-helper init|join`; GUI handles join strings transiently.
6. Windows pipe via direct Win32 handles; both sides verify identity.
7. AppImage GUI-only; DEB/RPM/Arch/NSIS install the root-owned helper/service.
8. Unsupported features removed from UI, not disabled controls.
9. SSE broadcast with per-subscriber queues and replay.
10. Network name is a local GUI label.

## 3. Architecture and contracts

### 3.1 Processes

- `skein-desktop` (Wails, unprivileged): React + Go services.
- `skeind` (root/LocalSystem): daemon + socket/pipe + connection pause/resume +
  log/event rings + new endpoints.
- `skein-helper` (root-owned, elevated): `init`, `join`, `import`, `install`,
  `uninstall`, `start`, `stop`, `restart`, `status`, `rotate-secret`, `reset`,
  `grant-user`, `set-owner`, `update-daemon`. Service status reads are
  unprivileged (SCM/systemd) and do not use the helper.

### 3.2 IPC

- Linux `/run/skein/control.sock` 0660 root:skein + per-user ACL; socket DACL
  authorizes, `SO_PEERCRED` uid recorded/audited; `SO_PEERGROUPS` optional.
- Windows `\\.\pipe\skein-control`, DACL SYSTEM/Administrators/installing user;
  server impersonates client and checks token SID; client checks server PID
  image/token; `PIPE_REJECT_REMOTE_CLIENTS`.
- HTTP/JSON + SSE; `Origin` rejection stays; `api.socket`/`api.pipe` config;
  TCP disabled by default, non-fatal if enabled.
- Errors `{code,message,remediation,details,request_id}`; strict decode; 16 KiB
  cap; per-caller rate limit; audit mutations without secrets.

### 3.3 Connection state

`service` (running/stopped/starting/error) and `connection`
(connected/paused/connecting/error) are separate. `POST /connection` pauses
(cancel engine, close TUN/link, remove interface/routes, drop sessions, persist
paused) or resumes. Service restart honors paused. Service start/stop is
elevated and labelled "Service".

### 3.4 API

Complete contract in `docs/gui-api.md` (created in W0): schemas for
`Capabilities`, `Status`, `Identity`, `ConfigView`/`ConfigPatch`, `Connection`,
`Invite`, `Peer`, `Route`, `Activity`, `LogEntry`, `Event`, `FirewallStatus`,
`ServiceStatus`, `DiagnosticBundle`, `APIError`. Endpoints per design §8.5.
`PATCH /config` allowlist; restart matrix; merge under config lock; preflight/
rollback. `GET /identity`; `ConfigView` adds `name`, `public_addr`, `stun`,
`stun_server`, `config_path`, `protocol_version`.

### 3.5 Wails services/events

`AppService`, `ConnectionService`, `PeerService`, `InviteService`,
`NetworkService`, `ConfigService`, `FirewallService`, `DiagnosticsService`,
`ServiceService`, `UpdateService`, `HistoryService`. Events per design §8.4.
Typed bindings; contract tests compare Go/TS fixtures.

## 4. Workstreams and file ownership

Rule: exactly one owner per file. Shared contracts land before parallel work.

| ID | Owner | Files |
|---|---|---|
| W0 | Lead | `docs/gui-api.md`, `internal/api/types.go`, `internal/version/protocol.go`, `gui/internal/contracts/*.go`, `gui/frontend/src/lib/types.ts`, `gui/frontend/src/lib/errors.ts`, contract fixtures, `.github/workflows/ci.yml` |
| W1 | IPC/security | `internal/api/server.go`, `ipc_unix.go`, `ipc_windows.go`, `auth.go`, `internal/conf/socket.go`, `internal/daemon/listeners.go` |
| W2 | Helper/service/config | `cmd/skein-helper/*`, `cmd/skein/main.go`, `cmd/skeind/main.go`, `internal/service/*`, `internal/app/*`, `internal/fileperm/*`, `internal/conf/config.go`, `internal/conf/system.go`, `internal/firewall/*`, `internal/cli/*.go` (CLI migration/dev-only guard) |
| W3 | API/daemon features | `internal/api/routes.go`, `handlers_*.go`, `internal/api/identity.go`, `internal/daemon/daemon.go`, `connection.go`, `logs.go`, `events.go`, `config_patch.go`, `invite.go`, `diagnostics.go`, `firewall_status.go`, `security.go` |
| W4 | Shell/design system | `gui/main.go`, `gui/app.go`, `gui/services/*`, `gui/frontend/src/app/*`, `src/styles/*`, `src/components/ui/*`, `src/lib/api.ts`, `src/lib/query.ts`, `src/lib/store.ts` |
| W5 | Onboarding/dashboard | `src/routes/onboarding/*`, `src/routes/overview/*`, `src/features/connection/*` |
| W6 | Peers/invites | `src/routes/peers/*`, `src/routes/invites/*`, `src/features/peers/*`, `src/features/invites/*` |
| W7 | Topology/routes/activity | `src/routes/network/*`, `src/routes/activity/*`, `src/features/topology/*`, `src/features/activity/*` |
| W8 | Security/logs/settings | `src/routes/security/*`, `src/routes/logs/*`, `src/routes/settings/*`, `src/features/diagnostics/*`, `src/features/settings/*` |
| W9 | Motion/assets | `src/motion/*`, `src/components/brand/*`, `gui/frontend/public/assets/*`, `build/appicon.png`, `build/assets/*` |
| W10 | Packaging/tray/updates | `gui/tray.go`, `gui/updater.go`, `gui/singleinstance.go`, `build/linux/*`, `build/windows/*`, `Taskfile.yml`, `.github/workflows/gui.yml`, `docs/install/*` |
| W11 | QA/audit | `test/gui/*`, `gui/frontend/e2e/*`, `gui/frontend/scripts/contrast.test.ts`, `gui/frontend/scripts/size-limit.ts`, `docs/superpowers/reviews/gui-audit-*.md` |

Feature owners write colocated unit tests; W11 owns E2E, audits, and scripts.
W4 owns `gui/main.go`; W10 exposes `registerTray(app)`/`registerUpdater(app)`
called from W4's main.

## 5. Phases and hard gates

### Phase 0 — Contracts (W0, blocking)

- `docs/gui-api.md` with schemas, errors, restart matrix, merge semantics,
  audit, protocol version, capability matrix, SSE IDs.
- Go/TS types and contract fixtures; generator or CI fixture test.
- Wails v3.1 hello-world build (local GTK4/WebKitGTK 6.0).
- Pin Wails v3.1.0, React 19, Tailwind 4, React Flow, Motion.
- Add CI jobs: Go tests/race/vet/staticcheck; frontend lint/typecheck/test/
  build/size; Wails Linux build; Windows cross-build.

Gate: contract tests pass; `wails3 build` hello-world; docs reviewed.

### Phase 1 — Security/IPC/bootstrap (W1+W2+W3, blocking)

- UDS listener + ACL + SO_PEERCRED; Windows pipe via direct Win32 handles + impersonation +
  client server verification; pipe-squat tests.
- Fatal socket/pipe bind; TCP dev-only/non-fatal; config `api.socket`/`api.pipe`.
- Root-owned config/identity; helper `init`/`join`/`import`/`rotate-secret`/
  `grant-user`; service unit/binPath migration; Windows service DACL; CLI socket
  client; remove chown hint.
- Connection pause/resume; status/service split; persisted paused state.
- SSE hub with replay; log/event rings; audit log.
- API: capabilities, identity, config patch allowlist, invites, connection,
  logs, diagnostics, firewall status/repair.
- Firewall transactional repair + status (add-before-remove, unique names,
  rollback, ufw forward rules); Windows argv/COM.
- Tests: allowed/denied uid/SID, supplementary group/ACL, pipe squat, config
  allowlist/merge, bootstrap/join/rotate, migration/upgrade, pause/resume, SSE
  fan-out, firewall rollback, audit redaction.

Gate: clean Linux systemd container/VM (not a temp root) install via helper;
unprivileged GUI client connects; wrong user denied; ACL survives daemon
restart; daemon reads/writes root config; helper init → service starts → join
second node; rotate secret invalidates old join; TCP squat cannot stop daemon;
firewall repair works under the installed unit; `go test -race ./...`.

### Phase 2 — Shell/design/assets (W4+W9)

- Wails app, single instance, native decorations on Linux, custom titlebar on
  Windows (tested), tray registration hook, notifications hook, theme sync.
- Tokens incl. high contrast; fonts; shadcn primitives; shell; palette; toasts;
  error boundary; responsive/zoom.
- Logo SVG, variants, tray glyph, icons, splash; motion primitives; reduced
  motion; glass fallback.
- Typed services/events; TanStack Query/Zustand; MSW.
- Bundle size gate; contrast tests; axe.

Gate: 1440×900/1280×800/900×600, 200% zoom, light/dark/high-contrast, reduced
motion, keyboard shell, bundle <450 KB gzip.

### Phase 3 — Onboarding/dashboard (W5)

- Stepper; existing-config detection/migration; create/join via helper; service
  install; permissions/socket ACL; tray availability; connect; first invite.
- Dashboard hero/metrics/sparkline/topology preview/live events; derived
  connection/health states; pause/resume; empty/error/disconnected.
- Tests: clean-machine create/join (real helper), elevation cancel, stale join,
  socket denied, tray missing, state derivation.

Gate: clean Linux systemd VM/container: install → create network → service
starts → invite/join second node → dashboard connected; pause/resume; restart
honors paused; AppImage no-helper guidance.

### Phase 4 — Peers/invites/topology/routes/activity (W6+W7)

- Peers list/search/filter/sort/inspector/probe async/remove local-only;
  add-from-invite.
- Invitations peer/network, QR, copy, reveal, stale snapshot, rotate flow UI.
- React Flow topology (direct/relay, path cost, link RTT, no fake per-edge
  RTT), accessible list; routes table/inspector.
- Activity charts/history opt-in/retention/export; privacy copy.
- Tests: add peer round-trip, probe states, removal copy, invite snapshot,
  graph transforms, history caps.

Gate: two/three loopback nodes; add peer; topology matches routes; probe result;
remove local-only; activity updates; history off by default.

### Phase 5 — Security/logs/settings/updates (W8+W10)

- Security: firewall manager/status/rules/last error/repair; notices; no ACL.
- Diagnostics: logs/events/tests/bundle redaction; copy/save.
- Settings: general/network/security/updates/advanced/about; patch/restart/
  rollback; reset; uninstall semantics per package.
- Updates: per-format; protocol version; signed helper update; consent/rollback.
- Tray fallback; notifications debounce/mute; quit semantics.
- Tests: firewall rollback, bundle redaction, patch allowlist, restart failure
  rollback, rotation, update signature failure, tray missing.

Gate: settings patch → restart → persisted; failed restart rolls back; bundle
contains no secrets/join strings; firewall repair failure restores rules; tray
fallback disables close-to-tray.

### Phase 6 — Packaging/platform/QA (W10+W11)

- Linux DEB/RPM/Arch (helper/service/polkit/sysusers/tmpfiles/desktop/icons);
  AppImage GUI-only with package guidance.
- Windows NSIS (helper/service/Wintun/WebView2); DACL; firewall; service
  recovery; no MSIX service install.
- CI packaging, checksums, signing hooks.
- Accessibility audit (contrast, keyboard, screen reader, zoom, reduced
  motion); performance audit (60fps, startup, memory, hidden pause); visual
  audit via computer use at 3 sizes × 3 themes.
- Docs: install/uninstall/update, known limitations, migration.

Gate: Linux clean install/upgrade/uninstall; Windows VM install/upgrade/repair/
uninstall **and init/join/rotate-secret through the helper pipe** (hard gate;
otherwise Windows labelled preview and production claim withheld); audits pass;
reviewer sign-off.

## 6. Detailed tasks

### W0
- [ ] `docs/gui-api.md` full contract; protocol version; error envelope.
- [ ] Go/TS types + fixtures + CI contract test.
- [ ] Wails v3.1 hello-world; pin versions.
- [ ] CI skeleton.

### W1
- [ ] `ipc_unix.go`: listener, mode/ACL, ConnContext, SO_PEERCRED,
  SO_PEERGROUPS optional.
- [ ] `ipc_windows.go`: direct Win32 named pipe (not go-winio, whose conn hides
  the handle): `FILE_FLAG_FIRST_PIPE_INSTANCE`, `PIPE_UNLIMITED_INSTANCES`
  (bounded by the server connection cap > max REST+SSE), DACL, `LockOSThread` +
  `ImpersonateNamedPipeClient` + token SID + `RevertToSelf`, client
  `SECURITY_IDENTIFICATION` + server PID/image check. Persist granted SIDs;
  single interactive user for this release. Test two clients + SSE.
- [ ] `auth.go`: listener-specific auth (UDS/pipe caller identity bypasses
  bearer; TCP requires bearer), mutation audit, per-caller limits. Config
  fields/validation (`api.socket`/`api.pipe`, optional `api.listen`/`api.token`)
  are W2-owned; W1 consumes them.
- [ ] Fatal socket/pipe bind; stale-socket unlink after verification
  (`RuntimeDirectory=skein`); TCP dev-only/non-fatal; `api.listen`/`api.token`
  optional when TCP is disabled; persist granted uids in `/etc/skein/access`
  and reapply ACLs at listener creation; SIGKILL restart test.
- [ ] Tests: uid/SID allow/deny, ACL, pipe squat, TCP squat, SSE caps.

### W2
- [ ] `skein-helper` verbs + JSON output; root-owned installed path.
- [ ] `init`: flags/JSON for node name, overlay prefix, port, MTU, mDNS/STUN;
  identity/secret/config generation, service install/start, returns public
  metadata + join/peer invite.
- [ ] `join`: read invite from stdin/inherited pipe (never argv), validate,
  zero the buffer, generate identity/config, service, public data. Windows:
  GUI-created helper pipe (installing user/Administrators/SYSTEM DACL), pipe
  name only in argv; helper connects, reads JSON, writes JSON. Apply the same
  channel to `init` and `rotate-secret` results.
- [ ] `import`: validate source owner/mode/regular/parent, copy, rewrite unit/
  binPath, write `api.socket`/`api.pipe`, disable legacy `api.listen`/token,
  backup; test GUI connect after upgrade.
- [ ] `rotate-secret`: backup, stop, rotate, start, new join.
- [ ] `grant-user`: persist uid and apply socket ACL; `status` (unprivileged
  SCM/systemd read) authoritative for service state; `set-owner` for Windows
  single-user change; `reset`: backup, stop, remove config/identity per
  keep/purge, optional re-init, start; `update-daemon`: signed manifest,
  consent, stop, swap, health-check, rollback.
- [ ] systemd hardening; Windows service DACL/recovery.
- [ ] CLI socket client; remove `ChownToSudoUser` from root writers; mark
  `init`/`join`/`run -c` dev-only; remove chown hint; migration.
- [ ] Firewall status/repair transactional + ufw forward rules + Windows argv/
  COM.
- [ ] Tests: helper validation, migration/upgrade, firewall failure rollback,
  service account read/write, ACL/SID persistence across restart, root-only
  Windows DACL.

### W3
- [ ] `connection.go`: pause/resume, paused state in
  `/var/lib/skein/state.json`, status/health truth table (service from
  SCM/systemd read, connection from daemon), wire UDS/pipe listeners
  into `daemon.Run` (replacing the TCP-only `serveAPI`), remove
  `fileperm.PreserveOwner` from `persistLocked`, reject non-root config at
  startup.
- [ ] `events.go`: broadcast hub, IDs, replay ring, slow consumers,
  `peer:probe` event.
- [ ] `logs.go`: ring buffer, redaction.
- [ ] `invite.go`: peer/network invite, snapshot warning.
- [ ] `config_patch.go`: allowlist, merge lock, restart matrix, rollback;
  `/name` alias. Pause state in `/var/lib/skein/state.json` with
  `StateDirectory=skein`.
- [ ] `identity.go` handler.
- [ ] `diagnostics.go`: bundle schema/redaction.
- [ ] `security.go`: config/identity owner+mode, socket ACL/uids, update
  signature state, firewall manager.
- [ ] `firewall_status.go`.
- [ ] `routes.go` registration (W1 owns server core); `POST /api/v1/name`
  alias `{name}` → 204, deprecated, removed in protocol v2; wire UDS/pipe
  listeners into `daemon.Run`.
- [ ] Tests: contract, allowlist negative, pause/resume, SSE two clients,
  replay, redaction.

### W4
- [ ] Wails app/window/single instance/theme; hooks for tray/updater.
- [ ] Tokens/fonts/base CSS; shadcn primitives; shell/palette/toasts/error
  boundary; responsive/zoom.
- [ ] Services/events/query/store/MSW; generated bindings.
- [ ] Tests: shell, keyboard, theme, contrast hook.

### W5
- [ ] Onboarding stepper + migration detection; create/join; service install;
  permissions; connect; first invite; AppImage/no-helper package guidance
  branch; local network label (`networkLabel`, default "My Skein", editable).
- [ ] Dashboard hero/metrics/sparkline/topology preview/events; states.
- [ ] Connection pause/resume UI + state/health derivation; node name field.
- [ ] Tests: clean-machine create/join, cancel, stale join, socket denied, tray
  missing, AppImage no-helper state, service stopped/starting/error without a
  daemon.

### W6
- [ ] Peer list/filter/sort/inspector; add-from-invite; probe async; remove
  local-only confirmation.
- [ ] Invitations peer/network/QR/copy/reveal/stale/rotate.
- [ ] Tests: round-trip, probe states, remove copy, invite snapshot.

### W7
- [ ] React Flow graph/custom nodes/edges/radial/filters/minimap/list
  alternative; no fake RTT.
- [ ] Routes table/inspector/path highlight.
- [ ] Activity charts/history opt-in/retention/export.
- [ ] Tests: graph transforms, accessibility list, history caps.

### W8
- [ ] Security firewall/notices; diagnostics logs/events/tests/bundle; settings
  sections; updates UI; reset/uninstall.
- [ ] Tests: rollback, redaction, patch/restart, update signature failure, tray
  fallback.

### W9
- [ ] SVG logo recreation/masks; variants; tray glyph; icons; splash; motion
  primitives; reduced motion; glass fallback.
- [ ] Performance checks for logo/topology.

### W10
- [ ] Linux packaging + polkit/sysusers/tmpfiles/desktop; AppImage GUI-only.
- [ ] Windows NSIS/helper/Wintun/WebView2; service DACL/recovery; firewall.
- [ ] Updater per format; protocol version; signing hooks.
- [ ] CI packaging/VM job.

### W11
- [ ] E2E: clean Linux create/join/remove/rotate/pause; upgrade; uninstall.
- [ ] Windows VM install/upgrade/repair/uninstall (hard gate or preview).
- [ ] Accessibility: contrast/axe/keyboard/screen reader/zoom/reduced motion;
  every token pair including high contrast in the automated test.
- [ ] Performance: 60fps/startup/memory/hidden pause.
- [ ] Visual screenshots at 3 sizes × 3 themes; fix list.
- [ ] Final audit report.

## 7. Dependencies and parallel batches

- W0 blocks all.
- Batch A: W1, W2, W3 (file ownership disjoint; W3 registers routes in
  `routes.go`, W1 owns `server.go`).
- Batch B: W4 + W9 (shell and assets; W4 consumes W9 brand components via a
  contract defined in W0).
- Batch C: W5, W6, W7, W8 (disjoint route/feature directories; W4 components
  frozen).
- Batch D: W10 + W11 (packaging and QA; W10 exposes hooks; W11 audits).
- W2 and W3 both touch daemon? W2 owns service/app/firewall; W3 owns daemon
  connection/logs/events/config. No overlap.

## 8. Testing strategy

- Go unit/integration: IPC auth (uid/SID/ACL/pipe squat), config allowlist/
  merge, bootstrap/join/rotate, migration/upgrade, pause/resume, SSE fan-out/
  replay, logs/redaction, firewall rollback, helper verbs; `-race`.
- Frontend: Vitest/RTL, MSW, Playwright browser flows, contrast/axe, keyboard,
  reduced motion, 200% zoom, bundle size.
- E2E: temp socket daemon; two/three loopback nodes; create/join/add/remove/
  rotate/pause; upgrade from existing service.
- Platform: Linux clean install/upgrade/uninstall; Windows VM hard gate.
- Visual: computer-use screenshots 1440×900, 1280×800, 900×600; light/dark/
  high-contrast/reduced motion.

## 9. Security checklist

- GUI unprivileged; helper fixed verbs; root-owned helper.
- Socket/pipe DACL + kernel identity; no bearer token in GUI; audit.
- Root-owned config/identity; Windows service-only DACL; parent checks.
- Same-user trust boundary documented; join strings transient/redacted.
- Firewall transactional/rollback; no PowerShell interpolation.
- Updates signed; protocol version; rollback.
- No webview direct API; CSP; sanitized update notes.

## 10. Packaging and release

- DEB/RPM/Arch/NSIS: full service/helper/Wintun/polkit/UAC.
- AppImage: GUI-only; package guidance.
- Updates: per format; package manager for system installs; signed helper for
  Windows; AppImage self-update.
- CI artifacts/checksums/signing; release notes; known limitations.

## 11. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Root config/helper migration breaks users | Backup, migration tests, existing-service upgrade gate |
| Windows pipe/service runtime | direct Win32 handles + VM hard gate; else Windows preview |
| Firewall lockout | Transactional add-before-remove, rollback, status |
| GTK/WebKit distribution | DEB/RPM current; AppImage GUI-only; minimums |
| React Flow/WebKit jank | Memoization, simple styles, hidden pause, low-end test |
| Update ownership | Per-format paths; no package overwrite |
| Secret handling | Transient only, redaction, clipboard clear, audit |
| Unsupported features | Removed UI; documented limitations |

## 12. Acceptance mapping

| Requirement | Gate |
|---|---|
| Install/init without terminal | Linux clean-machine helper E2E; Windows VM gate incl. helper pipe |
| Create/join | Phase 3 clean-machine E2E |
| Invites/QR/add peer | Phase 4 round-trip |
| Peers/routes/topology | Phase 4 real daemon |
| Pause/resume | Phase 1/3 tests |
| Firewall status/repair | Phase 1/5 rollback tests |
| Logs/bundle | Phase 1/5 redaction tests |
| Settings/restart | Phase 5 rollback test |
| Service lifecycle | Helper tests + Linux/Windows gate |
| Rotate secret | Phase 1/5 destructive test |
| Tray fallback | Phase 5 no-SNI test |
| Updates | Phase 5 signature/format tests |
| Visual/accessibility | Phase 6 audits |
| Security | IPC/auth/redaction/audit tests + reviewer |

## 13. Reviewer gate

Strict reviewer (architecture/security + UX/accessibility) must verify:
single consistent privilege/ownership model; unprivileged connection toggle;
honest revocation/secret/trust language; working pre-service create/join;
buildable/testable Windows pipe with squat tests; safe packaging/update paths;
complete API contract; no fabricated controls; WCAG AA + automated tests; hard
gates for Windows runtime, migration, firewall rollback, SSE fan-out, existing-
service upgrades. Re-review after Phase 0/1 docs and after Phase 6.
