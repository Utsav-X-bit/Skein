#!/usr/bin/env bash
# Build the topology, run the netns integration test, and always clean up.
set -euo pipefail

if [[ $(id -u) -ne 0 ]]; then
	echo "run.sh: must run as root" >&2
	exit 1
fi

cd "$(dirname "$0")/../.."

run_id=$(head -c4 /dev/urandom | od -An -tx1 | tr -d ' \n')
prefix="skein-$(id -u)-$run_id"

cleanup() {
	./test/netns/cleanup.sh "$prefix" || true
}
trap cleanup EXIT

./test/netns/setup.sh "$prefix"
SKEIN_NETNS_PREFIX="$prefix" go test ./internal/integration -run TestNetns -v -count=1
