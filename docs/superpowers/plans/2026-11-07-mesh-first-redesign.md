# Mesh-first redesign — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans`. Steps use `- [ ]`. Do not run builds, tests, linters or formatters mid-task; the integration owner runs each batch's gate once. Never push.

**Goal:** Replace the card dashboard and star topology with one derived mesh model, honest per-link facts, event-first updates, bounded local history, real recovery, and full operator parity (ping, key generation, access/roles/revoke, diagnostics export, updates with notes).

**Architecture:** Pure model functions (`features/mesh/model.ts`) + one hook (`useMesh`) + one event hook (`useDaemonEvents`) feed every surface. Go changes add only source-backed read/write contracts: lifecycle forwarding in the existing event bridge, a bounded GUI-owned history store, a secret-gated grants read endpoint, fixed-helper wrappers for grant/revoke/set-owner, offline identity generation, bounded overlay ping, native diagnostic export, session timestamps, and signed release notes.

**Tech stack:** Go 1.27, Wails v3.1.0, React 19, TypeScript, TanStack Query, @xyflow/react, Radix, Tailwind tokens, zustand, sonner, vitest (dev only).

**Source of truth:** `docs/superpowers/specs/2026-11-07-mesh-first-redesign-design.md` (approved 2026-11-07). The owner explicitly deferred **only** invitation expiry, active-invite inventory and invite-use attribution. F-01 per-peer revocation remains deferred. Do not add a fake expiry/list.

## Global constraints

- No invented data: edges only from `peer.direct && peer.state==="up"`, `routes[].path`, or fresh authenticated `activity` samples. Unknown ≠ zero. Missing telemetry shows “unknown”/“—”, never 0.
- `Peer.last_seen_ms` is elapsed age, not an epoch. Route `path` excludes self and ends at destination; `cost_ms` is whole-path cost.
- Activity `rx_bytes`/`tx_bytes` are deltas over an unexported window. A remote rate requires a proven window; otherwise show bytes with “window unknown”.
- One SSE subscription in Go; one event hook in the frontend. Typed snapshot events update the query cache directly. No 2-second polling of every endpoint. 30 s reconciliation only while visible.
- ADR-001: GUI unprivileged; daemon sole config writer; fixed helper verbs; verified socket/pipe identity; `secret` requires kernel transport. Never weaken these.
- Tokens only (`var(--…)`); reduced motion honoured; canvas has a table/list equivalent; all controls keyboard reachable.
- Destructive actions keep their existing confirms and partial-completion behaviour; never repeat a completed reset.
- No new runtime dependencies. `vitest` is a dev dependency for pure-function tests.
- Verification targets: disposable/authorized machines only. The daily-use Linux install stays read-only. Phase 0's VM and Windows install no longer exist; re-provision before mutation tests.

## File map

| Area | Files |
|---|---|
| Pure model | `gui/frontend/src/features/mesh/model.ts`, `model.test.ts`, `vitest.config.ts` |
| Data hooks | `features/mesh/useMesh.ts`, `useDaemonEvents.ts`, `useSessionHistory.ts`, `features/notifications/store.ts` |
| Mesh UI | `features/mesh/MeshCanvas.tsx`, `MeshTable.tsx`, `PeerInspector.tsx`, `LinkInspector.tsx`, `RouteDrawer.tsx`, `ThroughputStrip.tsx`, `EventStrip.tsx`, `PeerHealth.tsx` |
| Routes | `routes/overview/Overview.tsx`, `routes/network/Network.tsx`, `routes/peers/Peers.tsx`, `routes/activity/Activity.tsx`, `routes/invites/Invites.tsx`, `routes/security/Security.tsx`, `routes/logs/Logs.tsx`, `routes/settings/Settings.tsx`, `routes/settings/NetworkRecovery.tsx`, `routes/onboarding/Onboarding.tsx`, `routes/about/About.tsx` |
| Shell | `components/layout/AppShell.tsx`, `CommandPalette.tsx`, `CommandBar.tsx`, `Sidebar.tsx` |
| Deleted | `features/topology/Topology.tsx` |
| Go services | `gui/services/events.go`, `history.go`, `identity.go` (new), `security.go` (new), `network.go`, `diagnostics.go`, `services.go`, `update.go` |
| Go API | `internal/api/types_gui.go`, `types.go`, `types_system.go`, `routes.go`, `handlers_security.go` (new), `internal/daemon/security.go`, `update.go`, `daemon.go`, `internal/peer/table.go`, `internal/dataplane/engine.go` |

## Shared interfaces (Task A produces; every later task consumes)

```ts
// features/mesh/model.ts
export type CounterSample = { t: number; rx: number; tx: number; uptime: number };
export type Rate = { rxPerSec: number; txPerSec: number };
export type HistorySample = { t: number; rxPerSec: number; txPerSec: number; peersUp: number; peersTotal: number };
export type Evidence = "direct" | "route" | "observed";
export type MeshNode = { id: string; label: string; overlay?: string; kind: "self" | "peer" | "intermediate"; state: string; rttMs?: number; transport?: string; hops?: number };
export type MeshEdge = { id: string; from: string; to: string; evidence: Evidence; fresh: boolean; rttMs?: number };
export type MeshPath = { destination: string; nextHop: string; path: string[]; hops: number; costMs: number };
export type MeshModel = { nodes: MeshNode[]; edges: MeshEdge[]; paths: MeshPath[]; stale: boolean };
export type Presence = "active" | "idle" | "offline" | "unknown";

export function computeRate(prev: CounterSample | null, next: CounterSample): Rate | null;
export function buildMesh(self: string, peers: Peer[], routes: Route[], activities: Activity[], now: number): MeshModel;
export function dedupeActivities(rows: Activity[]): Activity[];
export function activityWindow(prev: Activity | null, next: Activity): { dtMs: number; rxPerSec: number; txPerSec: number } | null;
export function bucketActivities(rows: Activity[], windowMs: number): ActivityBucket[];
export function presenceOf(peer: Peer, lastTrafficAt: number | undefined, now: number): Presence;
export function peerHealth(peer: Peer, presence: Presence): { tone: "ok" | "warn" | "down" | "unknown"; label: string; detail: string };
export function formatRate(rate: Rate | null): string;
export function formatAge(ms: number | undefined): string;

// features/mesh/useMesh.ts
export function useMesh(): {
  status?: Status; peers: Peer[]; routes: Route[]; activities: Activity[];
  model: MeshModel; totalRate: Rate | null; rates: Map<string, Rate>;
  isLoading: boolean; error: unknown; staleSince?: number; refetch: () => void;
};

// features/mesh/useDaemonEvents.ts
export function useDaemonEvents(): void; // mounted once in AppShell
```

```go
// gui/services/history.go — keep the five existing method names
type HistorySample struct {
    AtMS int64 `json:"at_ms"`; NodeID string `json:"node_id"`
    RxBytes uint64 `json:"rx_bytes"`; TxBytes uint64 `json:"tx_bytes"`
    PeersUp int `json:"peers_up"`; PeersTotal int `json:"peers_total"`
    UptimeSeconds int64 `json:"uptime_seconds"`
}
func (s *HistoryService) Record(ctx context.Context, sample HistorySample) error
func (s *HistoryService) Samples(ctx context.Context, r HistoryRange) ([]HistorySample, error)
func (s *HistoryService) Summary(ctx context.Context) (HistorySummary, error)   // existing signature
func (s *HistoryService) SetEnabled(ctx context.Context, enabled bool) error   // existing
func (s *HistoryService) Clear(ctx context.Context) error                      // existing
func (s *HistoryService) Export(ctx context.Context, format string) (string, error) // existing

// gui/services/security.go (new)
type SecurityService struct{}
func (s *SecurityService) Grants(ctx context.Context) (api.AccessGrantsView, error)
func (s *SecurityService) GrantUser(ctx context.Context, uid int, role string) error
func (s *SecurityService) SetOwner(ctx context.Context, user string) error
func (s *SecurityService) RevokeUser(ctx context.Context, identity string, force bool) (app.RevokeOutcome, error)

// gui/services/identity.go (new)
type IdentityService struct{}
type OfflineIdentity struct { PeerID string `json:"peer_id"`; PublicKey string `json:"public_key"`; IdentityPath string `json:"identity_path"` }
func (s *IdentityService) Public(ctx context.Context) (api.Identity, error)
func (s *IdentityService) GenerateOffline(ctx context.Context, targetDir string) (OfflineIdentity, error)

// gui/services/network.go (add)
type PingResult struct { Reachable bool `json:"reachable"`; Output string `json:"output"` }
func (s *NetworkService) Ping(ctx context.Context, ip string) (PingResult, error)

// gui/services/diagnostics.go (add)
func (s *DiagnosticsService) ExportBundle(ctx context.Context) (string, error)

// internal/api/types_gui.go (add)
type CallerSecurity struct { ID string `json:"id"`; Transport string `json:"transport"`; Role string `json:"role"`; IsRoot bool `json:"is_root"` }
type GrantRecord struct { Identity string `json:"identity"`; Role string `json:"role"` }
type AccessGrantsView struct { Caller CallerSecurity `json:"caller"`; Grants []GrantRecord `json:"grants"` }

// internal/api/types.go — Peer additions
SessionStartMS  *int64 `json:"session_start_ms,omitempty"`
LastHandshakeMS *int64 `json:"last_handshake_ms,omitempty"`

// internal/api/types_system.go — UpdateCheckResult addition
ReleaseNotes string `json:"release_notes,omitempty"`
```

---

### Task A: pure mesh model + history math (frontend foundation)

**Files:** create `features/mesh/model.ts`, `model.test.ts`, `vitest.config.ts`; modify `package.json` (devDep `vitest`, script `"test": "vitest run"`).

**Interfaces:** produces every type/function in the “Shared interfaces” TS block.

- [ ] **Step 1: add vitest and write failing tests** covering:
  - `computeRate`: steady counters → rate; `dt <= 0` → null; `next.uptime < prev.uptime` → null; counter rollback → null; first sample → null.
  - `activityWindow`: same origin, `next.seq === prev.seq + 1`, `0 < dt <= 10_000` → bytes/dt; `seq` gap → null; `dt > 10_000` → null; non-increasing `observed_ms` → null.
  - `buildMesh`: no edge from a peer with `direct=false` and no route/activity; direct up peer → `direct`; route path `[a,b]` → edges self→a (route unless direct) and a→b; intermediate `a` absent from peers gets `kind:"intermediate"`; activity `{origin:self,path:[dest]}` → edge self→dest `observed`; activity `{origin:a,path:[a,b]}` → a→b; expired activity adds no edge; stale flag when peers query older than 15 s.
  - `dedupeActivities`: same `(origin,destination,seq)` collapses to newest.
  - `bucketActivities`: two samples in the same 60 s bucket sum bytes and keep latest RTT.
  - `presenceOf`: fresh traffic → active; `state==="up"` with no traffic → idle; `state!=="up"` → offline; no peer details → unknown.
  - `peerHealth`: up/direct, up/route, down with last-seen, unknown.

Run: `cd gui/frontend && npx vitest run src/features/mesh/model.test.ts` → expect failures.

- [ ] **Step 2: implement `model.ts`** with exactly the signatures above. Rules:
  - `buildMesh` dedupes edges by `${from}>${to}` with precedence `direct > observed > route`; `fresh` is true for direct/observed, false for route-only.
  - Route edges use consecutive pairs of `[self, ...route.path]`; the destination node is always present.
  - Activity edges: normalize `[origin, ...path]` (drop a duplicated leading origin), then consecutive pairs.
  - `activityWindow` uses `StateInterval = 5000` ms as the nominal cadence and accepts only `seq` delta 1 with `0 < dt <= 2 * 5000`.
  - `formatRate` → `"—"` for null, `B/s`, `kB/s`, `MB/s` with one decimal.
- [ ] **Step 3: run the tests** → all pass. `npx tsc --noEmit` → exit 0.
- [ ] **Step 4: commit** `feat(mesh): add pure mesh and history model with tests`.

---

### Task B: event-first data layer

**Files:** modify `gui/services/events.go`; create `features/mesh/useDaemonEvents.ts`, `useMesh.ts`, `useSessionHistory.ts`; modify `components/layout/AppShell.tsx` (mount once), `lib/api.ts` (exports).

**Interfaces:** consumes Task A; produces `useMesh`, `useDaemonEvents`, `useSessionHistory`.

- [ ] **Step 1: forward lifecycle events in Go.** In `emitEvent`, add a default branch for the daemon's `api.Event` envelope types (`peer_up`, `peer_joined`, `peer_discovered`, `peer_added`, `peer_removed`, `peer:leave`, `peer:rejoined`, `name_changed`, `secret_rotated`, `system_reset`, `system_imported`, `leave_dataplane_stop_failed`) and emit them as `app.Event.Emit(name, v)`. Do **not** wire `update:available` (no producer exists) and do not invent `routes_changed`.
- [ ] **Step 2: `useDaemonEvents`** mounted once in `AppShell`:
  - `peers:changed` → `qc.setQueryData(["peers"], payload)`; `routes:changed` → set routes; `service:changed` → set service; `connection:changed` → patch `status.connection`; `peer:probe` → update a probe-state store keyed by peer id; `log:entry` → append to a bounded log store; `activity:sample` → append to a bounded activity store (dedupe by origin+destination+seq) and refresh `lastTrafficAt`.
  - lifecycle events → append to the session event store (`features/notifications/store.ts`, bounded 200, dedupe by `type|peer_id|time_ms`), toast only for `peer:leave`, `peer:rejoined`, `secret_rotated`, `system_reset`, `system_imported`, `leave_dataplane_stop_failed`; invalidate `peers`/`status` on peer lifecycle.
  - Return unsubscribes; no per-route subscriptions.
- [ ] **Step 3: `useSessionHistory`** — 120-sample ring in zustand (not persisted), reset on daemon restart (`uptime` decrease) or node id change.
- [ ] **Step 4: `useMesh`** — initial `status`/`peers`/`routes`/`activities` queries (staleTime 5 s, no interval), merge with the stores, compute `model` with `buildMesh`, compute total/peer rates from a `useLiveSampler` hook:
  - live sampler active only when a live surface is mounted and `document.visibilityState === "visible"`; 2 s interval; `Promise.all([Status(), List()])`; write caches; `computeRate` against the previous sample; push to `useSessionHistory`; when `HistoryService.Summary().Enabled`, call `HistoryService.Record`.
  - a separate 5 s sampler runs when history is enabled even while hidden; it stops when the window is destroyed.
  - reconciliation: one 30 s refetch of `status`/`peers`/`routes` while visible; none while hidden.
  - `staleSince` = time of the last successful snapshot; error keeps last-known data.
- [ ] **Step 5: run** `npx tsc --noEmit` and `npm test` → exit 0. Commit `feat(mesh): event-first data layer and shared model hook`.

---

### Task C: mesh surface, routes drawer and inspectors

**Files:** create `features/mesh/MeshCanvas.tsx`, `MeshTable.tsx`, `RouteDrawer.tsx`, `PeerInspector.tsx`, `LinkInspector.tsx`, `PeerHealth.tsx`, `ThroughputStrip.tsx`, `EventStrip.tsx`; rewrite `routes/overview/Overview.tsx`, `routes/network/Network.tsx`, `routes/peers/Peers.tsx`; delete `features/topology/Topology.tsx`.

**Interfaces:** consumes `useMesh`, `model`; consumes `PeerService.Probe`, `NetworkService.Ping`, `PeerService.LeaveNetwork`.

- [ ] **Step 1: canvas.** @xyflow/react with fixed positions: self at center, direct peers on an inner ring, route-only peers on an outer ring ordered by path length then id; nodes keyed by id; `nodesDraggable={false}`, `fitView`, no physics. Node shows label, overlay, state dot, RTT, transport. Edge styles: `direct` solid accent; `observed` dashed accent with flow animation only when a fresh rate > 0; `route` dotted neutral. Use `prefers-reduced-motion` + `useUI().motion` to disable animation and show static arrows.
- [ ] **Step 2: accessible table.** `MeshTable` renders the same `model` as an ordered list/table: “This device → <peer>, direct/route, <rtt>, <rate>” plus intermediate rows. A visible “Map / Table” switch swaps the two; the canvas is `aria-hidden` only when the table is shown (never hide focusable controls). Keyboard: arrows move selection, Enter opens inspector, Escape clears.
- [ ] **Step 3: inspector.** `PeerInspector` (aside at ≥1100 px, Dialog at 900×600) shows identity, overlay, state, endpoint/candidates, transport/RX, last-seen age, session start/last handshake (or “not observed”), counters/rates, routes via peer, **Ping**, **Probe**, **View routes**, **Leave network** (self only, existing confirm). `LinkInspector` shows origin→destination, evidence, transport, RTT/jitter, rate window or “window unknown”, evidence age, and the owning route.
- [ ] **Step 4: overview.** Header: membership/service/connectivity as separate facts, peers up/total, capacity “n of 5”, uptime, stale banner when `staleSince` is old. Main: canvas + route drawer toggle; `ThroughputStrip` (last 60 samples, gap-aware); `EventStrip` (session events, deep links).
- [ ] **Step 5: network route table.** Sortable/searchable table from `model.paths`: destination, next hop, cost, hops, full path; row click selects the destination peer and highlights its path on the map; `?peer=` deep link.
- [ ] **Step 6: peers.** Sortable table (name, presence, path, transport, RTT, rate, last seen), keyboard rows, `?peer=` selection; empty state with an invite action; no “remove peer” control.
- [ ] **Step 7: run** `npx tsc --noEmit`, `npm test`, `npm run build`. Commit `feat(mesh): mesh canvas, routes drawer, peer and link inspectors`.

---

### Task D: bounded history service (Go) and activity UI

**Files:** rewrite `gui/services/history.go`; create `features/activity/Timeline.tsx`; rewrite `routes/activity/Activity.tsx`.

**Interfaces:** `HistoryService` five methods as declared; `HistorySample` type.

- [ ] **Step 1: failing Go tests** (`gui/services/history_test.go`, use a temp `UserConfigDir`):
  - disabled by default; `Record` is a no-op while disabled;
  - enabling persists across a new service instance (settings file);
  - `Record` appends one JSONL line; `Samples` filters by `PeerID`/`Minutes` and returns newest first;
  - retention drops samples older than 24 h and enforces a 50 MB cap (write a synthetic oversize file);
  - `Clear` empties the store; `Export("json")` writes valid JSON and `Export("csv")` a header+rows;
  - `Export` cancelled dialog → error, no file.
  Run `go test ./gui/services -run TestHistory -v` → fail.
- [ ] **Step 2: implement** with `os.UserConfigDir()/skein-desktop/history/` (`settings.json`, `history.jsonl`), `O_APPEND` line writes, atomic settings writes, time+size trimming, and `application.Get().Dialog.SaveFile().SetFilename(...).AddFilter(...).PromptForSingleSelection()` for export. Reject export when no Wails app (server mode) with a clear error.
- [ ] **Step 3: run tests** → pass; `go vet ./gui/services`.
- [ ] **Step 4: Activity UI** — two panes: session event feed (from `useDaemonEvents`) and measured history. Live chart from `useSessionHistory`; history chart from `HistoryService.Samples` with an explicit “this app, this device” label and collection gaps. Controls: enable/disable (explanation + privacy copy), clear, export JSON/CSV, filters peer/direct/transport backed by URL params. Expired live evidence is labelled, not deleted from history.
- [ ] **Step 5: commit** `feat(history): implement bounded local history and activity surface`.

---

### Task E: security read contract, grants, revoke, identity

**Files:** create `internal/api/handlers_security.go`, `gui/services/security.go`, `gui/services/identity.go`; modify `internal/api/types_gui.go`, `routes.go`, `internal/daemon/security.go`, `gui/services/services.go`, `routes/security/Security.tsx`.

**Interfaces:** `AccessGrantsView`, `CallerSecurity`, `GrantRecord`, `SecurityService` methods, `IdentityService` methods.

- [ ] **Step 1: failing API tests** (`internal/api/authz_test.go`):
  - kernel caller with a `secret` grant → 200 and the full grant list;
  - bearer caller with a valid token → 403 (secret requires kernel);
  - caller fields report identity, transport, role, root/SYSTEM;
  - no secrets/private keys in the payload.
- [ ] **Step 2: implement** `GET /api/v1/security/grants` with `wrapRole(RoleSecret, ...)`; read the caller via `CallerFrom`; build `Grants` from the server's parsed identity→role map (the same map `roleFor` uses). Keep `/security` unchanged for backward compatibility.
- [ ] **Step 3: Go service wrappers** — `Grants` calls the endpoint; `GrantUser`/`SetOwner`/`RevokeUser` call the existing fixed helper verbs through `runHelper`/`runHelperJSON` and then `UpdateService.ReloadGrants()` where the verb does not reload itself. Platform split with build tags: Linux `grant-user <uid> --role <role>`; Windows `set-owner <user>`; revoke on both. Preserve last-grant/last-secret protection: surface the helper error verbatim; `--force` only after an explicit confirm naming the consequence.
- [ ] **Step 4: offline identity** — `Public()` binds `/identity`. `GenerateOffline(targetDir)`:
  - require absolute, clean, existing user-owned directory; refuse `/etc/skein`, `/usr`, `/var`, `C:\Program Files\Skein`, `C:\ProgramData\Skein` and any path inside them;
  - refuse if `targetDir/skein.toml` exists (not a fresh artifact directory);
  - reuse `identity.Load` when `identity.key` exists (return public data only);
  - otherwise `identity.Generate()` + `configstore.Open(targetDir/skein.toml)` + `WriteIdentity` (no-overwrite, 0600);
  - never return or log the private key; never write system config; never call a helper.
- [ ] **Step 5: tests** — no-overwrite, locked config → `configstore.ErrLocked`, system path refusal, existing key reuse. Run `go test ./internal/api ./gui/services`.
- [ ] **Step 6: Security UI** — caller role/transport banner; effective grants table with role badges; grant dialog (uid/SID + role) with elevation guidance; revoke dialog (force only after naming the last-grant/last-secret consequence); set-owner on Windows; firewall status/repair; rotation with “all peers must rejoin” confirm; node identity with copy and offline generation dialog (target directory picker, explanation that a live node’s identity cannot be regenerated without reset). No terminal instructions.
- [ ] **Step 7: commit** `feat(security): grants read contract, helper access actions and offline identity`.

---

### Task F: ping, diagnostics export, update notes and session timestamps

**Files:** modify `gui/services/network.go`, `diagnostics.go`, `internal/api/types.go`, `types_system.go`, `internal/peer/table.go`, `internal/dataplane/engine.go`, `internal/daemon/daemon.go`, `internal/daemon/update.go`, `internal/daemon/security.go` (no change expected), `routes/logs/Logs.tsx`, `routes/about/About.tsx`, `README.md` (manifest fields).

- [ ] **Step 1: ping** — `NetworkService.Ping(ctx, ip)`:
  - `netip.ParseAddr`; reject anything else;
  - `exec.CommandContext` with fixed argv `ping -c 4 <ip>` (`-n 4` on Windows), 15 s context;
  - cap combined output at 4 KiB; `Reachable = exit 0`; a non-zero ping exit is a result, not an error; spawn/validation failure is an error;
  - never accept a hostname or extra flags.
  Tests: invalid input, fake command path (var for tests), output cap.
- [ ] **Step 2: diagnostics export** — `ExportBundle` fetches the existing bundle, marshals indented JSON, opens the native Save dialog (`skein-diagnostics-<timestamp>.json`), writes 0600, returns the path. Tests: cancelled dialog, write failure, content contains `redactions`.
- [ ] **Step 3: session timestamps** — `peer.Peer` gains `SessionStart`/`LastHandshake`; set `SessionStart` only on transition to up, update `LastHandshake` on every installed handshake; clear both on down/leave/drop. `api.Peer` gains nullable `*int64` epoch-ms fields; `Daemon.Peers()` projects them. Tests: first up sets start, rekey updates handshake only, down clears, reconnect sets a new start; JSON omits unknown.
- [ ] **Step 4: release notes** — `UpdateManifest` gains `ReleaseNotes` and `NotesSignature`; `SignedPayload()` stays the legacy five lines; `NotesPayload()` is `sha256(ReleaseNotes)` hex. Verify notes only when present: require `NotesSignature`, cap 64 KiB, reject tampering. `UpdateCheckResult.ReleaseNotes` carries the verified text. Tests: legacy manifest still verifies; valid notes verify and return; missing/invalid notes signature rejects; oversize rejects. Document the two new manifest fields in the README update section.
- [ ] **Step 5: Logs UI** — virtualized/bounded tail from the log store + paged `Logs(since, limit)` using the returned `next` cursor, level filter, pause/follow, copy line, **Save diagnostic bundle** button, and a troubleshooter panel that runs probe/ping/route checks for a selected peer and states only observed facts.
- [ ] **Step 6: About UI** — GUI/daemon versions, protocol, platform, capabilities, staged manifest path + binary path, **Check** (shows verified version + release notes + last outcome), **Apply** with confirm, poll `CheckManifest` until `LastUpdate` is applied/rolled back, then compare `Status().Version` to the applied version and report the actual running version. No release feed, no auto-check.
- [ ] **Step 7: commit** `feat(operator): ping, diagnostic export, update notes and session timestamps`.

---

### Task G: settings, recovery and onboarding

**Files:** rewrite `routes/settings/Settings.tsx`, `routes/settings/NetworkRecovery.tsx`, `routes/onboarding/Onboarding.tsx`; modify `features/notifications/store.ts`.

- [ ] **Step 1: Settings** — node form (name/port/MTU/mDNS) with dirty-state protection from incoming snapshots, apply via `ConfigService.Patch`, show `RestartRequired`/`Applied`; read-only overlay prefixes/interface/STUN/config/socket paths; service card using the verified lifecycle methods with elevation guidance and authoritative state after each action; links to Security/Diagnostics; theme/motion controls (existing store).
- [ ] **Step 2: recovery** — rejoin (same network, confirm), switch network wizard (leave → reset with backup/purge + keep identity → verified Stop → create/join/import), partial completion keeps the returned backup path and offers **Retry stop** only; rejoin success copy says “Membership restored; waiting for peers” until a peer is observed up (tombstone note, up to 180 s). Never show a raw endpoint.
- [ ] **Step 3: onboarding** — welcome → create/join/import → working → done; each elevation outcome (cancelled, failed, service not ready) has one action; bootstrap secret shown once with copy, never persisted; “what happens” summary before the privileged step.
- [ ] **Step 4: tests/checks** — `npx tsc --noEmit`, `npm test`, `npm run build`; existing fail-closed fixture runs for destructive flows.
- [ ] **Step 5: commit** `feat(settings): complete settings, recovery and onboarding flows`.

---

### Task H: palette, notifications, shell and audit

**Files:** rewrite `components/layout/CommandPalette.tsx`; modify `CommandBar.tsx`, `Sidebar.tsx`, `AppShell.tsx`; add notification popover.

- [ ] **Step 1: palette** — navigation, peer selection, actions (probe, ping, connect/disconnect, service start/stop/restart, generate invite, rotate, open logs, reset), fuzzy search over peers/routes/logs; combobox semantics (`aria-activedescendant`, arrow/Enter/Escape); destructive actions open the same confirms.
- [ ] **Step 2: notifications** — SSE-driven toasts with one action; persistent session popover (bounded 200); no invented severity; role/service gating matches the pages.
- [ ] **Step 3: shell** — mount `useDaemonEvents` once; separate membership/service/connectivity banners with one action each; keep Settings/Diagnostics reachable while stopped; 900×600 layout without overlap; focus-visible and reduced-motion checks.
- [ ] **Step 4: audit** — 900×600 and 1280×800 screenshots in all retained themes; keyboard-only pass; canvas/table equivalence; reduced-motion pass; event-burst performance (no per-frame React state, no layout thrash); contrast spot checks; delete `features/topology/Topology.tsx` and any unused imports.
- [ ] **Step 5: commit** `feat(shell): palette, notifications and accessibility audit`.

---

### Task I: integration, verification and delivery

**Files:** docs and evidence only; regenerate bindings.

- [ ] **Step 1: regenerate bindings** from `gui/` with the project's task (`task generate:bindings` or the same `wails3 generate bindings -clean=true -ts -i` used by the build), then record the actual inventory (services/methods/models). Confirm: History 5 methods, Security 4, Identity 2, Network +Ping, Diagnostics +ExportBundle; Setup unchanged at 5.
- [ ] **Step 2: gates** — `go build ./...`, `go vet ./...`, `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`, targeted unprivileged tests, full Go suite in an isolated user/mount/network namespace with private `/etc/skein` and `/run`, `bash scripts/collapse-gates.sh`, `bash scripts/packaging-assert.sh`, frontend `npm test`, `npx tsc --noEmit`, `npm run build`, Linux `wails3 build`.
- [ ] **Step 3: runtime** — real GUI against a disposable daemon fixture for read paths; intercepted fail-closed bindings for destructive flows; native Linux smoke on Xvfb/real seat; authorized Windows smoke for Windows-specific changes; capture screenshots and state exact coverage. Re-provision targets first; do not touch the daily-use node.
- [ ] **Step 4: evidence** — update `docs/engineering/GUI-REDESIGN-STATUS.md` and `~/workspace/skein-work/BUGS.md` with VERIFIED/PARTIALLY VERIFIED/BLOCKED/FAILED and exact scope; note the deferred invitation features and the carried tombstone/peer-list observations; `graphify update .`.
- [ ] **Step 5: commit** `docs: record redesign implementation evidence` (no push).

---

## Self-review notes

- Every spec section maps to a task: visual/IA (C, G, H), event/data path (B), telemetry invariants (A), map/inspectors (C), events/history (D), parity (E, F), invitations/identity (E, G), recovery (G), health (F), a11y/performance (H), verification (I).
- The owner-approved invitation deferral is the only reduction; no task implements advisory expiry or usage tracking.
- Contracts are split between existing symbols and explicitly proposed ones; implementation must verify each proposed symbol before use.
- History keeps its five existing method names; only `Record` is added, so bindings change is additive.
