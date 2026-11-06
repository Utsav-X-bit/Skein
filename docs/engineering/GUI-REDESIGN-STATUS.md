# Desktop redesign — Phase 0 checkpoint

Date: 2026-11-07. Branch: `GUI-redesign`, based on `GUI` at `20a388d`.

**Verdict: PARTIALLY VERIFIED. The redesign is not complete.** Phase 1 visual design and Phase 2 redesign have not started. Phase 0 cannot be declared closed until hardware-only acceptance is run or the owner explicitly approves its deferral. No production network, system service or firewall was reset or reinstalled. Nothing was pushed.

Working bug ledger, parity map, plan and raw evidence: `~/workspace/skein-work/` (`BUGS.md`, `PARITY.md`, `PHASE-0-PLAN.md`). The linked worktree is `~/workspace/skein-work/desktop/`; the original checkout and its pre-existing edits remain intact.

## Changes

- Correct ufw route deletion in the daemon, repair rollback and shell script. Missing-rule handling reads both runner output and error instead of assuming output is embedded in the error.
- Elevate Linux CLI helper calls through pkexec; preserve bootstrap tokens in memory/stdout only. Windows lifecycle uses UAC; unelevated first setup gives explicit elevated-PowerShell guidance rather than moving a token to disk. Status remains unprivileged. Bootstrap 403 guidance distinguishes token/transport mismatch from a role grant.
- Fix the additional GUI elevation root cause: pkexec has no `--action` option. GUI/bootstrap/CLI now use the documented `pkexec <absolute helper> <verb>` grammar. One executable-path-selected polkit policy is admin-only, uncached and denied to inactive/remote sessions; no GUI environment preservation. The packaging gate checks these authorization invariants structurally.
- Add Settings network recovery: same-network rejoin uses the normal daemon join endpoint with confirmation; switching announces self-departure, resets with backup/purge and keep-identity choices, verifies Stop, then offers create/join/import. It never removes another peer. A failed Stop preserves the completed reset and exact backup path; retrying Stop does not repeat reset. No raw endpoint is presented as recovery guidance.
- Verify authoritative OS service state after every lifecycle method, including helper failure and asynchronous transitions. Settings retains service controls if config is unavailable and refreshes after success or failure. Rotation/reset use the same verified lifecycle methods.
- Add NSIS and PowerShell running-desktop guards before service/file mutations. Nothing is killed. Correct an existing NSIS string-escaping compile error. Packaging-order checks distinguish executable RMDir statements from comments.
- Set explicit Windows DACLs before path-based operations in access-file, backup, update-state and rollback writers; fix Windows access temp-file placement. Preserve Unix executable modes when copying binaries. Real Windows behavior remains unverified.
- Replace cross-process lock-test sleep/poll synchronization with a child stdout handshake and process wait. Fix an API authentication fixture to grant mutation explicitly; remove a nondeterministic map-order assertion and obsolete forwarding/binding-availability tests.
- Regenerate bindings and replace obsolete availability shims with actual generated methods/types. Setup has five methods before and after: `Reset` was removed and `Rejoin` added. `ResetWithPurge` now returns structured partial completion. Other relevant method counts remain Service 8, Peer 4, Invite 3, Update 4; generator reports 12 services / 46 methods / 29 models.
- README now has exactly two installation methods: complete Windows setup artifact, and platform-separated source builds. Prerequisites precede the source steps; Linux copies `gui/bin/skein-desktop`; helper installation remains existing-config-only. No installer binary is committed.

## Evidence

| Check | Verdict | Scope |
|---|---|---|
| `go test -count=1 ./...` | VERIFIED | Full suite passed inside disposable user/network/mount namespaces with subordinate UID/GID mappings, empty private `/etc/skein` and private `/run`. Not a claim that unprivileged host tests pass: the first host run exposed root-dependent fixtures and real `/etc/skein/access` permission dependencies. |
| CLI/services/firewall/configstore/service/app/bootstrap tests | VERIFIED | Unprivileged targeted suite; GUI services also passed with `-tags purego,skeindev`. |
| `TestLockHeldAcrossProcesses -count=20` | VERIFIED | Deterministic cross-process handshake, no real system config. |
| Frontend `npx tsc --noEmit`, `npm run build` | VERIFIED | Actual generated bindings; production bundle. Warnings remain for npm minimum-release-age config, Vite native-config compatibility and large bundle size. |
| Linux `wails3 build` | VERIFIED | Pinned Wails v3.1.0: doctor reported no issues; system-binary task and native desktop build exited 0 from a clean committed-source archive (no node_modules/build outputs copied). Native GTK/WebKit rendering also displayed the real daemon's overlay `10.144.144.9` with zero peers on Xvfb; no network/service buttons activated. Native keyboard palette activation was not established by that screenshot run. |
| Real daemon IPC | VERIFIED for reads | Actual Wails service calls loaded live status and redacted config through unchanged server-identity verification. Installed service remained active and the node/peer count unchanged. |
| GUI conflict and partial-reset recovery | PARTIALLY VERIFIED | Real browser UI with every binding intercepted fail-closed: different-network error becomes guided choice; backup survives reset/Stop failure; Retry Stop does not repeat reset; create/join choices appear. Fixture proof is not real bootstrap/switch acceptance. |
| Real ufw status before/after | VERIFIED in isolation | All Skein IPv4/IPv6 allow and forwarding rules added/removed with real ufw in private user/network/mount namespaces. No host firewall writes. This is not a production-service reset proof. |
| NSIS compile | VERIFIED | NSIS 3.11 compiled the actual script; first compile exposed and led to the quote fix. Fixture-only installer was deleted. A subsequent actual-payload compile exited 0. |
| Complete Windows installer contents | VERIFIED for packaging | Cross-built desktop, daemon, helper, CLI, pinned SHA-256-checked Wintun DLL/licence and WebView2 bootstrapper packaged. Actual-payload makensis exit 0; 7z inventory exit 0. `gui/bin/skein-desktop-amd64-installer.exe`, 31,627,898 bytes at this checkpoint. Never executed on the owner's Windows machine. |
| Windows-specific code/tests | PARTIALLY VERIFIED | `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet` typechecked affected packages/tests. No Win32 DACL/UAC/SCM runtime claim. |
| Architecture and packaging gates | VERIFIED | `scripts/collapse-gates.sh`, `scripts/packaging-assert.sh`; shell syntax checks. |
| Knowledge graph | VERIFIED | `graphify update .` rebuilt the worktree code graph, AST-only, no LLM calls. |

## Unresolved acceptance blockers (not approved deferrals)

1. Windows native runtime: protected-directory DACL regression tests, set-owner/restart on an isolated install, UAC denial/failure, actual installer open-desktop precheck, fresh install/upgrade/uninstall. No SSH verification host is configured in this environment.
2. Real privileged GUI leave → reset/purge → bootstrap create/join and same-network rejoin on a disposable install. The daily-use machines are explicitly excluded; intercepted fixtures and backend tests are not equivalent.
3. Every README installation command exactly as written from a clean state on both operating systems. Privileged package installs/copies/group/policy/service mutations were not run on daily-use systems. Cross-build/package evidence is not Windows PowerShell installation evidence.
4. Requested invitation expiry, active-invite inventory and invite-use attribution have no daemon/protocol data contract. Do not fabricate them or implement deferred F-01 revocation. Resolve the product/API treatment in Phase 1.

The complete mesh-first redesign, event-driven history, full feature parity, health/troubleshooter, accessible topology and finished visual audit are still required. This checkpoint must not be presented as that deliverable.
