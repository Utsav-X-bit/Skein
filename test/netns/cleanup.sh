#!/usr/bin/env bash
# Remove only the namespaces created by one run.
#
#   cleanup.sh skein-<uid>-<8 hex>
set -euo pipefail

prefix=${1:?usage: cleanup.sh <prefix>}
if [[ ! $prefix =~ ^skein-[0-9]+-[0-9a-f]{8}$ ]]; then
	echo "cleanup: refusing to touch names outside a validated run prefix: $prefix" >&2
	exit 1
fi

for suffix in n4 n6 ndual; do
	ns="$prefix-$suffix"
	if ip netns list | grep -qx "$ns"; then
		ip netns del "$ns"
	fi
done

echo "cleanup complete: $prefix"
