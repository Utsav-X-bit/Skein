# Changelog

## Unreleased — mesh-first desktop redesign

The desktop is now a mesh workspace instead of a card dashboard. The daemon
protocol is unchanged; the GUI gained read/write contracts where parity
required them.

### Added

- Automatic path-MTU negotiation: after a direct connection, the daemon
  measures the largest unfragmented frame to every direct peer and raises the
  overlay MTU (up to 1346) when all of them support it. The configured MTU is
  the floor; a failed measurement leaves it unchanged.
- `skein uninstall` removes the local installation and its data after
  confirmation; `--yes` confirms noninteractively.
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

### Fixed

- Joining an already configured network now initiates contact with the
  inviter even when discovery previously registered it as a non-dialer.
- Network invites reserve the inviter's overlay address as well as known
  peers' addresses, preventing a new joiner from selecting the same address.
- Failed bootstrap setup cancels its own unused token when safe; pending
  bootstrap errors include recovery guidance.

- The Arch installer no longer asks pacman to replace an installed Node.js
  LTS package; it uses its existing private Node.js fallback when needed.
- The Arch installer explains how to recover from out-of-sync system-library
  dependencies without performing a system upgrade automatically.
- First-time setup stays open while the service starts and until the user has
  seen the result, preserving the one-time network join string.
- Duplicate multi-address handshakes no longer delete a completed responder
  handshake, which previously left a joining peer permanently unadmitted.
- Handshake replies to an address owned by a stale down peer are addressed to
  the sender instead of the old peer ID; only up/degraded peers count as
  relays.
- Create/join now clears a persisted connection pause, so a new network does
  not start with no dataplane after a reset.
- Windows first setup now launches the helper through UAC and returns the
  bootstrap token over a random, user-only named pipe, so the normal
  unprivileged desktop can create or join without manual elevation. The
  helper verifies that the pipe server is the desktop before writing.
- Mesh cards expose connection handles and use straight links between facing
  sides; the map refits when the layout changes.

### Added

- `scripts/install-linux.sh` (Arch/Debian/RPM families, prebuilt or source,
  standalone clone, dry-run) and `scripts/install-linux-test.sh`.
- Windows setup hardening: WebView2 verified after installation, trusted
  install-directory validation, controlled copy/stop failures, tray-aware
  running-desktop refusal, machine-wide-only builds.

### Changed

- The desktop starts maximised on every platform so a 1280x800 window is not
  clipped on 1366x768 laptops.
- Polling is replaced by event-driven cache updates plus a calm sampler; the
  service/config cadence is 30 s.
- The mesh node card uses explicit theme values inside React Flow, where
  inherited custom properties resolved to the light palette in WebKit.
- Mesh cards now expose invisible connection handles; links render as straight
  segments between facing card sides instead of missing links or Bézier detours.

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
- Phase 4: real Linux↔Windows direct mesh, bidirectional overlay ping and
  sustained 240/240 packet delivery; live throughput and opt-in local history.
  Native Linux rendering verified the repaired straight map link.
- Real three-node relay and Windows 11 runtime remain unexercised.
