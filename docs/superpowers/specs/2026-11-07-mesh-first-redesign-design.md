# Mesh-first desktop redesign — design

Date: 2026-11-07. Worktree: `GUI-redesign`.

**Status: design proposal, source-reviewed; awaiting owner review before implementation planning.** Phase 0 evidence remains in [GUI-REDESIGN-STATUS.md](../../engineering/GUI-REDESIGN-STATUS.md). This revises the existing draft rather than starting a second design. No application behavior has changed in Phase 1.

**Owner decision recorded in this conversation:** explicitly defer invitation expiry, active-invite inventory, and invite-use attribution. Network/peer invite generation, QR/copy, snapshot warnings and secret rotation remain required. No other feature is deferred by this decision. F-01 per-peer invitation revocation remains deferred; no membership/admission protocol redesign is authorized.

## 1. Goal and acceptance boundary

The home screen answers: **what can this device reach, what is moving through the mesh, what evidence supports that view, and what can I safely do next?** The map is the primary view, not a decoration beside dashboard cards.

Every existing CLI/operator capability remains reachable in the GUI, normally within two clicks to its surface; confirmations and required wizard steps are not bypassed to satisfy the click count. Missing bindings are work to implement, not a reason to send users to a terminal. Status, peers, routes, ping, probe, invites, key generation, name/config editing, init/join/import, self-leave, reset/purge/keep-identity, rotation, service lifecycle, version/update, firewall, diagnostics/logs and access/grants are covered below.

The daemon remains the source of network truth. Unknown observations remain unknown. Five nodes is the supported ceiling, including this device. No other-peer removal, fabricated reachability, invented NAT causes, or synthetic security assurances.

## 2. Approach selected

| Approach | Tradeoff | Decision |
|---|---|---|
| Mesh workspace with route drawer and peer/link inspectors | Requires a shared evidence model; keeps paths, traffic and actions together | Selected |
| Incremental dashboard-card refresh | Smaller presentation change, but routing and link details remain scattered | Rejected |
| Wizard-first application | Strong setup experience, weak everyday monitoring | Use only for onboarding/recovery |

Reuse React, TypeScript, TanStack Query, React Flow, Radix, the existing theme store, and Wails services. Do not add a graph engine, generic plugin architecture, new component library or second daemon client.

## 3. Visual direction and navigation

Dark neutral surfaces, restrained mesh accent, existing Geist/Geist Mono, token-based color, strong typographic hierarchy. Monospace is for addresses, identifiers and numbers, not all body copy. Avoid decorative grids, glowing status lights, gradient card collections and ambient animations. Existing light/high-contrast themes remain supported; contrast is an acceptance check, not assumed from token names.

At 1280×800: compact left navigation, status/action header, dominant map, contextual right inspector, and shallow throughput/recent-event strips. At 900×600: inspector becomes a drawer; routes remain one click away; controls do not overlap or require whole-window horizontal scrolling. Capacity and service/membership/connectivity are distinct labels, not one ambiguous green “Connected”.

Preserve the existing HashRouter paths; add selection/filter query parameters rather than a parallel navigation system.

| Path | Surface | Primary contents |
|---|---|---|
| `/` | Mesh | Map, routes drawer, peer/link inspector, current health, last 60 throughput samples, recent events |
| `/network` | Routes | Full searchable route table, same model and selection as the map |
| `/peers` | Peers | Presence, direction/rates, endpoints, ping, probe, route inspection, self-leave |
| `/activity` | Activity | Human event feed, throughput/RTT history, filters and history controls |
| `/invites` | Invitations | Network versus peer introduction, QR/copy, snapshot/security warnings |
| `/security` | Security | Identity/key generation, effective access/roles, grant/revoke/reload, rotation, firewall |
| `/logs` | Diagnostics | Live logs, troubleshooter, redacted diagnostic file export |
| `/settings` | Settings | Node form, effective configuration, service lifecycle, membership recovery, theme/motion |
| `/about` | About / Updates | Running versions, capabilities, signed staged update, release notes, apply outcome |

Map node selection opens the same peer inspector used by Peers. Link selection opens a link inspector. A route row highlights its complete path; its destination opens the matching peer. Palette results and notification actions use these same destinations and mutations. Security owns access/rotation; Settings and Invitations link to it rather than duplicating stateful implementations.

## 4. State and error design

| Observed state | Primary next action | Required distinction |
|---|---|---|
| No configured network | Create / Join, with Import alongside | Not inferred merely from an unreachable API |
| Configured service stopped | Start service | Retain Settings and Diagnostics |
| Service missing but existing configuration | Install/repair service | Do not overwrite configuration via bootstrap |
| Dataplane paused | Resume | Not a departure or loss of membership |
| Departed | Rejoin this network / Switch network | Never offer generic Connect as remediation |
| Configured, no peers | Invite first device | Local daemon health does not prove mesh connectivity |
| Service running, API unreachable | Retry connection / open diagnostics | Display last-known data with age, not fresh green state |
| Role denied | Open access management | Do not retry elevated network mutations or weaken IPC checks |
| Protocol mismatch | Open compatibility/version details | Do not interpret incompatible data as current state |

Each error states what failed, the known reason (or that the cause is unknown), and one primary action. Technical details are expandable/copyable and redacted. A configuration-lock error is not an unconditional instruction to stop the daemon: the daemon is the legitimate writer; retry/diagnose the competing operation first.

Every destructive confirmation names the affected device/network, what is removed, what is kept, backup behavior, and recovery limits. Show an actual returned backup path after completion, never a guessed path. Purge and rotation do not promise an automatic Undo. Preserve partial-reset recovery: reset complete plus Stop unconfirmed offers **Retry stop**, never another reset.

## 5. One event-first data path

One Go SSE subscription remains the verified daemon connection. The application shell owns its lifetime; individual routes do not subscribe independently. Extend the existing Wails bridge to forward the daemon's existing lifecycle envelopes; today its switch silently drops them. Preserve current typed events: `connection:changed`, `peers:changed`, `routes:changed`, `activity:sample`, `log:entry`, `peer:probe`, `service:changed`.

Typed full snapshots update TanStack Query caches directly. Discrete events become human events and invalidate only affected data. There is no `routes_changed` or `peer:discovered` contract: existing names include `routes:changed` and `peer_discovered`. Do not infer an update producer merely because an event constant exists.

Fetch initial snapshots once, reconcile after reconnect/focus and successful mutations, and use one low-frequency reconciliation timer (30 s while visible) because the stream is not a durable event log. Bound buffers, coalesce bursts and isolate subscriptions/selectors so a log entry does not rerender the map. A reconnect introduces an explicit observation gap; do not invent missed events. Snapshot/event ordering must prevent an older request overwriting a newer event.

Live total/peer byte rates need timed counters: one shared 2 s sampler for status/peers while a live chart is visible, not per-page timers and not polling config/firewall/routes every two seconds. When only opted-in history is collecting, sample at 5 s. Stop live-only sampling when hidden; preserve opted-in background collection while the app runs. Service polling is on-demand after actions, during bounded transitions, and while resolving an unavailable daemon.

Keep a shared derived mesh model, not another mutable copy of every API response. Its lifetime is above route navigation. Rates, selection and history must not reset merely because the user opens Peers.

## 6. Telemetry and graph invariants

These are implementation requirements, grounded in the current source:

- `Status.rx_bytes/tx_bytes` and peer byte fields are cumulative. Rate is counter difference divided by actual monotonic elapsed time. First observation, rollback, reconnect/epoch uncertainty, non-positive time and daemon-uptime decrease invalidate the baseline. Show “—”, not negative/infinite traffic.
- `Peer.last_seen_ms` is **age at snapshot receipt**, not an epoch timestamp. Add local elapsed time while displaying a cached sample. Zero is ambiguous with never-seen; do not manufacture a handshake timestamp from it.
- A route's path excludes the local node and ends at its destination. Prepend self once. `cost_ms` is path cost, not RTT for each constituent link.
- Activity byte fields are **deltas**, not cumulative counters. Deduplicate `(origin, destination, seq)` within an observation generation. Expiry controls live evidence; historical records do not disappear simply because live evidence expires.
- Current activity sampling omits zero-traffic records and exports no sample duration. Never divide bytes by the 90 s TTL or blindly assume the nominal 5 s interval. A remote rate is computable only with a proven window (for example consecutive origin sequences and increasing observation times); after a gap show measured bytes/window unknown until a new valid baseline. Do not add both endpoints' reports to double-count the same traffic.
- Local activity paths currently contain the destination, not the origin. Normalize from the declared origin and the actual path; do not apply route-advertisement semantics to Activity by analogy.
- Nodes include self, known peers and explicitly referenced route intermediates. An intermediate with no peer details gets an ID and unknown metadata, not an invented name/presence.
- Direct local up-links and fresh authenticated remote observations provide link evidence. A contiguous route segment provides **route evidence**, not proof of symmetric reachability. Retain provenance and freshness; absence of a reported link does not prove it is down.
- Stale Activity removes its traffic evidence, not independently current route/direct-link evidence. No star edge to every configured peer and no invented full mesh.
- Sum totals from this device's counters, not all relay/remote observations. A forwarded packet may legitimately appear on multiple links.

Peer presence is a UI classification with visible criteria: active = fresh observed traffic in the recent 10 s window; idle = current reachable/up state with fresh zero local measurements; offline = explicit down/departed/unreachable evidence. Missing measurements instead show unknown/stale. Activity here means network traffic, not human presence.

## 7. Map and inspectors

Stable ID-based node placement for at most five nodes, self clearly marked. Do not reorder the canvas on every name/rate update. The path overlay follows actual next hops. Use distinct line patterns and a legend for measured direct links, routed paths and stale/unknown evidence; color alone is insufficient.

Peer inspector: name/ID, overlay v4/v6, state, endpoints/candidates, TX/RX transport, last-seen age, counters/rates, routes via the peer, Ping, Probe and View routes. Public identity is not a private-key viewer.

Link inspector: reporting origin, endpoints, direction, transport, direct/routed provenance, RTT/jitter samples, throughput window and evidence age. Whole-path RTT is labelled as such. “Observed up since” is app-collected continuity, not daemon session uptime. Add last-handshake and session-start timestamps at authoritative daemon session transitions and expose them read-only; sessions with no recorded transition return unknown until one occurs. Never substitute last-seen for a handshake. No made-up loss/NAT diagnosis.

Packets/dots are a visual rate encoding, not packet capture. Animate only fresh measured directions; TX is origin→destination and RX the reverse. Remote reports cannot create instant traffic across unmeasured route segments. Cap dot count and use transform/opacity, no per-frame React state or stroke-dashoffset animation. Unknown/zero/stale samples stop motion. Reduced motion uses static directional arrows and values; hidden views stop all animation.

The canvas and table are two presentations of the same model. A visible Map/Table switch offers keyboard-complete node/link/path selection and the same actions. Never place focusable canvas controls beneath `aria-hidden`; if a decorative graph is hidden from accessibility APIs, its accessible controls live outside that hidden subtree. Escape clears selection/closes drawers; focus returns to the originating control. Zoom/pan shortcuts do not steal keys from inputs or the palette.

## 8. Events, history and search

Activity has two complementary views: a human event feed and measured throughput/RTT history. Authenticated Activity samples are telemetry, not a substitute for lifecycle events. Use real lifecycle events, successful action results and snapshot differences with provenance: a diff may say “route observed/withdrawn”, not claim an unobserved packet handshake or exact occurrence time. Correlate action results and events to avoid duplicate notifications.

Live charts retain the last 60 samples with timestamps and gaps. Finish the existing GUI-owned `HistoryService`; it is currently a stub, not a functioning store. Keep its responsibility instead of deleting history scope. Opt-in persistent history is **off by default**, bounded by **24 h and 50 MB**, whichever expires first. Clear and CSV/JSON export work. Store payload-free aggregate/per-peer measurements and redacted event metadata only; no secrets, invitation strings, private keys, packet payloads, endpoints or raw configuration. Peer IDs are local history data and disclosed in the opt-in explanation.

History belongs to the OS user, not `/etc/skein`, and records only while this app is running. Partition capture sessions by verified connection/node identity; do not merge across an unobserved restart or network switch merely because a retained node ID matches. Label collection gaps and provenance, and distinguish live evidence expiry from retention. Persistence uses a bounded GUI-owned file store with crash-safe writes, not a new daemon database.

Search/filter peers, routes, logs and activity via the existing palette/local filters. URL-backed filters survive navigation; result selection opens the relevant inspector or filtered route. No global indexing service is needed for five nodes and bounded logs/history.

## 9. Full operator parity and required contract work

The original draft's “only add IdentityService” boundary is withdrawn: it could not meet the requested parity. The following are **planned changes**, not claims that their bindings/contracts already exist.

| Capability | Required implementation boundary |
|---|---|
| Ping | Bounded, cancellable overlay-IP operation through a typed Go service; reuse CLI semantics with parsed IP and fixed argv, no shell or arbitrary command input. Keep ICMP reachability distinct from Probe. |
| Probe | Existing asynchronous endpoint plus `peer:probe`; peer-specific pending/result/timeout state, not optimistic success. |
| Public identity | Bind existing `/identity`; public ID/key only. |
| Key generation | Match `genkey`: reuse an existing key; generate only when absent; never overwrite. Operational system identity is minted/reused by bootstrap. Offline generation uses an explicitly selected user-owned configuration/key destination under the existing configstore lock, refuses active/system targets and exposes only public output. Import into a system installation goes through daemon bootstrap, not a GUI write. |
| Access/roles | Add a secret-role, kernel-transport read contract for effective UID/SID grants and caller capability. Current `SecurityStatus.allowed_uids` cannot describe roles or Windows grants. Structured grant/set-owner/revoke dialogs use existing fixed helper verbs and authoritative readback; preserve last-secret-user protection and explicit force confirmation. No guessed role from UID membership. |
| Safe config | Existing daemon patch allowlist; editable name/port/MTU/mDNS, effective network/interface/STUN/paths read-only. Dirty forms are not overwritten by incoming snapshots; apply/restart outcomes are explicit. |
| Service | Start/Stop/Restart/Install/Repair/Uninstall via existing verified lifecycle path. Repair reuses supported install/repair semantics, not a new broad helper verb. Fresh setup remains bootstrap. Helper uninstall purge is not daemon config purge. |
| Firewall | Existing status/repair, scoped to Skein-owned rules; shared surface linked from Diagnostics/Settings. |
| Diagnostics | Native save dialog plus real redacted file output from the existing diagnostic bundle; disclose included peer/route/log/config-summary data. Clipboard copy is secondary, not file-export parity. |
| Updates | Keep operator-staged signed manifests/binaries. Add bounded release-note metadata to the signed release/check contract and release tooling; no speculative feed. Apply is asynchronous: observe applied/rollback result and then verify the actual running version. Existing packages without notes state that fact. |
| Notifications | Real event/action outcomes with one relevant action; session event list remains available after the toast. Role and service state gate the same shared page/palette action. |

Privilege boundary remains ADR-001: unprivileged GUI, sole privileged daemon writer, fixed helper verbs, verified socket/pipe server identity, existing role order and kernel-only secret operations. Offline user-owned artifacts do not authorize writing active system configuration. Native file actions validate destinations, prevent overwriting live secrets, and do not expose arbitrary elevated paths.

## 10. Invitations and identity safety

Two distinct actions: network invitation joins the network and contains a secret; peer invitation introduces a public peer and does not grant membership. Preserve snapshot warnings, reveal/copy and QR. Do not retain invitation plaintext in history, notifications, logs, URLs or exports. Show the consequences of clipboard/screenshot exposure without promising that the OS prevents it.

The owner explicitly deferred expiry, active inventory and use attribution. Do not add an advisory “Expired” badge or a locally generated list labelled “Active”; neither would implement those requirements. Do not infer who used an invitation from a later peer event. Existing invites have no enforced expiration; network-wide rotation changes credentials only after the required restart and rejoin of legitimate nodes. It is not per-peer revocation.

## 11. Onboarding and recovery

Create, Join and Import are first-class choices in the same design language. Keep bootstrap tokens in memory, single-use and short-lived; secret-bearing results are revealed only in the relevant completed step, never placed in persistent notifications/history.

Same-network rejoin uses normal-mode join with explicit confirmation. Switch network is leave → reset (backup versus purge, keep-identity choice) → verified Stop → create/join/import. Each step shows real local state and propagation limits; departure is not a ban. Preserve partial completion and returned backup/firewall error evidence through navigation.

Rejoin success means membership restored. Remote tombstones can delay reconnection up to 180 s; show “Waiting for peers” until observed connectivity, not “mesh resumed”. With no peers, setup may be complete while the mesh has no active links. Approval cancellation and failed authentication have distinct actionable guidance; the GUI never acquires a shell or keeps elevated privilege.

## 12. Health and troubleshooter

Run a bounded checklist for a selected peer: actual service state, verified API access, membership/pause state, relevant route, known candidates, Probe and overlay Ping, then firewall facts. Display observations and the next action, not unsupported causal claims. A failed Ping is not by itself proof of a NAT/firewall fault. Diagnostic loss is measured loss for that ping run; the API has no continuous loss field.

Investigate the recorded Windows/Linux peer-list asymmetry as an interoperability issue during implementation validation. Do not conceal it by drawing reciprocal links or copying the Linux report into the Windows peer list.

## 13. Accessibility, performance and verification

Keyboard-complete navigation, visible focus, Radix focus trapping/restoration, accessible names, contrast checks in all retained themes, and reduced motion are acceptance requirements. Announce discrete changes politely; do not announce every byte-rate sample. Log lists are bounded/virtualized and pausable. Local filters must not trap global shortcuts.

Verification must exercise consumer-visible invariants: no invented edges, source/path direction, unknown versus zero, counter reset, activity duplicate/gap handling, history retention/export, action-state reconciliation and secret redaction. Use the smallest suitable existing/standard test tooling; a new test framework is not a design requirement. Native visual/keyboard/motion verification is separate from typechecking and from fixtures.

For implementation acceptance: real GUI at 900×600 and 1280×800, one-node empty state and up to five-node direct/relay/partial-observation fixtures; native Linux plus Windows for affected behavior; service/elevation/recovery only on authorized disposable targets. Observe 60fps behavior under traffic/event bursts rather than asserting it from CSS. Keep full Go/frontend/native build and packaging gates, binding regeneration with actual method inventories, and unchanged CLI/TUI/security semantics.

Phase 0's disposable VM was destroyed and the Windows test installation removed. Those historical results are not current Phase 1 runtime evidence. Re-establish authorized test targets before Phase 2/3 mutation tests. The daily-use Linux installation remains read-only.

## 14. Carried observations and source anchors

- Phase 0 remains the accepted prior checkpoint, not proof of the redesign. Keep the delayed-rejoin observation, peer-list asymmetry and unexplained 554→552 unrelated-firewall count change visible; do not restate the older blanket unchanged-firewall claim as current evidence.
- `internal/api/types.go`: Status/Peer/Route/Activity wire fields.
- `internal/daemon/daemon.go:529-638`: cumulative totals, elapsed last-seen, route projection and Activity snapshots.
- `internal/route/table.go:38-47`: resolved paths exclude self.
- `internal/control/control.go:420-506`: origin sequence, direct-link delta records, omitted zero records and destination-only local Activity path.
- `internal/daemon/events.go:143-179`, `gui/services/events.go:14-114`: typed and lifecycle events; current bridge omissions.
- `gui/services/history.go:9-37`: five exported stub methods, not four.
- `internal/cli/cli.go:564-591,1245-1257`: non-overwriting key generation and separate ICMP Ping.
- `internal/api/types_gui.go:89-113`: incomplete grant-read fields and existing diagnostic export payload.
- `internal/api/types_system.go:116-154`: update check/apply outcome; no release-note field today.
- `internal/identity/network.go:11-31,61-64`: secret-bearing, stateless invite format; basis for the approved deferral.
- `gui/frontend/src/app/App.tsx:13-29`: existing hash routes, including `/network` and `/logs`.

## 15. Review and next step

Review this design before revising the existing implementation plan. The old plan is not executable: its Go-only identity addition, History deletion, broad polling, missing parity and method-count assumptions no longer match this design. Planning must split delivery into working vertical slices (event/mesh, history/activity, operator parity, recovery/settings, accessible audit) with shared contracts defined before delegation. No Phase 2 code is authorized merely by the existence of these documents.
