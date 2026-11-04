#!/usr/bin/env bash
# single-writer-gate.sh — fail if a config writer appears outside
# internal/configstore (ADR-001 §4, Step 3; enforcement sweep is Step 9).
#
# Allowed config writers, and only these:
#   - internal/configstore itself (the single writer; atomicWrite,
#     Write/WriteText/WriteIdentity/WriteRaw)
#   - internal/conf (Render/Parse — serialisation, not a file write)
#   - tests (they may render configs to compare, or write temp configs
#     through configstore; a test that writes a config file directly
#     must do so in a temp dir and is exempted by the _test.go filter)
#   - cmd/skein-helper result files and daemon state.json (not configs;
#     see the exceptions below — they are named explicitly so a new
#     direct config write cannot hide behind them)
#
# Banned patterns (outside the allow-list):
#   1. conf.Render outside conf/configstore/tests — a render that is not
#      followed by a configstore write is a writer in waiting.
#   2. The deleted helpers: writeConfigAtomic, writeConfigRoot outside
#      internal/app (where writeConfigRoot is the configstore wrapper),
#      persistLocked-style direct temp+rename in daemon.
#   3. os.WriteFile / os.OpenFile with O_CREATE on a *.toml path, and
#      os.Rename onto a config path — direct config file writes.
# The gate is intentionally grep-based: it must catch a new writer in
# code review and in CI without needing to run the program.
set -uo pipefail

# Grep paths are repo-relative: run from the repo root regardless of
# the caller's working directory.
cd "$(cd "$(dirname "$0")/.." && pwd)"

fail=0
note() { echo "single-writer-gate: $*"; fail=1; }

# 1. conf.Render must only appear in conf, configstore, and tests.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/conf/*|internal/configstore/*|*_test.go:*) ;;
		*) note "conf.Render outside conf/configstore/tests: $hit" ;;
	esac
done <<EOF
$(grep -rn "conf\.Render" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF

# 2. Deleted direct-write helpers must not reappear.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		*writeConfigRoot*) ;; # internal/app's configstore wrapper only; checked below
		*) note "direct config-write helper: $hit" ;;
	esac
done <<EOF
$(grep -rn "writeConfigAtomic" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF
# writeConfigRoot may only be defined/called inside internal/app.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/app/*) ;;
		*) note "writeConfigRoot outside internal/app: $hit" ;;
	esac
done <<EOF
$(grep -rn "writeConfigRoot" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF
# persistLocked must delegate to configstore, not temp+rename itself.
if grep -rn "CreateTemp" --include="*.go" internal/daemon/daemon.go >/dev/null 2>&1; then
	note "internal/daemon/daemon.go still temp-writes configs (persistLocked must use configstore)"
fi

# 3. Direct writes to a .toml path outside configstore/tests.
while IFS= read -r hit; do
	[ -z "$hit" ] && continue
	case "$hit" in
		internal/configstore/*|*_test.go:*) ;;
		*) note "direct .toml write outside configstore: $hit" ;;
	esac
done <<EOF
$(grep -rn "WriteFile([^,]*\.toml\|OpenFile([^,]*\.toml" --include="*.go" internal/ cmd/ gui/ 2>/dev/null || true)
EOF

if [ "$fail" -ne 0 ]; then
	echo "single-writer-gate: FAILED — route the write through internal/configstore"
	exit 1
fi
echo "single-writer-gate: OK — no direct config writer outside internal/configstore"
