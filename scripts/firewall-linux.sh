#!/usr/bin/env bash
# Skein firewall setup for Linux.
#
# Applies exactly the rule set the daemon itself manages at startup
# (see internal/firewall/firewall_linux.go), for hosts where you want
# the rules in place before the first daemon start, or as a manual
# repair without starting the daemon:
#
#   ufw       (when `ufw status` reports active):
#     allow <port>/udp
#     allow in  on <interface>
#     allow out on <interface>
#     route allow in  on <interface>
#     route allow out on <interface>
#
#   firewalld (when `firewall-cmd --state` reports running):
#     --permanent --add-port=<port>/udp
#     --permanent --zone=trusted --add-interface=<interface>
#     --reload
#
#   iptables  (fallback, both iptables and ip6tables when present):
#     INPUT  -p udp --dport <port> -j ACCEPT
#     INPUT  -i <interface> -j ACCEPT
#     OUTPUT -o <interface> -j ACCEPT
#     FORWARD -i <interface> -j ACCEPT
#     FORWARD -o <interface> -j ACCEPT
#
# Normally you do not need this script: `skein run` (and the packaged
# service) applies the same rules itself, idempotently, and the desktop
# Security page offers a repair action. Use it to pre-seed a host, to
# fix rules after an external firewall reset, or on a host where the
# daemon cannot run the firewall tools itself.
#
# Usage:
#   sudo ./scripts/firewall-linux.sh [--port 11010] [--interface skein0]
#   sudo ./scripts/firewall-linux.sh --remove [--port 11010] [--interface skein0]
#
# The interface rules cover relayed traffic too: a relay forwards
# overlay packets between peers, so FORWARD on the overlay interface
# must be allowed, not just INPUT/OUTPUT.
set -euo pipefail

PORT=11010
IFACE=skein0
REMOVE=0

while [ $# -gt 0 ]; do
    case "$1" in
        --port)      PORT="$2"; shift 2 ;;
        --interface) IFACE="$2"; shift 2 ;;
        --remove)    REMOVE=1; shift ;;
        -h|--help)
            sed -n '2,35p' "$0"
            exit 0
            ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

if [ "$(id -u)" -ne 0 ]; then
    echo "Run as root (sudo)." >&2
    exit 1
fi

if ! [[ "$PORT" =~ ^[0-9]+$ ]] || [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
    echo "Invalid --port: $PORT" >&2
    exit 2
fi

# Manager detection mirrors the daemon: ufw active wins, then a running
# firewalld, then raw iptables as fallback.
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    MANAGER=ufw
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state 2>/dev/null | grep -q "running"; then
    MANAGER=firewalld
elif command -v iptables >/dev/null 2>&1 || command -v ip6tables >/dev/null 2>&1; then
    MANAGER=iptables
else
    echo "No firewall manager found (need ufw, firewalld, or iptables)." >&2
    exit 1
fi

echo "Firewall manager: $MANAGER (port $PORT, interface $IFACE)"

case "$MANAGER" in
    ufw)
        # `ufw allow` is idempotent; `ufw --force delete` never prompts.
        if [ "$REMOVE" -eq 1 ]; then
            ufw --force delete allow "$PORT/udp" || true
            ufw --force delete allow in on "$IFACE" || true
            ufw --force delete allow out on "$IFACE" || true
            ufw --force delete route allow in on "$IFACE" || true
            ufw --force delete route allow out on "$IFACE" || true
        else
            ufw allow "$PORT/udp"
            ufw allow in on "$IFACE"
            ufw allow out on "$IFACE"
            ufw route allow in on "$IFACE"
            ufw route allow out on "$IFACE"
        fi
        ;;
    firewalld)
        if [ "$REMOVE" -eq 1 ]; then
            firewall-cmd --permanent --remove-port="$PORT/udp" || true
            firewall-cmd --permanent --zone=trusted --remove-interface="$IFACE" || true
        else
            firewall-cmd --permanent --add-port="$PORT/udp"
            firewall-cmd --permanent --zone=trusted --add-interface="$IFACE"
        fi
        firewall-cmd --reload
        ;;
    iptables)
        for bin in iptables ip6tables; do
            command -v "$bin" >/dev/null 2>&1 || continue
            # Rules are (chain, spec...) triples; -C checks before -A so
            # re-runs never duplicate, and -D removes exactly one copy.
            add_or_del() {
                local op="$1"; shift
                local chain="$1"; shift
                if [ "$op" = "-A" ]; then
                    "$bin" -C "$chain" "$@" 2>/dev/null || "$bin" -A "$chain" "$@"
                else
                    "$bin" -D "$chain" "$@" 2>/dev/null || true
                fi
            }
            op=-A
            if [ "$REMOVE" -eq 1 ]; then op=-D; fi
            add_or_del "$op" INPUT -p udp --dport "$PORT" -j ACCEPT
            add_or_del "$op" INPUT -i "$IFACE" -j ACCEPT
            add_or_del "$op" OUTPUT -o "$IFACE" -j ACCEPT
            add_or_del "$op" FORWARD -i "$IFACE" -j ACCEPT
            add_or_del "$op" FORWARD -o "$IFACE" -j ACCEPT
        done
        ;;
esac

echo "Done."
