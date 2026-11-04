#!/usr/bin/env bash
# test-skip-report.sh — run the Go suite verbosely and print per-package
# pass/fail/skip counts, so skipped tests are visible instead of hidden.
#
# A test that always skips is a missing test, not a pass. In particular,
# two tests skip when run as root and only run unprivileged:
#   internal/api  TestGrantedUIDACL            (root bypasses the DACL)
#   internal/cli  TestLoadConfigExplainsOwnership (root can read a 0000 file)
# CI runs as a non-root user, so those two run there; a root-only run must
# never be quoted as full coverage.
#
# Usage:
#   ./scripts/test-skip-report.sh [packages...]
#   GO_TEST_FLAGS="-race" ./scripts/test-skip-report.sh ./internal/...
#
# Defaults to ./cmd/... ./internal/... . Counts are top-level tests only:
# subtests (indented "--- SKIP:" lines in go test -v output) are not counted.
# The script prints the report even when tests fail, and exits with the
# go test exit code.
set -uo pipefail

packages=("$@")
if [ "${#packages[@]}" -eq 0 ]; then
	packages=(./cmd/... ./internal/...)
fi

# shellcheck disable=SC2086
log="$(mktemp)"
trap 'rm -f "$log"' EXIT

# go test is allowed to fail: capture its code, report, then re-raise it.
set +e
go test -count=1 -v ${GO_TEST_FLAGS:-} "${packages[@]}" >"$log" 2>&1
code=$?
set -e

awk '
BEGIN {
	printf "%-60s %-6s %5s %5s %5s\n", "PACKAGE", "RESULT", "PASS", "FAIL", "SKIP"
}
/^--- PASS:/ { p++ }
/^--- FAIL:/ { f++ }
/^--- SKIP:/ {
	s++
	name = $3
	sub(/ \(.*/, "", name)
	skips = skips " " name
}
/^(ok|FAIL)[ \t]/ {
	printf "%-60s %-6s %5d %5d %5d\n", $2, $1, p, f, s
	if (skips != "") printf "  skipped:%s\n", skips
	tp += p; tf += f; ts += s; p = 0; f = 0; s = 0; skips = ""
}
/^\?[ \t]/ {
	printf "%-60s %-6s %5d %5d %5d\n", $2, "notest", 0, 0, 0
}
END {
	printf "%-60s %-6s %5d %5d %5d\n", "TOTAL", "", tp, tf, ts
}
' "$log"

exit "$code"
