#!/usr/bin/env bash
# collapse-gates.sh — ADR-001 enforcement sweep (M5 Step 9).
#
# Fails the build if a collapse invariant reappears. This is a grep
# gate, not a style check: every rule below is a design rule from
# ADR-001 whose violation would silently reopen a Phase 1 finding.
# Run from CI on every run (see .github/workflows/ci.yml); it is
# repo-root-relative so it works from any cwd.
#
# Gates:
#   1. Single writer — delegates to scripts/single-writer-gate.sh
#      (Step 3): no direct config write outside internal/configstore.
#      Documented exceptions (byte-for-byte restores and non-config
#      files, asserted in gate 6, not silently bypassed):
#        - internal/service/register.go copyFileAtomic, used ONLY by
#          RollbackDaemon to restore the exact config bytes that
#          RegisterDaemon backed up before an upgrade (Step 6).
#        - internal/daemon/system.go writeBytesAtomic, used ONLY for
#          backup directories (backupSystem) and the update-state
#          sidecar (writeUpdateState) — never the live config path.
#        - internal/daemon/connection.go savePausedState, which writes
#          state.json (pause state, Step 3), not config.
#        - Access-file writes (internal/app/grant_*.go), which write
#          the uid:role grant file, not config (Step 3).
#   2. No dashboard-spawn path — spawnDaemon/ensureDaemon/
#      daemonLogPath must stay absent (Step 8 deleted
#      internal/cli/daemon_spawn.go; Step 9 deleted the orphaned
#      daemon_* helpers). The dashboard never starts a daemon.
#   3. No helper rotate-secret/reset from GUI/CLI — runHelper with
#      either verb in gui/ or internal/cli/ (Step 8 migrated both
#      to daemon system endpoints). The helper's own dispatch and
#      the retained verbs (install/start/stop/uninstall/restart/
#      bootstrap/status/grant-user/set-owner) are unaffected.
#   4. No direct service registration outside internal/service —
#      service.Install/service.Rewrite may only be driven from
#      inside internal/service (via RegisterDaemon's svcBackend).
#      Step 12: app.InstallService/UninstallService are deleted and
#      the skeind compat alias with them; every install path goes
#      through service.RegisterDaemon via the minimal helper.
#   9. No Step 12 deletion reappears (Step 12) — no DELETE peers
#      endpoint, no API/daemon/GUI peer-removal, no CLI peers rm,
#      no skeind install/uninstall alias, no legacy root entry
#      point. session.Manager.RemovePeer is NOT banned: it is
#      internal session cleanup used by leave/rotate/stale-sweep.
#   5. No bootstrap routes in the normal daemon handler —
#      /api/v1/bootstrap/* is registered ONLY by
#      internal/daemon/bootstrap.go (Bootstrap.Handler), served
#      ONLY by bootstrap_serve_*.go, and daemon.NewBootstrap is
#      constructed ONLY by internal/app/bootstrap.go (RunBootstrap).
#      The normal api.Server.Handler/RegisterRoutes must never
#      register a bootstrap route, and GUI/CLI clients calling the
#      bootstrap endpoints are not handler registrations.
#   6. Allow-listed non-config writers stay in their documented
#      homes (see gate 1): copyFileAtomic only in
#      internal/service/register.go; writeBytesAtomic only in
#      internal/daemon/system.go and internal/daemon/update.go.
#   7. No secret logging in the new collapse code — no log call in
#      the bootstrap / system-endpoint / bootstrap-client files
#      that formats a token, secret, join string, or invite value.
#      (The daemon-wide redactor in internal/daemon/events.go is
#      defence in depth, not a licence to log secrets: the gate
#      forbids the log call itself. The one place a secret is
#      deliberately shown — the once-only join print in CLI/GUI
#      output — is stdout/display, never a log line.)
#   8. No removed helper verb from GUI/CLI (Step 10) — init/join/
#      import/update-daemon must never be called by gui/ or
#      internal/cli/; the helper's own dispatch is not a caller.
#
# Tests are exempt from gates 2-5: a test may name a banned symbol
# to assert its absence or to test the replacement path.
set -uo pipefail

# Grep paths are repo-relative: run from the repo root regardless of
# the caller's working directory.
cd "$(cd "$(dirname "$0")/.." && pwd)"

fail=0
note() { echo "collapse-gates: $*"; fail=1; }

# Gate 1: single writer (Step 3 gate, verbatim).
if ! ./scripts/single-writer-gate.sh; then
	fail=1
fi

# Gate 2: no dashboard-spawn path.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		*_test.go:*) ;;
		*) note "dashboard-spawn symbol must stay absent: $hit" ;;
	esac
done <<EOF
$(grep -rn "spawnDaemon\|ensureDaemon\|daemonLogPath" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF
if [ -e internal/cli/daemon_spawn.go ]; then
	note "internal/cli/daemon_spawn.go reappeared (deleted in Step 8)"
fi

# Gate 3: no helper rotate-secret/reset from GUI/CLI.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		*_test.go:*) ;;
		*) note "helper rotate-secret/reset from GUI/CLI must stay absent: $hit" ;;
	esac
done <<EOF
$(grep -rn 'runHelper[^;]*"rotate-secret"\|runHelper[^;]*"reset"' --include="*.go" gui/ internal/cli/ 2>/dev/null || true)
EOF

# Gate 8 (Step 10): no removed helper verb from GUI/CLI either — the
# helper's own dispatch and the retained verbs (install/start/stop/
# restart/uninstall/bootstrap/status/grant-user/set-owner/revoke-user)
# are unaffected; only GUI/CLI callers are checked, tests exempt.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		*_test.go:*) ;;
		*) note "removed helper verb called from GUI/CLI (use the daemon endpoint): $hit" ;;
	esac
done <<EOF
$(grep -rn 'runHelper[^;]*"init"\|runHelper[^;]*"join"\|runHelper[^;]*"import"\|runHelper[^;]*"update-daemon"\|runSystemHelper("init"\|runSystemHelper("join"\|runSystemHelper("import"\|runSystemHelper("update-daemon"' --include="*.go" gui/ internal/cli/ 2>/dev/null || true)
EOF

# Gate 4a: no direct service.Install/Rewrite outside internal/service.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/service/*|*_test.go:*) ;;
		*) note "direct service.Install/Rewrite outside internal/service: $hit" ;;
	esac
done <<EOF
$(grep -rn "service\.Install(\|service\.Rewrite(" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF
# Gate 4b (Step 12): app.InstallService/UninstallService must be
# absent entirely — the functions and the skeind alias are deleted.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		*_test.go:*) ;;
		*) note "app.InstallService/UninstallService must stay deleted (Step 12): $hit" ;;
	esac
done <<EOF
$(grep -rn "app\.InstallService(\|app\.UninstallService(\|func InstallService(\|func UninstallService(" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF

# Gate 9 (Step 12): no deleted surface reappears. Tests exempt
# (a test may assert absence); session.Manager.RemovePeer is not
# matched by these patterns.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		*_test.go:*) ;;
		*) note "Step 12 deletion reappeared: $hit" ;;
	esac
done <<EOF
$(grep -rn 'DELETE /api/v1/peers/{id}' --include="*.go" internal/ cmd/ gui/ 2>/dev/null | grep -v 'Step 12' || true)
$(grep -rn 'func (d \*Daemon) RemovePeer' --include="*.go" internal/daemon/ 2>/dev/null || true)
$(grep -rn 'PeerService) Remove(' --include="*.go" gui/ 2>/dev/null || true)
$(grep -rn 'PeerService\.Remove(' gui/frontend/src/ 2>/dev/null || true)
$(grep -rn 'peers rm' --include="*.go" internal/cli/ 2>/dev/null | grep -v "Step 12" || true)
$(grep -rn '"install-service"\|"uninstall-service"' --include="*.go" cmd/skeind/ 2>/dev/null || true)
$(grep -rn '\bInitRoot(\|\bJoinRoot(\|\bRotateRoot(\|\bImportRoot(\|\bResetRoot(\|\bUpdateRoot(' --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF

# Gate 5a: no bootstrap route registered outside daemon/bootstrap.go.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/daemon/bootstrap.go:*|*_test.go:*) ;;
		*) note "bootstrap route registered outside daemon/bootstrap.go: $hit" ;;
	esac
done <<EOF
$(grep -rn 'HandleFunc.*"[^"]*bootstrap' --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF
# Gate 5b: daemon.NewBootstrap constructed only by app/bootstrap.go.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/daemon/bootstrap.go:*|internal/app/bootstrap.go:*|*_test.go:*) ;;
		*) note "daemon.NewBootstrap constructed outside app/bootstrap.go: $hit" ;;
	esac
done <<EOF
$(grep -rn "daemon\.NewBootstrap(" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF
# Gate 5c: Bootstrap.Handler served only by bootstrap_serve_*.go.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/daemon/bootstrap_serve_*.go:*|*_test.go:*) ;;
		*) note "Bootstrap.Handler used outside bootstrap_serve_*.go: $hit" ;;
	esac
done <<EOF
$(grep -rn "b\.Handler()\|bs\.Handler()\|Bootstrap) Handler" --include="*.go" internal/ cmd/ gui/ 2>/dev/null | grep -v "func (b \*Bootstrap) Handler" || true)
EOF

# Gate 6: allow-listed non-config writers stay in their documented homes.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/service/register.go:*|*_test.go:*) ;;
		*) note "copyFileAtomic outside internal/service/register.go (rollback restore only): $hit" ;;
	esac
done <<EOF
$(grep -rn "copyFileAtomic" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/daemon/system.go:*|internal/daemon/update.go:*|*_test.go:*) ;;
		*) note "writeBytesAtomic outside daemon backup/update-state (never live config): $hit" ;;
	esac
done <<EOF
$(grep -rn "writeBytesAtomic" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF

# Gate 7: no secret logging in the collapse surface.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		*_test.go:*) ;;
		*) note "secret material in a log call (bootstrap/system surface): $hit" ;;
	esac
done <<EOF
$(grep -rn 'log\.\(Printf\|Println\|Print\)([^)]*\(token\|Token\|secret\|Secret\|joinString\|join_string\|invite\|Invite\)' --include="*.go" internal/daemon/bootstrap*.go internal/app/bootstrap*.go internal/bootstrap/ gui/services/bootstrap*.go internal/cli/system.go internal/api/handlers_system.go 2>/dev/null || true)
EOF

if [ "$fail" -ne 0 ]; then
	echo "collapse-gates: FAILED — see violations above"
	exit 1
fi
echo "collapse-gates: OK — single writer, no spawn path, no helper rotate/reset, no direct service registration, no bootstrap route in the normal handler, allow-listed writers in place, no secret logging, no removed helper verb from GUI/CLI, no Step 12 deletion reappeared"
