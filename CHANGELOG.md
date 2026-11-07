# Changelog

## Unreleased — mesh-first desktop redesign

The desktop is now a mesh workspace instead of a card dashboard. The daemon
protocol is unchanged; the GUI gained read/write contracts where parity
required them.

### Added

- Live mesh map: stable node layout, evidence-ranked links (direct, observed,
  routed), route drawer, peer and link inspectors, keyboard/table equivalent,
  and motion-path flow animation gated by real rates and reduced motion.
- Event-first data layer: one SSE consumer forwards typed snapshots and
  lifecycle events (including peer-down, departure and dialer-fallback);
  snapshots write the query cache directly, with a 30 s reconciliation.
- Bounded, opt-in local history (24 h / 50 MB, 5 s cadence, JSON/CSV export),
  app-wide while the app runs; the daemon never stores history.
- Peer parity: bounded overlay Ping (fixed argv, IP-only), asynchronous Probe,
  session-start/last-handshake timestamps, health explanations.
- Security: secret-gated `GET /api/v1/security/grants` (caller identity,
  transport, role, effective grants), grant/revoke/set-owner through the fixed
  helper verbs, public identity and offline key generation (reuse-or-create,
  system-path refusal, public output only).
- Diagnostics: native diagnostic-bundle export; logs tail with level filter,
  pause/follow and a peer troubleshooter.
- Updates: signed release notes with legacy-compatible verification; apply
  polls the outcome and confirms the running version.
- Settings/recovery/onboarding: dirty-state node form, verified service
  lifecycle, tombstone-aware rejoin copy, switch wizard with backup/purge and
  Retry-stop-only partial completion, one-time bootstrap secret.
- Command palette, session notifications, cross-surface filters.

### Changed

- The desktop starts maximised on every platform so a 1280x800 window is not
  clipped on 1366x768 laptops.
- Polling is replaced by event-driven cache updates plus a calm sampler; the
  service/config cadence is 30 s.
- The mesh node card uses explicit theme values inside React Flow, where
  inherited custom properties resolved to the light palette in WebKit.

### Deferred

- Invitation expiry, active-invite inventory and invite-use attribution
  (owner decision; the UI states the limitation and points to rotation).
- F-01 per-peer invitation revocation remains deferred by project design.
- Diagnostics paging still uses the support-bundle ring rather than the
  daemon's `next` cursor.

### Verification

- Frontend: `tsc --noEmit` clean, 82 vitest tests, production build.
- Go: build, vet, Windows cross-build; isolated suite green (one
  PID-namespace test artifact, passes outside the namespace); architecture and
  packaging gates.
- Native: Linux daily-use upgrade (installed hashes match the build; node id
  and membership preserved; live grants/identity/activity/settings), Windows
  fresh source install (Overview, Security with kernel/secret caller and SID
  grant, Activity, Settings) followed by scoped removal with SSH/PATH/firewall
  invariants intact.
- Not exercised: multi-node/relay traffic, upgrade/NSIS packaging, Linux
  distribution packages.
