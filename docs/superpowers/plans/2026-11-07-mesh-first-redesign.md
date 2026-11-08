# Mesh-first redesign — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans`. Steps use `- [ ]`. Do not run full builds, suites, linters or formatters mid-task; run only the task's covering tests. The controller runs each batch gate once. Never push.

**Goal:** Replace the card dashboard and star topology with one derived mesh model, honest per-link facts, event-first updates, bounded local history, real recovery, and full operator parity (ping, key generation, access/roles/revoke, diagnostics export, updates with notes).

**Architecture:** Pure model functions (`features/mesh/model.ts`) + one hook (`useMesh`) + one event hook (`useDaemonEvents`) feed every surface. Go contracts land before the UI consumes them: lifecycle forwarding in the existing event bridge, a bounded GUI-owned history store, a secret-gated grants read endpoint, fixed-helper wrappers, offline identity generation, bounded overlay ping, native diagnostic export, session timestamps, and signed release notes. Bindings regenerate once after the Go contract tasks.

**Tech stack:** Go 1.27, Wails v3.1.0, React 19, TypeScript, TanStack Query, @xyflow/react, Radix, Tailwind tokens, zustand, sonner, vitest (dev, installed).

**Source of truth:** `docs/superpowers/specs/2026-11-07-mesh-first-redesign-design.md` (approved 2026-11-07). The owner explicitly deferred **only** invitation expiry, active-invite inventory and invite-use attribution. F-01 per-peer revocation remains deferred. Do not add a fake expiry/list.

## Global constraints

- No invented data: edges only from `peer.direct && peer.state==="up"`, `routes[].path`, or fresh authenticated `activity` samples. Unknown ≠ zero. Missing telemetry shows “unknown”/“—”, never 0.
- `Peer.last_seen_ms` is elapsed age, not an epoch. Route `path` excludes self and ends at destination; `cost_ms` is whole-path cost.
- Activity `rx_bytes`/`tx_bytes` are deltas over an unexported window. A remote rate requires a proven window; otherwise show bytes with “window unknown”.
- One SSE subscription in Go; one event hook in the frontend. Typed snapshot events update the query cache directly. No 2-second polling of every endpoint. 30 s reconciliation only while visible.
- ADR-001: GUI unprivileged; daemon sole config writer; fixed helper verbs; verified socket/pipe identity; `secret` requires kernel transport. Never weaken these.
- Tokens only (`var(--…)`); reduced motion honoured; canvas has a table/list equivalent; all controls keyboard reachable.
- Destructive actions keep their existing confirms and partial-completion behaviour; never repeat a completed reset.
- No new runtime dependencies. `vitest` is a dev dependency (already installed); never run `npm install`.
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
| Go services | `gui/services/events.go`, `history.go`, `identity.go` (new), `security.go` (new), `network.go`, `diagnostics.go`, `services.go` |
| Go API | `internal/api/types_gui.go`, `types.go`, `types_system.go`, `routes.go`, `handlers_security.go` (existing), `internal/daemon/security.go`, `update.go`, `daemon.go`, `internal/peer/table.go`, `internal/dataplane/engine.go` |

## Shared interfaces (Task 1 produces; every later task consumes)

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
func (s *HistoryService) Summary(ctx context.Context) (HistorySummary, error)
func (s *HistoryService) SetEnabled(ctx context.Context, enabled bool) error
func (s *HistoryService) Clear(ctx context.Context) error
func (s *HistoryService) Export(ctx context.Context, format string) (string, error)

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

### Task 1: pure mesh model + history math (frontend foundation)

**Files:** create `features/mesh/model.ts`, `model.test.ts`, `vitest.config.ts`; modify `package.json` (script `"test": "vitest run"`; vitest already installed).

**Interfaces:** produces every type/function in the TS block above.

- [ ] **Step 1: write failing tests** covering:
  - `computeRate`: steady counters → rate; `dt <= 0` → null; `next.uptime < prev.uptime` → null; counter rollback → null; first sample → null.
  - `activityWindow`: same origin, `next.seq === prev.seq + 1`, `0 < dt <= 10_000` → bytes/dt; `seq` gap → null; `dt > 10_000` → null; non-increasing `observed_ms` → null.
  - `buildMesh`: no edge from a peer with `direct=false` and no route/activity; direct up peer → `direct`; route path `[a,b]` → edges self→a (route unless direct) and a→b; intermediate `a` absent from peers gets `kind:"intermediate"`; activity `{origin:self,path:[dest]}` → edge self→dest `observed`; activity `{origin:a,path:[a,b]}` → a→b; expired activity adds no edge; stale flag when the newest snapshot is older than 15 s.
  - `dedupeActivities`: same `(origin,destination,seq)` collapses to newest.
  - `bucketActivities`: two samples in the same 60 s bucket sum bytes and keep latest RTT.
  - `presenceOf`: fresh traffic → active; `state==="up"` with no traffic → idle; `state!=="up"` → offline; no peer details → unknown.
  - `peerHealth`: up/direct, up/route, down with last-seen, unknown.
  Run: `cd gui/frontend && npx vitest run src/features/mesh/model.test.ts` → expect failures.
- [ ] **Step 2: implement `model.ts`** with exactly those signatures. `buildMesh` dedupes edges by `${from}>${to}` with precedence `direct > observed > route`; `fresh` is true for direct/observed. Route edges use consecutive pairs of `[self, ...route.path]`. Activity edges normalize `[origin, ...path]`. `activityWindow` accepts only `seq` delta 1 with `0 < dt <= 10 000`.
- [ ] **Step 3: run the tests** → pass; `npx tsc --noEmit` → exit 0.
- [ ] **Step 4: commit** `feat(mesh): add pure mesh and history model with tests`.

---

### Task 2: event-first data layer (frontend, no new bindings)

**Files:** modify `gui/services/events.go`; create `features/mesh/useDaemonEvents.ts`, `useMesh.ts`, `useSessionHistory.ts`, `features/notifications/store.ts`; modify `components/layout/AppShell.tsx`, `lib/api.ts`.

**Interfaces:** consumes Task 1; produces `useMesh`, `useDaemonEvents`, `useSessionHistory`.

- [ ] **Step 1: forward lifecycle events in Go.** In `emitEvent`, add a default branch for `api.Event` envelope types (`peer_up`, `peer_joined`, `peer_discovered`, `peer_added`, `peer_removed`, `peer:leave`, `peer:rejoined`, `name_changed`, `secret_rotated`, `system_reset`, `system_imported`, `leave_dataplane_stop_failed`) and emit them as `app.Event.Emit(name, v)`. Do **not** wire `update:available` (no producer) and do not invent `routes_changed`.
- [ ] **Step 2: `useDaemonEvents`** mounted once in `AppShell`:
  - `peers:changed` → `qc.setQueryData(["peers"], payload)`; `routes:changed` → set routes; `service:changed` → set service; `connection:changed` → patch `status.connection`; `peer:probe` → probe store keyed by peer id; `log:entry` → bounded log store; `activity:sample` → bounded activity store (dedupe origin+destination+seq) and `lastTrafficAt`.
  - lifecycle events → session event store (`features/notifications/store.ts`, bounded 200, dedupe `type|peer_id|time_ms`); toast only for `peer:leave`, `peer:rejoined`, `secret_rotated`, `system_reset`, `system_imported`, `leave_dataplane_stop_failed`; invalidate `peers`/`status` on peer lifecycle.
  - No per-route subscriptions.
- [ ] **Step 3: `useSessionHistory`** — 120-sample ring in zustand (not persisted), reset on uptime decrease or node-id change.
- [ ] **Step 4: `useMesh` + `useLiveSampler`** — initial `status`/`peers`/`routes`/`activities` queries (staleTime 5 s, no interval); merge stores; compute `model`; total/peer rates from a 2 s sampler that runs only while a live surface is mounted and the document is visible; 30 s reconciliation only while visible; `staleSince` = last successful snapshot; errors keep last-known data. Do **not** import `HistoryService` here (Task 6 wires recording).
- [ ] **Step 5: run** `npx tsc --noEmit`, `npm test` → exit 0. Commit `feat(mesh): event-first data layer and shared model hook`.

---

### Task 3: Go contracts — history, ping, export, timestamps, release notes

**Files:** rewrite `gui/services/history.go`; modify `gui/services/network.go`, `diagnostics.go`; `internal/api/types.go`, `types_system.go`; `internal/peer/table.go`, `internal/dataplane/engine.go`, `internal/daemon/daemon.go`, `internal/daemon/update.go`; add tests; `README.md` (manifest fields).

**Interfaces:** `HistorySample`, `HistoryService.Record`, `PingResult`, `NetworkService.Ping`, `DiagnosticsService.ExportBundle`, `Peer` timestamp fields, `UpdateCheckResult.ReleaseNotes`.

- [ ] **Step 1: history tests** (`gui/services/history_test.go`, temp `UserConfigDir`): disabled by default; `Record` no-op while disabled; enable persists across instances; append/read/filter; 24 h and 50 MB retention; `Clear`; `Export("json")`/`Export("csv")` contents; cancelled dialog → error, no file. Run `go test ./gui/services -run TestHistory -v` → fail.
- [ ] **Step 2: implement history** with `os.UserConfigDir()/skein-desktop/history/` (`settings.json`, `history.jsonl`), `O_APPEND` line writes, atomic settings writes, time+size trimming, and `application.Get().Dialog.SaveFile().SetFilename(...).AddFilter(...).PromptForSingleSelection()` for export. Server mode (no app) → clear error.
- [ ] **Step 3: ping** — `NetworkService.Ping(ctx, ip)`: `netip.ParseAddr`; fixed argv `ping -c 4 <ip>` (`-n 4` on Windows), 15 s context; cap combined output at 4 KiB; `Reachable = exit 0`; non-zero ping exit is a result, not an error; spawn/validation failure is an error; never accept a hostname or extra flags. Tests: invalid input, fake command var, output cap.
- [ ] **Step 4: diagnostics export** — fetch bundle, marshal indented JSON, native Save dialog (`skein-diagnostics-<timestamp>.json`), write 0600, return path. Tests: cancelled dialog, write failure, `redactions` present.
- [ ] **Step 5: session timestamps** — `peer.Peer` gains `SessionStart`/`LastHandshake`; set start only on transition to up, update handshake on every installed handshake, clear both on down/leave/drop; `api.Peer` gains nullable epoch-ms fields; `Daemon.Peers()` projects them. Tests: first up, rekey, down, reconnect; unknown omits JSON.
- [ ] **Step 6: release notes** — `UpdateManifest` gains `ReleaseNotes` and `NotesSignature`; `SignedPayload()` stays the legacy five lines; `NotesPayload()` is `sha256(ReleaseNotes)` hex; verify notes only when present (signature required, 64 KiB cap, tamper rejection); `UpdateCheckResult.ReleaseNotes` carries verified text. Tests: legacy verifies; valid notes verify; missing/invalid signature rejects; oversize rejects. Document the fields in the README update section.
- [ ] **Step 7: run** `go test ./gui/services ./internal/api ./internal/daemon ./internal/peer` (covering tests only) and `go vet ./...`. Commit `feat(operator): history store, ping, export, timestamps and signed notes`.

---

### Task 4: Go contracts — security grants, helper wrappers, offline identity

**Files:** modify `internal/api/handlers_security.go`; create `gui/services/security.go`, `gui/services/identity.go`; modify `internal/api/types_gui.go`, `routes.go`, `internal/daemon/security.go`, `gui/services/services.go`; tests.

**Interfaces:** `AccessGrantsView`, `CallerSecurity`, `GrantRecord`, `SecurityService`, `IdentityService`.

- [ ] **Step 1: failing API tests** (`internal/api/authz_test.go`): kernel caller with `secret` → 200 and full grants; bearer with valid token → 403; caller fields (identity, transport, role, root/SYSTEM); no secrets/private keys.
- [ ] **Step 2: implement** `GET /api/v1/security/grants` with `wrapRole(RoleSecret, ...)`; caller via `CallerFrom`; grants from the same parsed identity→role map `roleFor` uses. Keep `/security` unchanged.
- [ ] **Step 3: service wrappers** — `Grants` calls the endpoint; `GrantUser`/`SetOwner`/`RevokeUser` call the existing fixed helper verbs via `runHelper`/`runHelperJSON`, then `UpdateService.ReloadGrants()` where needed. Linux `grant-user <uid> --role <role>`; Windows `set-owner <user>`; revoke on both. Surface helper errors verbatim; `--force` only after explicit confirm naming the consequence.
- [ ] **Step 4: offline identity** — `Public()` binds `/identity`. `GenerateOffline(targetDir)`: absolute clean existing user-owned dir; refuse `/etc/skein`, `/usr`, `/var`, `C:\Program Files\Skein`, `C:\ProgramData\Skein` and descendants; refuse if `skein.toml` exists; reuse existing `identity.key` (public data only); else `identity.Generate()` + `configstore.Open` + `WriteIdentity`; never return/log the private key; never write system config or call a helper.
- [ ] **Step 5: tests** — no-overwrite, locked config → `configstore.ErrLocked`, system path refusal, existing key reuse. `go test ./internal/api ./gui/services`.
- [ ] **Step 6: register services** in `services.go`; commit `feat(security): grants read contract, helper actions and offline identity`.

**Batch gate (controller):** after Task 4, regenerate bindings from `gui/` with the project's command, commit the regenerated bindings, and record the inventory (History 6 incl. `Record`; Security 4; Identity 2; Network +Ping; Diagnostics +ExportBundle). Later tasks consume these bindings.

---

### Task 5: mesh surface, routes drawer and inspectors

**Files:** create `features/mesh/MeshCanvas.tsx`, `MeshTable.tsx`, `RouteDrawer.tsx`, `PeerInspector.tsx`, `LinkInspector.tsx`, `PeerHealth.tsx`, `ThroughputStrip.tsx`, `EventStrip.tsx`; rewrite `routes/overview/Overview.tsx`, `routes/network/Network.tsx`, `routes/peers/Peers.tsx`; delete `features/topology/Topology.tsx`.

- [ ] **Step 1: canvas** — @xyflow/react, fixed positions (self center; direct peers inner ring; route-only peers outer ring ordered by path length then id); `nodesDraggable={false}`, `fitView`; node shows label/overlay/state/RTT/transport; edge styles `direct` solid, `observed` dashed with flow only when fresh rate > 0, `route` dotted neutral; reduced motion → static arrows.
- [ ] **Step 2: accessible table** — same model as an ordered list/table; visible Map/Table switch; canvas `aria-hidden` only when the table is shown; keyboard arrows/Enter/Escape.
- [ ] **Step 3: inspectors** — peer inspector (aside ≥1100 px, Dialog at 900×600): identity, overlay, state, endpoints/candidates, transport/RX, last-seen age, session start/last handshake (or “not observed”), counters/rates, routes via peer, Ping, Probe, View routes, Leave network (self only, existing confirm). Link inspector: origin→destination, evidence, transport, RTT/jitter, rate window or “window unknown”, evidence age, owning route.
- [ ] **Step 4: overview** — separate membership/service/connectivity facts, peers up/total, capacity “n of 5”, uptime, stale banner; canvas + route drawer; throughput strip (last 60, gap-aware); event strip with deep links.
- [ ] **Step 5: network route table** — sortable/searchable from `model.paths` (destination, next hop, cost, hops, full path); row selects the destination peer and highlights the path; `?peer=`.
- [ ] **Step 6: peers** — sortable table (name, presence, path, transport, RTT, rate, last seen), keyboard rows, `?peer=`, empty state with invite action, no remove-peer control.
- [ ] **Step 7: run** `npx tsc --noEmit`, `npm test`, `npm run build`. Commit `feat(mesh): mesh canvas, routes drawer, peer and link inspectors`.

---

### Task 6: activity surface and history recording

**Files:** create `features/activity/Timeline.tsx`; rewrite `routes/activity/Activity.tsx`.

- [ ] **Step 1: recording** — an effect reads `useSessionHistory` samples and, when `HistoryService.Summary().Enabled`, calls `HistoryService.Record` with the current counters/node id (one writer; no per-component duplication). Reset the history baseline on node change or uptime decrease.
- [ ] **Step 2: timeline** — session event feed (from `useDaemonEvents`) plus measured history; live chart from `useSessionHistory`; history chart from `HistoryService.Samples` labelled “this app, this device” with collection gaps; filters peer/direct/transport backed by URL params; expired live evidence labelled, not deleted from history.
- [ ] **Step 3: controls** — enable/disable with privacy copy, clear, export JSON/CSV, and an explicit note that collection runs only while this app is running.
- [ ] **Step 4: run** `npx tsc --noEmit`, `npm test`, `npm run build`. Commit `feat(activity): bounded history recording and activity surface`.

---

### Task 7: security, invitations and identity UI

**Files:** rewrite `routes/security/Security.tsx`, `routes/invites/Invites.tsx`; create `features/security/AccessPanel.tsx`, `IdentityPanel.tsx`.

- [ ] **Step 1: security** — caller role/transport banner; effective grants table with role badges; grant dialog (uid/SID + role) with elevation guidance; revoke dialog (`--force` only after naming last-grant/last-secret consequence); set-owner on Windows; firewall status/repair; rotation with “all peers must rejoin” confirm.
- [ ] **Step 2: identity** — node identity with copy; offline generation dialog (target directory, explanation that a live node’s identity cannot be regenerated without reset, public output only). No terminal instructions.
- [ ] **Step 3: invitations** — network vs peer introduction, QR/copy, snapshot/security warnings, rotation entry; explicit “no enforced expiry/inventory/attribution in this release” statement (owner-approved deferral); never persist invite plaintext.
- [ ] **Step 4: run** `npx tsc --noEmit`, `npm test`, `npm run build`. Commit `feat(security): access, identity and invitation surfaces`.

---

### Task 8: settings, recovery and onboarding

**Files:** rewrite `routes/settings/Settings.tsx`, `routes/settings/NetworkRecovery.tsx`, `routes/onboarding/Onboarding.tsx`.

- [ ] **Step 1: settings** — node form (name/port/MTU/mDNS) with dirty-state protection, apply via `ConfigService.Patch`, show `RestartRequired`/`Applied`; read-only effective config; service card with verified lifecycle and elevation guidance; links to Security/Diagnostics; theme/motion controls.
- [ ] **Step 2: recovery** — rejoin (same network, confirm); switch wizard (leave → reset with backup/purge + keep identity → verified Stop → create/join/import); partial completion keeps the backup path and offers **Retry stop** only; rejoin copy says “Membership restored; waiting for peers” until a peer is observed up (tombstone note, up to 180 s); never a raw endpoint.
- [ ] **Step 3: onboarding** — welcome → create/join/import → working → done; each elevation outcome has one action; bootstrap secret shown once, never persisted.
- [ ] **Step 4: run** `npx tsc --noEmit`, `npm test`, `npm run build`. Commit `feat(settings): complete settings, recovery and onboarding flows`.

---

### Task 9: diagnostics, updates, palette, notifications, shell, audit

**Files:** rewrite `routes/logs/Logs.tsx`, `routes/about/About.tsx`, `components/layout/CommandPalette.tsx`; modify `CommandBar.tsx`, `Sidebar.tsx`, `AppShell.tsx`; add notification popover.

- [ ] **Step 1: diagnostics** — virtualized/bounded tail from the log store + paged `Logs(since, limit)` using `next`; level filter, pause/follow, copy line, Save diagnostic bundle; troubleshooter running probe/ping/route checks for a selected peer with observed facts only.
- [ ] **Step 2: about** — GUI/daemon versions, protocol, platform, capabilities; staged manifest/binary paths; Check (verified version + release notes + last outcome); Apply with confirm; poll `CheckManifest` until applied/rolled back; compare `Status().Version` to the applied version and report the actual running version; no feed, no auto-check.
- [ ] **Step 3: palette** — navigation, peer selection, actions (probe, ping, connect/disconnect, service start/stop/restart, generate invite, rotate, open logs, reset), fuzzy search over peers/routes/logs; combobox semantics; destructive actions reuse confirms.
- [ ] **Step 4: notifications/shell** — SSE-driven toasts with one action; persistent session popover (bounded 200); mount `useDaemonEvents` once; separate membership/service/connectivity banners; Settings/Diagnostics reachable while stopped; 900×600 layout.
- [ ] **Step 5: audit** — 900×600 and 1280×800 screenshots in retained themes; keyboard-only pass; canvas/table equivalence; reduced motion; event-burst performance; contrast spot checks; delete `features/topology/Topology.tsx` and unused imports.
- [ ] **Step 6: run** `npx tsc --noEmit`, `npm test`, `npm run build`. Commit `feat(shell): diagnostics, updates, palette and accessibility audit`.

---

### Task 10: integration, verification and delivery

- [ ] **Step 1: bindings inventory** — confirm the post-Task-4 regeneration is committed; record actual services/methods/models.
- [ ] **Step 2: gates** — `go build ./...`, `go vet ./...`, `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`, targeted unprivileged tests, full Go suite in an isolated user/mount/network namespace with private `/etc/skein` and `/run`, `bash scripts/collapse-gates.sh`, `bash scripts/packaging-assert.sh`, `npm test`, `npx tsc --noEmit`, `npm run build`, Linux `wails3 build`.
- [ ] **Step 3: runtime** — real GUI against a disposable daemon fixture for read paths; intercepted fail-closed bindings for destructive flows; native Linux smoke; authorized Windows smoke for Windows-specific changes; screenshots; exact coverage. Re-provision targets; do not touch the daily-use node.
- [ ] **Step 4: evidence** — update `docs/engineering/GUI-REDESIGN-STATUS.md` and `~/workspace/skein-work/BUGS.md` with VERIFIED/PARTIALLY VERIFIED/BLOCKED/FAILED and scope; note deferred invitations and carried observations; `graphify update .`.
- [ ] **Step 5: commit** `docs: record redesign implementation evidence` (no push).

---

## Self-review notes

- Every spec section maps to a task: visual/IA (5, 8, 9), event/data path (2), telemetry invariants (1), map/inspectors (5), events/history (6), parity contracts (3, 4), security/invitations (7), recovery (8), health (9), a11y/performance (9), verification (10).
- Go contracts (3, 4) land before the UI consumes them; one bindings regeneration gate sits between 4 and 5.
- The owner-approved invitation deferral is the only reduction; no task implements advisory expiry or usage tracking.
- History keeps its five existing method names; only `Record` is added, so the binding change is additive.
