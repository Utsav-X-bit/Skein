#!/usr/bin/env bash
# Create the Skein dual-stack test topology.
#
#   setup.sh skein-<uid>-<8 hex>
#
# n4   : IPv4 only  (192.168.10.1/24 on v4a)
# n6   : IPv6 only  (fd00:10::1/64 on v6a)
# ndual: both       (192.168.10.2/24, fd00:10::2/64)
set -euo pipefail

prefix=${1:?usage: setup.sh <prefix>}
if [[ ! $prefix =~ ^skein-[0-9]+-[0-9a-f]{8}$ ]]; then
	echo "setup: invalid prefix $prefix" >&2
	exit 1
fi

n4="$prefix-n4"
n6="$prefix-n6"
nd="$prefix-ndual"

for ns in "$n4" "$n6" "$nd"; do
	if ip netns list | grep -qx "$ns"; then
		echo "setup: namespace $ns already exists" >&2
		exit 1
	fi
	ip netns add "$ns"
	ip -n "$ns" link set lo up
done

# IPv4 link between n4 and ndual.
ip link add v4a netns "$n4" type veth peer name v4b netns "$nd"
ip -n "$n4" addr add 192.168.10.1/24 dev v4a
ip -n "$nd" addr add 192.168.10.2/24 dev v4b
ip -n "$n4" link set v4a up
ip -n "$nd" link set v4b up

# IPv6 link between n6 and ndual.
ip link add v6a netns "$n6" type veth peer name v6b netns "$nd"
ip -n "$n6" addr add fd00:10::1/64 dev v6a nodad
ip -n "$nd" addr add fd00:10::2/64 dev v6b nodad
ip -n "$n6" link set v6a up
ip -n "$nd" link set v6b up

# n4 must have no IPv6 at all.
ip netns exec "$n4" sysctl -q -w net.ipv6.conf.all.disable_ipv6=1
ip netns exec "$n4" sysctl -q -w net.ipv6.conf.default.disable_ipv6=1

# Allow the underlay port in each namespace.
for ns in "$n4" "$n6" "$nd"; do
	ip netns exec "$ns" sysctl -q -w net.ipv4.ip_forward=1 || true
done

echo "topology ready: $n4 $n6 $nd"
