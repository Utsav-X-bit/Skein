# Mesh-first redesign — implementation plan

**Status: superseded draft — do not execute.** The source-reviewed design was revised on 2026-11-07 and awaits owner review. The owner explicitly deferred only invitation expiry, active-invite inventory and invite-use attribution. The remaining text is retained as prior planning material, not an approved scope or executable task list.

Required plan corrections after design review: preserve full ping/key-generation/access parity; implement bounded History rather than delete it; use the single event bridge and targeted sampling; correct route/Activity semantics; include peer/link inspectors, real diagnostic export and signed release notes; inventory actual bindings (History has five methods, not four). Remove the “Go changes are limited” restriction and define source-backed shared contracts before delegation.

> Executor: follow the spec at `docs/superpowers/specs/2026-11-07-mesh-first-redesign-design.md`. Tasks are ordered; A is a prerequisite for B–F. Do not run builds/tests/lint mid-task; the integration owner runs them once per batch.

**Goal:** replace the card dashboard and fabricated star topology with one derived mesh model, honest per-link facts, session history, real recovery and complete operator surfaces.

**Architecture:** pure model functions (`features/mesh/model.ts`) + one hook (`useMesh`) feed canvas, peers, activity, overview and notifications. Go changes are limited to deleting the empty `HistoryService` stub and adding a read-only `IdentityService`.

**Tech stack:** React 19, TypeScript, TanStack Query, @xyflow/react, Radix, Tailwind tokens, sonner, zustand; vitest for pure functions.

## Global constraints

- No invented data: edges only from `peers[].direct` or `routes[].path`; unsupported invite/identity/update capabilities are labelled.
- No new runtime dependencies except `vitest` (dev).
- Tokens only (`var(--…)`); reduced motion honoured; canvas has a list/table equivalent.
- Every destructive action keeps its existing confirm and partial-completion behaviour.
- No daily-use host mutation during verification.

## Shared interfaces (Task A produces; B–F consume)

```ts
// features/mesh/model.ts
export type LinkRate = { rxPerSec: number; txPerSec: number };
export type HistorySample = { t: number; rx: number; tx: number; peersUp: number };
export type MeshNode = { id: string; label: string; overlay?: string; kind: "self" | "direct" | "relay"; state: string; rttMs?: number; transport?: string; hops?: number };
export type MeshEdge = { id: string; from: string; to: string; kind: "direct" | "relay" | "unknown"; path: string[]; rttMs?: number };
export type MeshPath = { destination: string; path: string[]; hops: number; costMs: number };
export type MeshModel = { nodes: MeshNode[]; edges: MeshEdge[]; paths: MeshPath[]; stale: boolean };
export function buildMesh(status: Status, peers: Peer[], routes: Route[]): MeshModel;
export function computeRates(prev: { t: number; rx: number; tx: number } | null, next: { t: number; rx: number; tx: number }, restarted: boolean): LinkRate | null;
export function dedupeActivities(rows: Activity[]): Activity[];
export function bucketActivities(rows: Activity[], windowMs: number): { key: string; origin: string; destination: string; transport: string; direct: boolean; path: string[]; rx: number; tx: number; rttMs: number; jitterMs: number; lastObservedMs: number }[];
export function peerHealth(p: Peer): { tone: "up" | "degraded" | "down" | "unknown"; label: string; detail: string };
export function formatRate(rate: LinkRate | null): string;
// features/mesh/useMesh.ts
export function useMesh(): {
  status: Status | undefined; peers: Peer[]; routes: Route[]; activities: Activity[];
  model: MeshModel; rates: Map<string, LinkRate>; history: HistorySample[];
  isLoading: boolean; isError: boolean; error: unknown; staleSince?: number; refetch: () => void;
};
// features/mesh/useSessionHistory.ts
export function useSessionHistory(): { samples: HistorySample[]; push: (s: HistorySample) => void; reset: () => void };
```

## Task A — model, hook, Go service changes (prerequisite)

**Files:**
- Create: `gui/frontend/src/features/mesh/model.ts`, `useMesh.ts`, `useSessionHistory.ts`, `model.test.ts`, `vitest.config.ts`
- Modify: `gui/frontend/package.json` (add vitest + `test` script)
- Delete: `gui/services/history.go`; remove HistoryService from the GUI service list
- Create: `gui/services/identity.go` (read-only `NodeIdentity()` returning ID, public key, fingerprint)
- Regenerate bindings (Wails) and confirm method counts: History −4, Identity +1.

**Steps:**
1. Add vitest; write failing tests for `computeRates` (steady, restart, counter rollback → null), `dedupeActivities` (same origin+dest+seq collapses), `buildMesh` (no edge without direct peer or route path; relay node per intermediate hop; stale flag), `bucketActivities` (window aggregation), `peerHealth` (up/direct, up/relay, down with last-seen, unknown).
2. Implement `model.ts` until tests pass.
3. Implement `useMesh` (queries + SSE invalidation + derived rates/model) and `useSessionHistory` (120-sample ring).
4. Delete the HistoryService stub and its registration; add `identity.go`; regenerate bindings.
5. Run `npm test`, `npx tsc --noEmit`, `go build ./...` in the guest; report exact output.

## Task B — canvas, accessible fallback, overview

**Files:** rewrite `features/topology/Topology.tsx`; create `features/mesh/MeshCanvas.tsx`, `MeshTable.tsx`, `ThroughputStrip.tsx`, `EventStrip.tsx`; rewrite `routes/overview/Overview.tsx`.

- Deterministic rings from `model`; edge styles by kind; flow animation only with non-zero rate and motion allowed.
- `aria-hidden` canvas + focusable ordered list/table from the same model; “Table view” toggle.
- Overview: health strip (connection state, peers up/total, rates, uptime, stale warning), canvas, session throughput (ring buffer), last events (SSE, session-only, deep links).

## Task C — peers

**Files:** rewrite `routes/peers/Peers.tsx`; create `features/mesh/PeerInspector.tsx`, `PeerHealth.tsx`.

- Sortable table (name, state, path, transport, RTT, rates, last seen); keyboard rows; URL-backed selection `?peer=`.
- Inspector: identity, endpoint/candidates, transport, counters, last-seen, route path, probe with result, add-device dialog, leave confirm.
- Health/troubleshooter from observable facts only, with links to Security/Diagnostics.

## Task D — activity and invitations/identity

**Files:** rewrite `routes/activity/Activity.tsx`; create `features/activity/Timeline.tsx`; rewrite `routes/invites/Invites.tsx`; rewrite `routes/security/Security.tsx`.

- Activity: windowed buckets, filters (peer/direct/transport) URL-backed, expiry notes.
- Invitations: network/peer generation, reveal/copy, rotate confirm + restart guidance, unsupported block (no expiry/inventory/usage/revocation).
- Security: ownership/modes, allowed UIDs, socket path, firewall status/repair, update-signature state, read-only identity (copy only), no regenerate.

## Task E — settings, recovery, onboarding

**Files:** rewrite `routes/settings/Settings.tsx`, `routes/settings/NetworkRecovery.tsx`, `routes/onboarding/Onboarding.tsx`.

- Settings: node form (dirty-state), read-only effective config, service card with authoritative state and elevation guidance, recovery entry.
- Recovery: rejoin/switch wizard, backup/purge/keep-identity, partial completion + Retry stop, tombstone warning.
- Onboarding: create/join/import with working/done/error states, secret shown once, approval guidance.

## Task F — diagnostics, about, palette, notifications, search

**Files:** rewrite `routes/logs/Logs.tsx`, `routes/about/About.tsx`; create `features/notifications/*`; rewrite `components/layout/CommandPalette.tsx`, `CommandBar.tsx`.

- Diagnostics: level filter, virtualized tail, copy/export redacted bundle.
- About: version/protocol/platform/capabilities, update check/apply, grants reload; no feed.
- Palette: navigation + actions + fuzzy search over peers/routes/logs; combobox semantics.
- Notifications: SSE-driven toasts with one action; session-only popover.

## Task G — audit and delivery (integration owner)

- `npm test`, `npx tsc --noEmit`, `npm run build`; full Go suite in the isolated guest; `go vet`; Windows cross-build; packaging gates.
- Real GUI smoke on Xvfb (host) against the read-only daemon; Windows read-only launch; screenshots.
- Regenerate bindings; verify method counts; update `docs/engineering/GUI-REDESIGN-STATUS.md` and `~/workspace/skein-work/BUGS.md`; `graphify update .`; commit without pushing.
