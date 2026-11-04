# M9 — Final acceptance checklist (DRAFT)

- **Verdict: NOT RELEASE READY.** This draft exists so M9 can be run, not so
  it can be claimed. The verdict upgrades only when every item in §2 passes
  on the machines and CI runs named there. Nothing in this branch — VM unit
  tests, cross-compilation, or static gates — is a substitute for those
  runs.
- **Branch:** `collapse/adr-001`, base `f6bc79b`. All work uncommitted, for
  review. Upstream `main` untouched.
- **How to use this document:** each item states its exact test and its
  pass criterion. Record the date, machine, commit, and log for every
  pass; a pass without a log is not a pass. Statuses stay distinct:
  VERIFIED / PARTIALLY VERIFIED / BLOCKED / FAILED.

## 1. VM-verified (evidence on this VM)

These passed on the audit VM and are the regression baseline M9 must not
regress. Suite numbers are root runs via `scripts/test-skip-report.sh
./cmd/... ./internal/...` unless stated otherwise.

| Item | Evidence |
|---|---|
| Full Go suite | M7 baseline: **367 pass / 10 fail / 6 skip = 383**. The 10 fails are unchanged across M3–M7: `internal/link` 6 + `internal/integration` 4, sandbox UDP `sendto: operation not permitted` — environmental (a plain Python UDP loopback fails identically), not a code verdict. Any new failure beyond those 10 is a regression. |
| Unprivileged suite | M3 Tier 0: `nobody` run 270 pass / 11 fail / 4 skip = 285 at that time (root-skipped `TestGrantedUIDACL` and `TestLoadConfigExplainsOwnership` run unprivileged; the former fails only because `setfacl` was absent — CI installs the `acl` package first). |
| Fuzz | `FuzzParse` (`internal/wire`): bounded 60 s run passes, no crash (M3: 6,066 execs, 2 new interesting inputs). CI runs the same 60 s bound. |
| `go vet`, `gofmt`, staticcheck | Clean on every phase (M3–M7). `gofmt -l` clean for all touched trees. |
| Collapse gates | `scripts/collapse-gates.sh` passes: single writer, no daemon spawn from GUI/CLI, no removed helper verb called from GUI/CLI, no direct service registration, no bootstrap route in the normal handler, allow-listed writers, no secret logging, no reappeared Step 12 deletion (gate 9). |
| Packaging assertions | `scripts/packaging-assert.sh` passes: NSIS ships desktop + `skeind.exe` + `skein-helper.exe` + `skein.exe` + `wintun.dll` + licence; nfpm ships desktop + `skeind` + `skein-helper` + `skein` + unit + group + polkit; uninstall order (helper first, stop-then-delete) and purge behaviour (no purge by default; Windows purge moves config to `.bak`). |
| IPC authorisation | Endpoint × role × transport matrix test passes (Step 1); `POST /api/v1/invites/network` is `secret` role, kernel transport only. |
| Single listener + server identity | `StartControlListeners` is the single listener, fatal on bind failure; `VerifyPipeServer`/`VerifyUnixServer` have callers on all four dial paths (GUI Unix/Windows, CLI Unix/Windows). |
| Single writer + lock | `internal/configstore`: daemon holds the lifetime lock; helper/CLI writes take a brief lock; `<config>.lock` beside the config file. |
| Bootstrap token | Hash-only (SHA-256) state, 256-bit `crypto/rand` token, 10-minute expiry, minted verb, uid/SID binding, 5 failed attempts burn the token, bearer callers 403, token marked used before config work, bootstrap-mode route table (socket/pipe only). Unit + daemon tests pass. |
| Service identity + upgrade | `RegisterDaemon` (`skeind` only, never PATH/helper dir); upgrade keeps `<binary>.old` + timestamped config backup, never deleted on failure; rollback restores both. |
| System endpoints | init/join/import/rotate/reset/leave/update-check/update-apply/grants-reload pass on fake backends (Step 7); update refuses downgrades (new version strictly greater), verifies ed25519 + SHA-256 + `key_id` + signed `valid_until`, basename-only staging, detached apply with verified rollback; key-rotation map (`v1` current, overlap + retirement) passes (M6). |
| Protocol fixes (M4) | TCP accept-side handshake, STATE >8 candidates, epoch monotonicity, dialer validation, punch-nonce verification, route-withdrawal propagation, rekey/epoch-expiry enforcement, leave tombstone gossip (STATE v5, self-announcement trusted, re-gossip needs two origins, 180 s retention, live-STATE clears tombstone, rejoin needs fresh invite). All on fake/loopback harnesses — live-mesh proof is §2, not this section. |
| Security (M6) | Release builds ignore `SKEIN_HELPER`/`SKEIN_CONTROL_*`/`SKEIN_DAEMON_IMAGE`/`SKEIN_HELPER_NO_PKEXEC` (dev-tag `skeindev` builds honour them; never ship one); CLI release path uses the packaged helper only; log redaction (RawURL + Std forms + join strings); O_EXCL + 0600 identity; pipe first-instance anti-squat; Wintun 0.14.1 SHA-256 pin on fetch. |
| GUI (M7, VM half) | Frontend `tsc --noEmit` passes and `vite build` succeeds on a clean copy; esbuild parse OK for all touched TS/TSX files; GUI services tests pass (`-tags purego`, 8/8 at M6); Leave/About/Settings screens written with runtime guards that disable binding-gated calls legibly until bindings regenerate; F-07 guard (Windows GUI refuses non-nil helper payloads, so no secret ever travels `%TEMP%`). Wails build blocked on this VM (no gtk4) — GUI runtime is §2. |

## 2. Still open — the M9 run (machines + CI)

### 2a. First GitHub Actions run (CI)

- [ ] The `test` job runs green on a real runner, including: skip-report
  step (non-root runner, `acl` installed, root-skipped tests run),
  collapse-gates step (including gate 9), packaging-assert step, 60 s
  `FuzzParse` step, and the new M8 steps — govulncheck
  (`golang.org/x/vuln/cmd/govulncheck@v1.9.0`, pinned), gitleaks
  working-tree scan (`gitleaks/v8@v8.30.1`, `--no-git`; see note below),
  GUI services tests (`-tags purego` and `-tags purego,skeindev`),
  and the release-artefact `skeindev` absence check.
  Pass criterion: green run + a log showing skip counts, not just "ok".
- [ ] **Before that green run, bump the Go toolchain to >= 1.26.10
  (go.mod).** M8 proved (2026-11-04, local runs): under go1.26.0,
  govulncheck reports 28 reachable stdlib vulnerabilities (crypto/x509,
  net/http, crypto/tls, net/textproto, net/url, encoding/asn1, net),
  all fixed by go1.26.10; under go1.26.10 it reports 0 reachable
  findings (3 unreachable module findings remain, non-gating). The
  toolchain bump is a separate one-line change for review, not smuggled
  into M8 — do not weaken the govulncheck step to make the first run
  pass instead.
- [ ] Secret-scanning scope note: the working-tree scan is deliberately
  `--no-git`. A full-history scan is a separate remediation: the
  November 2026 GitGuardian history (Skein commit `7c88ad8`, two Generic
  High Entropy Secrets) means a history scan may fail on pre-existing
  history. Gate PR CI on the working tree now; schedule history scrub /
  allowlist as its own item with rotation confirmed first. Do not weaken
  the working-tree scan to make history pass, and do not claim "secret
  scanning passes" for history until that run is green.
- [ ] Release artefacts: built without `-tags skeindev` (assert the build
  command / binary behaviour); artefacts signed by the release pipeline
  (signing is a pipeline item, not an in-repo change — record the
  mechanism and key holder before calling this done).

### 2b. Linux machine (user's Linux box)

- [ ] Fresh install: install the nfpm package; `getent group skein`
  exists; unit `ExecStart` is `/usr/local/bin/skeind run -c
  /etc/skein/skein.toml`; socket is `/run/skein/control.sock` 0660
  root:skein after bootstrap.
- [ ] Fresh-install end-to-end: bootstrap → mesh → rotate → reset →
  uninstall → reinstall → re-bootstrap, on a real install (not a dev
  tree). Each step leaves no orphaned service, unit, or firewall rule.
- [ ] Bootstrap on a real node: helper `bootstrap --verb init` mints the
  token, daemon in bootstrap mode serves only socket + bootstrap
  endpoints, token use grants `secret`, second bootstrap attempt fails,
  expired/burned token fails.
- [ ] Reboot + reconnect: service restarts, mesh reforms, dashboard
  attaches over the socket without `-c` or `sudo`.
- [ ] Upgrade: package upgrade adopts the new binary with no unit
  rewrite; `<binary>.old` + config backup exist; a failing upgrade
  restores both.
- [ ] Uninstall: without purge keeps `/etc/skein` +
  `/var/lib/skein/backups`; with purge (daemon reset endpoint) removes
  config as documented; helper-uninstall failure aborts uninstall
  with nothing deleted.
- [ ] Live revoke: `revoke-user` against a running daemon removes the
  grant, re-ACLs the socket, and reloads grants without a restart; the
  revoked uid's next control call is denied (403) and audited.
- [ ] Live rotate → eviction (mesh proof): two nodes, one departs, the
  other rotates and restarts, the departed node tries to rejoin with
  the old join — it must fail the PSK check; the remaining node
  rejoins with the new join and works.
- [ ] Packaged CLI helper path: from a directory containing a planted
  `skein-helper`, `skein system restart` uses `/usr/libexec/skein-helper`,
  never the planted one.
- [ ] Daemon env follow-up (M8, security): decide and implement — either
  gate `SKEIN_NO_FIREWALL` and `SKEIN_STATE_FILE` behind `skeindev`
  like the GUI/CLI security variables (release builds ignore them), or
  bless them explicitly as operator variables in the README with the
  risk stated. Until then, packaged installs must leave both unset,
  and no doc may present them as dev-only.

### 2c. Windows machine (user's Windows box)

- [ ] Real `makensis` NSIS build: installer compiles, ships exactly
  desktop + `skeind.exe` + `skein-helper.exe` + `skein.exe` +
  `wintun.dll` + licence; `sc qc skein` binPath is the installed
  `skeind.exe`.
- [ ] Fresh install + bootstrap + reboot + upgrade + uninstall (±purge)
  on a real NSIS install, mirroring §2b: uninstall runs helper
  `uninstall` first (stop-then-delete) and aborts with nothing deleted
  if the helper fails; purge moves `%ProgramData%\Skein` to `.bak`.
- [ ] Pipe-DACL rebuild: after `grant-user`/`set-owner`/`revoke-user`,
  the control-pipe DACL names exactly the current grants; a revoked
  SID's control call is denied. (Today the API denial is the
  load-bearing check; the DACL naming a revoked SID is the known
  residual — this test closes it or records it as still open.)
- [ ] Live revoke + Windows stop-then-delete (Step 10 items): revoke
  against a running daemon on Windows; uninstall stops the service
  before deleting it.
- [ ] Windows update cycle: real install → signed-manifest update
  (with the key-rotation map) → rollback on a failing health check,
  on a real service; confirm the manifest-open TOCTOU caveat
  (Lstat, not O_NOFOLLOW) in practice.
- [ ] Windows pipe deadlines: confirm whether stalled clients can wedge
  the control plane despite the 64-connection cap, before adding
  Unix-equivalent timeouts.
- [ ] Portable-layout decision: either bless only the packaged layout
  in the docs (portable use at your own risk, never elevated) or
  implement the Windows ownership check with a real test. Until then:
  portable helper plant test (marker `skein-helper.exe` beside the
  GUI/CLI in a user-writable directory must never run elevated) and
  portable Wintun plant test (which DLL the service loads, via
  Process Explorer / `Get-Module`) must both be run and recorded —
  expected safe behaviour is the packaged Program Files helper / the
  verified install-dir DLL, or a loud failure, never the marker.
- [ ] F-07 pipe fix (separate change, Windows machine only): helper
  `--pipe` case for retained verbs + GUI-created named pipe with
  server-identity treatment, `%TEMP%` result-file path deleted for
  those verbs. Test: a same-user process polling `%TEMP%` during a
  service action sees no result file; a planted result file is never
  read. Until it lands, the documented residual stands: result files
  exist during service actions and are same-user readable, but carry
  no secret.

### 2d. GUI machine (Linux + Windows, Wails toolchain)

- [ ] Regenerate bindings: Wails build exports `PeerService.LeaveNetwork`,
  `UpdateService.CheckManifest` / `Apply` / `ReloadGrants`,
  `ServiceService.InstallWithDaemon` / `UninstallWithPurge`, and model
  types `LeaveResult` / `UpdateCheckResult` / `UpdateApplyResult`;
  then `tsc --noEmit` passes with no `IMPORT_IS_UNDEFINED` warnings.
- [ ] Click-through: Peers → Leave (confirm dialog, toast with counts,
  `propagated` true and false both exercised); About → Check manifest
  (bad signature → error; downgrade manifest → 409, not "no update"),
  Apply (poll → applied), failing apply (poll → rolled back + backup
  path shown); Settings Install/Uninstall/Repair each prompt once for
  elevation and report the three-state kind on denial (403) vs
  daemon-down; Repair failure mid-sequence names its step.
- [ ] Runtime GUI: Leave button enabled (no binding notice); About
  version footer matches the built version (not `dev` unless
  unstamped); no `%TEMP%` `skein-helper-request-*.json` file is ever
  created by the GUI.
- [ ] Post-regeneration cleanups: replace `lib/bindings.ts` local result
  types with generated model types; wire `InstallWithDaemon` and
  `UninstallWithPurge` in Settings; drop the two binding-limit notes
  from the trust copy once bound.

### 2e. Live mesh / NAT / CGNAT (real hosts, not simulation)

- [ ] Live 4–5-node mesh (the 5-node limit stays explicit until a larger
  design is implemented and tested): per-pair data-plane proof (ping +
  checksummed TCP, both directions), add/leave/concurrent-join/restart/
  kill-9/relay-failure, convergence/failover/recovery timing.
- [ ] NAT ladder, labelled simulated where simulated: full-cone →
  restricted → symmetric → double-NAT; record measured
  direct/relayed/disconnected per pair. Simulated CGNAT is never
  presented as real ISP CGNAT — a real CGNAT pair test is its own
  item if the user's ISP provides one.
- [ ] Linux ↔ Windows mixed mesh: same per-pair proof as above, both
  directions, with the Windows node installed via NSIS (not portable).
- [ ] Performance: iperf3 benchmarks with a without-Skein baseline on
  the same hosts, dated and logged; no number is quoted without
  date/commit/machine/topology/logs.

## 3. Items explicitly NOT claimed by this branch

- Windows runtime of any kind (all Windows evidence here is
  cross-compilation only).
- Live mesh, live NAT/CGNAT, live install/upgrade/uninstall on either
  platform.
- GUI runtime, binding regeneration, or Wails build success.
- First GitHub Actions run of the current workflow.
- Real `makensis` / `nfpm` package builds and per-distro dependency
  resolution.
- Per-peer invitation model (the product fix for F-01 beyond rotation);
  rotation remains the eviction mechanism, with the restart +
  every-node + new-join-secrecy conditions.
- Automatic updates (no version-feed endpoint by design).
- Secret history scrub / rotation confirmations (DanyAPI Google key,
  Neon credential in `mohakps/riptide`, Skein GitGuardian secrets) —
  those are the user's security stack, tracked separately, not M9
  items, but release must not ship while a live credential from this
  repo's history is unrotated.

## 4. Verdict rule

M9 passes, and only then may the status change from NOT RELEASE READY,
when: §2a is green, §2b and §2c are fully passed with logs, §2d
click-through passes on both platforms, and §2e live-mesh proof exists
for 4–5 nodes including a mixed Linux ↔ Windows mesh. Any FAILED item
keeps the verdict at NOT RELEASE READY with the item named. A
PARTIALLY VERIFIED item must name exactly which sub-test passed and
which is still open.
