# Phase 4 — multi-node acceptance and automatic installation

## Live mesh acceptance

The authorized Windows 10 22H2/amd64 PC joined the running Linux system
node using an invite obtained from `POST /api/v1/invites/network` over its
local authenticated socket. No network secret is saved in this report.
Linux identity `61a31aa77736412c` / `10.144.144.9` was preserved.

- Both CLI peer tables showed the other node **up**, by name, with overlay
  addresses, direct UDP endpoints and session timestamps. Both route tables
  showed the corresponding one-hop route.
- Bidirectional overlay ping after a **fresh package install and join**:
  10/10 replies each way (Linux RTT 3.0–18.7 ms; Windows 2–10 ms).
- Sustained Linux→Windows traffic during acceptance: **240/240 replies**,
  1200-byte payload, 0.2 s interval (RTT min/avg/max 3.310/9.364/147.335 ms).
- Native Linux Overview showed the connected peer, capacity 4 of 5, and one
  route. The Windows normal-token desktop showed 1/1 peers, connected state
  and one route. Both rendered the direct link as a straight segment.
- Opt-in history used a disposable GUI profile. The Activity screen showed
  live rates and stored history; six samples contained only timestamps,
  rates and peer counts, with positive rates and one up peer. Recording was
  switched back off afterward.

### Defects found and fixed during acceptance

1. **Map links missing.** The custom React Flow node had no connection
   handles, so connected nodes rendered without a link. Added invisible,
   non-connectable handles for all four sides and straight paths between the
   facing sides; the map now also refits when the layout changes.
2. **Duplicate candidate handshakes corrupted admission.** When the initiator
   raced several candidate addresses, a repeated message one arrived after
   the responder had completed the handshake. The error path deleted the
   completed responder state, so the later confirmation no longer matched
   and the responder never admitted the peer. The responder now preserves a
   completed handshake. Regression:
   `TestDuplicateCandidateHandshakesPreserveReadySession`.
3. **Stale peer addresses misrouted replies.** A down peer from an earlier
   test identity still owned the joiner's underlay address. Handshake replies
   were addressed to that stale peer ID, so the fresh member dropped them.
   Only peers that are up or degraded are now treated as relays; a reply to
   an address owned by a down peer is addressed to the sender. Regression:
   `TestFreshIdentityCanReuseADownPeersEndpoint`.
4. **Bootstrap inherited a persisted pause.** A reset leaves the connection
   pause flag in the daemon state file. A subsequent create/join started
   paused with no dataplane. Bootstrap commit now clears that flag.
   Regression: `TestBootstrapClearsPersistedPause`.

All four were reproduced before the fix. The session/dataplane/control/link/
peer suites and the real-socket integration suite pass with `-race`; the
bootstrap and connection regressions pass under `-tags skeindev`.

## Automatic Linux installer

`scripts/install-linux.sh` detects Arch-family, Debian 13+/Ubuntu 24.04+ and
Fedora 40+/RHEL 10+ systems, installs distro runtime/build dependencies,
builds as the invoking non-root user with pinned Wails v3.1.0, and uses sudo
for root-owned files, the helper, polkit policy, desktop entry/icon and the
socket-access group. It can install compatible prebuilt binaries with
`--binary-dir`, and works standalone by cloning the official repository.

Verified in a checksum-verified Ubuntu 24.04 cloud VM with systemd:

- Prebuilt fresh install: dependencies installed, narrow AppArmor userns
  profile for `/usr/local/bin/skein-desktop` only (global restriction left
  at `1`), root:root 0755/0644 files, `skein` group, no invented config and
  no service registered before the network choice.
- CLI bootstrap `skein system init`: service enabled and running, and ufw
  rules for `11010/udp` plus the `skein0` interface created by the daemon.
- Source-build upgrade: private Node 22 downloaded and SHA-256 verified,
  pinned Wails installed privately, all four binaries built as the normal
  user, service stopped before replacement, identity hash unchanged, helper
  reported running+enabled, installed files root:root 0755.
- The installed desktop rendered under Xvfb with the global userns
  restriction still enabled.

Distro detection and package availability were checked against Debian 13,
Fedora 43 and Arch base containers. Actual installation was exercised on
Ubuntu 24.04; no Arch or RPM runtime install was claimed.

## Windows 10/11 setup

The single x64 NSIS setup was rebuilt from the current tree and exercised on
the authorized Windows 10 22H2 PC:

- Upgrade over the live install: exit 0, all four installed hashes match the
  build, identity and network secret preserved, service Automatic/running as
  LocalSystem.
- Fresh install with no config: all payload files plus Wintun/licence and
  uninstaller installed, Start Menu shortcut created, no config invented and
  no service registered before the network choice.
- Running-desktop guard: silent install exited 1, service PID unchanged and
  the owned desktop stayed open; guidance now says to quit from the tray
  because the window Close button only hides the app.
- Uninstall: exit 0, service, install directory and Skein firewall rules
  removed, config/identity retained.
- Fresh package install followed by `system join` produced a working
  two-sided mesh without restarting either daemon.

Windows 11 was not available for a runtime run; the same x64 artifact targets
Windows 10 and 11. Per-user installs are rejected at build time because the
VPN service is machine-wide.

## Cleanup and limitations

- Windows: helper uninstall plus scoped removal of install/data/profile and
  test directories and scheduled tasks. Final state: no Skein service,
  process, install/data directory or Skein firewall rule; sshd still running
  and SSH connected. The exact machine-PATH/non-Skein-rule digest comparison
  is unavailable because the baseline file lived in the removed test
  directory; PATH was never modified by any step.
- The disposable Ubuntu VM was powered off and its disk/image removed.
- Stale test identities remain as down peers on the daily Linux node until
  the 15-minute never-seen TTL prunes them; they do not affect traffic.
- A real three-node relay run and a Windows 11 runtime run remain
  unexercised. Loopback relay coverage is separate.

Raw screenshots, logs and JSON evidence are retained outside the repository
under `~/workspace/skein-work/phase4/`.
