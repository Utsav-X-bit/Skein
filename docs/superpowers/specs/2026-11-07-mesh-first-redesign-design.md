# Mesh-first desktop redesign — design

Date: 2026-11-07. Branch `GUI-redesign`. Supersedes the pre-ADR-001 specification. Phase 0 acceptance is recorded in `docs/engineering/GUI-REDESIGN-STATUS.md`; this document covers Phase 1 design only.

## 1. Goal

Make the desktop answer, at a glance and without inventing data: **what is my mesh doing right now, what is known and unknown about each link, and what can I safely do next?** Every surface must be backed by an existing daemon/binding contract; anything the contract cannot answer is shown as unknown or unsupported, never simulated.

Success criteria:

- The Overview is a live mesh (nodes, direct links, relayed paths) derived from `peers` + `routes[].path` + authenticated `activity`, with an accessible list equivalent and reduced-motion support.
- Every peer row/detail exposes only real fields: state, direct/relay, transport, endpoint, candidates, RTT, cumulative counters, last-seen; rates are derived from deltas with restart handling.
- Activity is a windowed, deduplicated timeline of authenticated link samples, never presented as persisted history.
- Invitations, identity, access and updates show exactly what the contracts expose; no expiry, inventory, usage, revocation or release-feed fiction.
- Recovery (leave, reset, purge, keep-identity, create/join/import, rejoin) is reachable in one place, with authoritative service state and partial-completion handling.
- Command palette and notifications act on real actions and real SSE events; every dialog is keyboard-complete.

## 2. Non-goals

- No new daemon endpoints except one read-only `IdentityService` binding for the public node identity (node ID, public key) used by Security.
- No per-peer removal, no invite expiry/inventory/usage, no release feed, no automatic update checks.
- No persisted history in this iteration; the GUI-side `HistoryService` stub is deleted rather than half-implemented.
- No invented health causes (NAT, packet loss, handshake time). Health is a composition of observable fields.

## 3. Approaches considered

**A. Mesh-first single canvas (recommended).** Overview becomes the mesh with a health strip, peer cards and a session throughput strip; Peers/Activity/Invitations/Security/Diagnostics/Settings are focused work surfaces reached from the canvas and the palette. One information model (`useMesh`) feeds every surface, so states cannot disagree.

**B. Dashboard cards (incremental).** Keep the current card grid and improve each card. Lowest risk, but the mesh stays a decoration; route paths, relay chains and per-link facts remain scattered.

**C. Wizard-first.** Onboarding-centric shell with the mesh as a secondary page. Rejected: after setup, operators spend their time in the mesh; the wizard is a rare state.

A wins because it matches the product's mental model (a mesh, not a form) and collapses duplicated fetching into one derived model. B's honesty rules are absorbed into A.

## 4. Information architecture

| Route | Purpose | Primary contracts |
|---|---|---|
| `/` Overview | Mesh canvas, health strip, session throughput, recent events | status, peers, routes, activities, SSE |
| `/peers` | Table + inspector: per-peer facts, probe, add-device, leave | peers, probe, add, leave |
| `/activity` | Windowed timeline, filters (peer, direct/relay, transport) | activities |
| `/invites` | Network/peer invites, rotate secret | invites, rotate |
| `/security` | Ownership, modes, allowed UIDs, firewall status/repair, node identity | security, firewall, identity |
| `/diagnostics` | Log tail with level filter, diagnostic bundle export | logs, bundle |
| `/settings` | Node config, service lifecycle, network recovery | config, service, setup |
| `/about` | Version, capabilities, update check/apply, grants reload | app, update |

Deep links: `/peers?peer=<id>`, `/settings?network=rejoin|switch`, `/activity?peer=<id>&direct=1`, `/diagnostics?level=warn`. The palette and notifications target these.

## 5. Data layer

One module, `features/mesh/useMesh.ts`, owns the derived model:

- Queries: `status` (2 s), `peers` (2 s), `routes` (2 s), `activities` (2 s), `service` (3 s), `connection` (on demand + SSE invalidation).
- **Rates**: for this node and each peer, compute `Δbytes/Δt` from consecutive samples keyed by daemon start (detect `uptime_seconds` decrease or counter rollback → reset the baseline, show “—”, never a negative rate).
- **Session history**: bounded ring buffer (120 samples ≈ 4 min at 2 s) of `{t, rxRate, txRate, peersUp}` plus per-peer `{rtt, rate}`. In-memory only, cleared on unmount/daemon restart; the UI labels it “this session, collected by this app”.
- **Graph model**: nodes = self + peers; edges = (a) direct peer links with `direct && state==="up"`, (b) relayed paths from `routes[].path` (full path arrays). An edge exists only if a route path or a direct peer proves it. Expired/absent activity marks an edge “unknown” (dashed, no rate) rather than zero.
- **Event wiring**: SSE events (`peer_up`, `peer:leave`, `peer:rejoined`, `peer:probe`, `peer:discovered`, `peer_added`, `peer_removed`, `name_changed`, `secret_rotated`, `system_reset`, `system_imported`, `leave_dataplane_stop_failed`) invalidate the affected query keys and feed the notification center. No event is fabricated.
- **Offline**: a single `useDaemonHealth` gate distinguishes `missing` (onboarding), `stopped` (shell + banner), `offline` (last-known data + retry), and `ok`. Screens never blank on transient errors; they show last-known data with an explicit staleness timestamp.

## 6. Mesh canvas

- Layout: self centered; direct peers on an inner ring ordered by name; relay-only peers on an outer ring positioned along their route path (first hop nearest). Deterministic ordering; no physics jitter.
- Node content: name or short ID, state dot, overlay address; relay nodes show hop count; hover/focus reveals RTT/transport/rates.
- Edge styling: solid accent = direct up; dashed relay = routed path; faint dashed = unknown/expired. Directional flow animation only when a rate is non-zero, disabled under reduced motion.
- Interaction: click/Enter selects a peer → inspector (same component as `/peers` inspector); `f` fits, `+/-` zoom, arrows pan; Escape clears selection.
- Accessible fallback: a visually-hidden-but-focusable ordered list of the same facts (“This device → peer, direct, 4 ms, 1.2 MB/s”) rendered from the same graph model, plus a “Table view” toggle that replaces the canvas with a sortable path table. The canvas is `aria-hidden` with the list as the accessible equivalent.
- Performance: render at most the five-node ceiling; memoize nodes/edges on a stable hash of the model; animation via CSS transform/opacity only; no per-frame React state.

## 7. Peers surface

- Table columns: name, state, path (direct/relay + hop count), transport, RTT, rates, last seen. Sortable; keyboard row navigation.
- Inspector (320 px, drawer on narrow): identity (ID, overlay v4/v6), endpoint + candidates, transport + RX transport, counters, last-seen, route path, probe action with result, “Add device”, and “Leave network” (self only, with confirm).
- Health explanations are templates over facts: `state=up && direct` → “Direct link, last seen 2 s ago”; `state=up && !direct` → “Reachable via <path>”; `state!=up && last_seen` → “Last seen <t> ago; no current session”; no last-seen → “No session observed”. Unknown stays “unknown”.
- Troubleshooter: an ordered checklist of observable checks (service state, firewall state, candidates count, probe result, route presence) with links to Security/Diagnostics. It never claims a cause it cannot observe.

## 8. Activity and event strip

- Deduplicate on `origin+destination+seq`; aggregate into 1-minute buckets per link; show origin→destination, transport, direct/relay, path, RX/TX deltas, RTT, jitter, observed time, expiry. Expired samples are removed with a note, not silently.
- Filters: peer, direct/relay, transport; the filter state is URL-backed for deep links.
- Home event strip: the last N SSE events with timestamps and deep links (“Peer up: studio”, “Left the network”, “Secret rotated”). Session-only, clearly labelled; it is not daemon history.

## 9. Invitations and identity

- Network invite: generate, reveal/copy once, warning, snapshot notice (“addresses as of now”), rotate with confirm and restart guidance. Peer invite: generate, copy, QR (peer invites carry no network secret).
- Explicit unsupported block: no expiry, no active-invite list, no usage/consumption tracking, no per-peer revocation. Revocation is secret rotation and the copy says exactly that.
- Identity (new read-only `IdentityService`): node ID, public key (base64), short fingerprint, and the config/identity paths from Security. Copy actions only. No “regenerate key”: the identity is minted at setup and changing it means leaving the network (`reset` without keep-identity). The UI explains that instead of offering a destructive pseudo-action.

## 10. Onboarding and network recovery

- States: welcome → create | join | import → working → done; every elevation outcome (cancelled, failed, service not ready) has actionable copy; the bootstrap secret is shown once with copy and never logged.
- Recovery lives in Settings → Network membership and is reachable from the departed banner, the Peers page and notifications: rejoin (same network), switch (leave → reset with backup/purge and keep-identity → verified stop → create/join/import). Partial completion (reset done, stop unconfirmed) keeps the backup path and offers “Retry stop” only.
- After a leave, the UI states that remote peers keep a departure tombstone for up to 180 s, so an immediate rejoin may stay unlinked; it must not claim “mesh resumed” until a peer is observed up.

## 11. Node, service, configuration, access, security, updates, diagnostics

- Settings: node form (name/port/MTU/mDNS) with dirty-state polling pause; read-only effective paths/STUN; service card with authoritative state after every action and elevation guidance; recovery card.
- Security: ownership/modes for config+identity, allowed UIDs, socket path, firewall manager/state/rules with repair, update-signature state, node identity.
- Diagnostics: log tail (level filter, virtualized), copy/export redacted bundle, firewall link, “what this bundle contains” disclosure.
- About: running daemon version, protocol, platform, capabilities, update check (signed manifest path + staged binary), apply with restart guidance, grants reload. No download feed.
- Access/roles: show `allowed_uids` and owner/mode only; the full grant table has no read contract, so the UI links to the documented helper commands instead of inventing rows.

## 12. Command palette, notifications, search

- Palette (`Ctrl/Cmd+K`): navigation, peer selection, actions (probe, connect/disconnect, service start/stop/restart, generate invite, rotate secret, open logs, reset), and fuzzy search over peers/routes/logs. Actions call the same mutations as the pages; destructive ones open the same confirms.
- Notifications: SSE-driven toasts with one action each and a persistent “Notifications” popover (session-only). Severity from the event type; no invented severity.
- Cross-surface search: `/` focuses the local filter; the palette searches across peers, routes and log lines with grouped results.

## 13. Accessibility and motion

- Keyboard: every control reachable; canvas has list/table equivalents; dialogs trap focus and restore it; palette is a combobox with `aria-activedescendant`.
- Live regions: connection/peer changes announce politely; errors use `role=alert`.
- Motion: honor `prefers-reduced-motion` and a Settings toggle; allowed properties are transform/opacity/stroke-dashoffset; no parallax or looping ambient animation; flow animation stops when the tab is hidden.
- Contrast: keep the existing AA-checked token themes (dark/light/high-contrast/high-contrast-light); new components consume tokens only.

## 14. Error handling and degradation

| Condition | Surface behavior |
|---|---|
| Daemon missing | Onboarding only |
| Service stopped | Shell + banner; Settings/Diagnostics reachable; mesh shows last-known with staleness |
| Offline/unreachable | Last-known data, “as of <t>”, retry; actions disabled with reason |
| 403/role | Guidance to grant access; no silent retry |
| Config locked | “Another process holds the config; stop the service first” |
| Elevation cancelled | Approval guidance (shared formatter) |
| Partial reset | Backup path + Retry stop; never repeat reset |
| Expired activity | Removed from the timeline with a note; graph edge becomes unknown |

## 15. Testing

- Unit: add `vitest` as a devDependency (standard for Vite) with `npm test`; cover rate derivation incl. restart/rollback, activity dedupe/bucketing, graph model from peers+routes (no invented edges), health templates, palette matching, URL filter parsing. Pure functions live in `features/mesh/model.ts` so they are testable without a DOM.
- Component: accessible fallback equivalence with the canvas model; dialog focus; reduced-motion gating.
- Integration: real Wails bindings against a disposable daemon fixture for read paths; intercepted fail-closed bindings for destructive flows (as in Phase 0).
- Manual: real Linux GUI (VM) and real Windows GUI (authorized PC) smoke for the changed surfaces; screenshots for visual audit; no daily-use host mutation.
- Contract: `npx tsc --noEmit`, production build, full Go suite, bindings regeneration with method-count check.

## 16. Deletions

- `gui/services/history.go` stub and its binding (returns empty/errors) — replaced by the session ring buffer.
- The star-shaped `features/topology/Topology.tsx` (fabricates a full mesh from the peer list) — replaced by the path-aware canvas + table.
- Duplicated per-page fetching in Overview/Activity/Peers — replaced by `useMesh`.

## 17. Open questions for the owner

1. Session-only history (no persistence) is the recommendation; persistence would need a GUI-owned store and an explicit retention policy.
2. The read-only identity surface needs one small daemon-backed `IdentityService`; confirm it is in scope (it exposes only the public identity already returned by the daemon).
3. Invitation expiry/inventory remains a product decision with no contract; the design shows it as unsupported.
