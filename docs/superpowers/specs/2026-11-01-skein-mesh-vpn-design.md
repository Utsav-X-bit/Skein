# Skein — Mesh VPN Design Spec

**Date:** 2026-11-01
**Status:** Draft for review
**Module:** `github.com/utsavgupta5467/skein`

---

## 1. Summary

Skein is a userspace L3 overlay network for 2–5 personal devices. Every node gets a
stable overlay address that is independent of its underlay address family. Nodes
encapsulate IP packets in UDP, encrypt them end-to-end with Noise, and forward
between each other when a direct path is impossible.

There is no privileged node. Any node with both address families can act as a
bridge. There is no external relay, no coordination server, and no hole punching.

Target platforms: **Linux** and **Windows**.

---

## 2. Problem statement

Two nodes can exchange packets directly only if there exists an address family in
which both hold an address **and at least one side is inbound-reachable**.

| State | Meaning |
|---|---|
| `PUBLIC` | globally routable, accepts inbound (firewall permitting) |
| `NAT` | holds the family, inbound blocked, can dial out |
| `NONE` | no address in that family |

| A \ B | v4 PUBLIC | v4 NAT | v6 PUBLIC |
|---|---|---|---|
| **v4 PUBLIC** | direct | direct (NAT side dials) | **impossible** |
| **v4 NAT** | direct (NAT side dials) | **impossible** | **impossible** |
| **v6 PUBLIC** | **impossible** | **impossible** | direct (pinhole) |

Two cases are structurally unreachable:

1. **Family mismatch** — a v4-only and a v6-only node share no L3 protocol.
2. **Double NAT** — two CGNAT nodes; neither can accept, and hole punching through
   two endpoint-dependent NATs costs ~170,000 probes per side (≈28 minutes) and
   exhausts carrier session tables.

Skein addresses case 1 by **forwarding through a dual-stack member**. Case 2 is out
of scope and is documented as a known limit.

---

## 3. Goals and non-goals

### Goals

- 2–5 devices form a full mesh with one overlay address each.
- Overlay is dual-stack: IPv4 and IPv6 ULA, both independent of the underlay.
- End-to-end encryption; a forwarding node sees no plaintext.
- Zero privileged nodes; any dual-stack member can bridge.
- Cross-platform: Linux and Windows, single static binary each.
- Full CLI plus a stable local API for an external UI.
- The v4-only ↔ v6-only failure is **demonstrable** (netns test) and the fix
  (bridge) is **demonstrable** too.

### Non-goals (v1)

- External relay service (Oracle/VPS). Not built, not configured.
- UDP hole punching, STUN, birthday-paradox port prediction.
- TCP or WSS transport. The link layer is an interface so this drops in later.
- Routing protocols for >10 nodes. Full mesh only.
- NAT64/DNS64 awareness.
- Multi-tenant ACLs, audit logging, per-peer key revocation UX.
- The UI itself. Only the API it consumes.

---

## 4. Architecture

### 4.1 Process model

Two binaries. The daemon owns the dataplane and the API; the CLI and any UI are
both clients of that API. There is exactly one source of truth.

```
┌──────────┐   ┌──────────┐   ┌──────────┐
│  skein   │   │   UI     │   │  other   │
│  (CLI)   │   │ (yours)  │   │ clients  │
└────┬─────┘   └────┬─────┘   └────┬─────┘
     └──────────────┼──────────────┘
                    │ HTTP/JSON + SSE on 127.0.0.1:11011
             ┌──────┴───────┐
             │    skeind    │
             │  ┌────────┐  │
             │  │  API   │  │
             │  ├────────┤  │
             │  │ peers  │  │
             │  │ routes │  │
             │  │ session│  │
             │  │  wire  │  │
             │  ├────────┤  │
             │  │  link  │  │  ← interface, UDP now, TCP later
             │  ├────────┤  │
             │  │  tun   │  │  ← per-OS
             │  └────────┘  │
             └──────────────┘
```

`skeind` requires root (Linux) or Administrator (Windows) because it creates a TUN
device and installs routes. `skein` and the UI do not.

### 4.2 Layers

```
application            ping 10.144.144.3
kernel routing         ip route add 10.144.144.0/24 dev skein0
overlay                dst IP → peer ID
session                Noise AEAD, keyed by peer ID, end-to-end
transport              [type][to_next][to_final][from][ttl] + opaque blob
link                   UDP [::]:11010, dual-stack socket
underlay               v4, v6, or both
```

The session is keyed by **peer ID, not by address**. Swapping a transport address
never touches crypto.

### 4.3 Components

| Package | Responsibility | Privileged | Platform |
|---|---|---|---|
| `cmd/skeind` | daemon wiring, lifecycle | yes | both |
| `cmd/skein` | CLI client | no | both |
| `internal/api` | HTTP/JSON + SSE server | no | both |
| `internal/tun` | create TUN device | yes | per-OS file |
| `internal/netcfg` | set address, route, MTU | yes | per-OS file |
| `internal/link` | `Link` interface; UDP implementation | yes | both |
| `internal/wire` | header encode/decode, framing | no | both |
| `internal/session` | Noise IK handshake, AEAD, replay window | no | both |
| `internal/peer` | peer table, identity, state machine | no | both |
| `internal/route` | reachability table, bridge selection | no | both |
| `internal/path` | candidate enumeration, probing, RTT | no | both |
| `internal/discovery` | static config, invite string, mDNS | no | both |
| `internal/conf` | TOML load/validate, keygen | no | both |

Only `tun`, `netcfg`, and `link` are platform-specific. Everything else is pure Go
logic and unit-testable without privileges.

### 4.4 Platform matrix

| Concern | Linux | Windows |
|---|---|---|
| TUN device | `/dev/net/tun` via `golang.zx2c4.com/wireguard/tun` | Wintun via the same package; ships `wintun.dll` |
| Interface name | `skein0` | `Skein` |
| Address/route/MTU | `ip addr` / `ip route` / `ip link` | `netsh interface ipv4` / `ipv6` |
| Privilege | root, or `CAP_NET_ADMIN` | Administrator |
| Config dir | `$XDG_CONFIG_HOME/skein` → `~/.config/skein` | `%APPDATA%\skein` |
| Service | systemd unit | Windows Service |
| mDNS | `grandcat/zeroconf` | `grandcat/zeroconf` |

`golang.zx2c4.com/wireguard/tun` is used **only** for device creation. All VPN
logic — framing, crypto, routing, discovery — is ours. `wintun.dll` must sit next
to `skeind.exe` on Windows; the release archive includes it.

### 4.5 Link interface

`link` is defined as an interface on day one so a TCP transport can be added
without touching `session`, `path`, `wire`, or `route`.

```go
// Link carries opaque frames to a set of peer addresses.
type Link interface {
    Send(to netip.AddrPort, frame []byte) error
    Recv(buf []byte) (n int, from netip.AddrPort, err error)
    // Observe returns the reflexive address this node appears as to the peer,
    // when the transport can report it (STUN-lite). May return an error.
    Observe() (netip.AddrPort, error)
    LocalAddrs() []netip.AddrPort
    Close() error
}
```

One UDP implementation serves every peer from a single dual-stack socket. A TCP
implementation would keep a connection per peer behind the same interface; that
connection map is the only extra work, and it does not exist in v1.

### 4.6 Wire format

All multi-byte integers are big-endian.

```
transport header (26 bytes, plaintext)
  [type:1][to_next:8][to_final:8][from:8][ttl:1]

type:
  1 HANDSHAKE   payload = Noise IK message
  2 DATA        payload = [nonce:8][AEAD(...)]
  3 PING        payload = [seq:4]
  4 PONG        payload = [seq:4][echo_us:8]
  5 STATE       payload = [count:2][entry * count]

DATA plaintext
  [IP packet]                       version nibble decides v4 vs v6

AEAD associated data
  to_final || from                  binds header to ciphertext

STATE entry
  [id:8][pubkey:32][flags:1][addr_count:1][addr...][direct_rtt_ms:2][hops:1]

  flags: bit0 = has_v4, bit1 = has_v6, bit2 = has_direct_path
  addr:  [fam:1][port:2][ip:4 or 16]
```

`to_next` is where this hop goes. `to_final` is where the session terminates. For
a direct path they are equal; for a forwarded path they differ and the forwarding
node rewrites only `to_next`.

Peer IDs are the first 8 bytes of `SHA-256(static public key)`, rendered as hex.

**Overhead:** 26 (header) + 8 (nonce) + 16 (AEAD tag) = **50 bytes** per packet.

### 4.7 Session

- Noise **IK** handshake. Initiator knows the responder's static public key from
  config or gossip.
- The `network_secret` is used as a **PSK modifier**. Correct pubkey with the wrong
  secret fails the handshake.
- Replay protection: 64-bit counter plus a sliding window.
- Rekey after 2³² packets or 1 hour, whichever comes first.
- One session per peer, independent of which transport path is active.

### 4.8 Path selection

For each peer, enumerate candidates from the peer table filtered by families this
node actually holds, probe all of them in parallel, keep the lowest-RTT winner.

| Candidate | Source |
|---|---|
| global IPv6 | config, gossip |
| public IPv4 | config, gossip |
| LAN address | mDNS |

Both sides probe simultaneously. Whichever handshake completes first wins. This is
the mechanism that makes "one public side is enough" work with no traversal logic.

Per-peer state machine: `down → probing → up → degraded → probing`.

### 4.9 Routing and forwarding

Each node maintains a reachability table:

```
route[dest] = { next_hop, cost_ms, hops }
```

- Direct neighbours: `{ self, measured_rtt, 1 }`.
- Learned: for each advertised `B → X` with cost `c` and hops `h`, candidate is
  `{ B, rtt(A,B) + c, h + 1 }`. Lowest cost wins.
- Split horizon: never advertise a route back to the node that provided it.
- Maximum 3 hops; longer paths are dropped.
- Recomputed on any STATE update or RTT change.

**Forwarding a packet:**

1. Receive a frame where `to_final != self`.
2. Look up `route[to_final]`. If absent, drop and count.
3. Decrement `ttl`; if zero, drop and count.
4. Resolve `next_hop` to its current best transport address.
5. Rewrite `to_next` and send. **The payload is never decrypted.**

A forwarding node therefore sees `from`, `to_final`, and packet sizes — nothing
else. Tampering with the header breaks the AEAD associated data at the destination
and the packet is dropped.

With ≤5 nodes, the table is tiny and Bellman-Ford-style relaxation converges in at
most two rounds.

### 4.10 Discovery

| Mechanism | Effort | Notes |
|---|---|---|
| Static `[[peer]]` block | paste | always available |
| Invite string `skein:1:<base64>` | paste once | id + pubkey + addresses |
| mDNS `_skein._udp.local` | zero | same LAN, TXT carries the invite blob |
| STATE gossip | zero | transitive; one handshake teaches you everyone |

Gossip is merge-only and TTL-limited. No node is authoritative. A node ID that
appears with two different public keys is rejected and logged.

### 4.11 Addressing

| | IPv4 | IPv6 |
|---|---|---|
| Overlay prefix | `10.144.144.0/24` | `fd00:6d65:7368::/64` |
| Per-node | `10.144.144.N` | `fd00:6d65:7368::N` |
| Interface | `skein0` / `Skein` | same device |

Both are configurable. If `overlay_ipv6` is omitted the node advertises no IPv6
overlay address and is unreachable over the IPv6 overlay; its IPv4 overlay is
unaffected. Overlay family is independent of underlay family — that is the entire
point of the design.

### 4.12 MTU

Inner MTU **1280**. Worst case outer:

```
1280 + 50 (header+nonce+tag) + 8 (UDP) + 40 (IPv6) = 1378 < 1500
1280 + 50               + 8        + 20 (IPv4) = 1358 < 1500
```

Because the TUN MTU *is* 1280, the kernel never hands up a larger packet, so v1
needs no fragmentation at all. This breaks only if the underlay path MTU falls
below 1378; documented, not handled.

### 4.13 Keepalive and failover

| Event | Action |
|---|---|
| every 15 s | PING on the active path (holds NAT mappings) |
| 45 s silent | mark peer down, re-probe all candidates |
| path dies, peer alive | re-probe, keep the session, resume on any answering candidate |
| handshake failure | backoff 1 s → 2 s → 4 s → cap 30 s |
| route lost | recompute; existing sessions survive a route change |

---

## 5. Local API

Bound to `127.0.0.1` only. `Authorization: Bearer <token>` from config. This is the
contract the UI is built against; the UI is not designed here.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/status` | node ID, overlay addresses, uptime, version, listen port |
| `GET` | `/api/v1/peers` | peer table: id, state, path kind, rtt, rx/tx, last seen |
| `GET` | `/api/v1/routes` | destination, next hop, cost, hops |
| `GET` | `/api/v1/config` | effective config, secret redacted |
| `POST` | `/api/v1/peers` | add or update a peer from an invite string or JSON |
| `DELETE` | `/api/v1/peers/{id}` | remove a peer |
| `POST` | `/api/v1/peers/{id}/probe` | force re-probe of all candidates |
| `GET` | `/api/v1/events` | SSE stream: peer up/down, route change, error |

Example `GET /api/v1/peers`:

```json
[
  {"id":"a3f9c2e1b7d0456a","state":"up","path":"v6","rtt_ms":12.4,
   "overlay_ipv4":"10.144.144.1","rx_bytes":1200345,"tx_bytes":901222,
   "last_seen_ms":2000,"direct":true}
]
```

---

## 6. CLI

```
skeind run -c <path>          start the daemon (root / Administrator)
skeind install-service        register systemd unit or Windows service
skeind uninstall-service

skein genkey                  print a keypair and node ID
skein invite                  print skein:1:<base64> for this node
skein status                  overlay addresses, peer count, uptime
skein peers [list|add|rm]     manage peers
skein routes                  print the reachability table
skein probe <peer-id>         force a re-probe
skein ping <overlay-ip>       ICMP through the overlay (wraps the system ping)
skein version
```

Every command except `genkey` and `invite` talks to the running daemon over the
local API. `genkey` and `invite` work offline against the config file.

---

## 7. Configuration

TOML, loaded by `skeind`.

```toml
[node]
overlay_ipv4   = "10.144.144.2"
overlay_ipv6   = "fd00:6d65:7368::2"   # optional
listen_port    = 11010
network_secret = "change-me"
mtu            = 1280
interface      = "skein0"

[api]
listen = "127.0.0.1:11011"
token  = "generated-on-first-run"

[discovery]
mdns = true

[[peer]]
id     = "a3f9c2e1b7d0456a"
pubkey = "base64..."
addrs  = ["udp://[2406:b400:72:4f1e::1]:11010", "udp://203.0.113.5:11010"]
```

Validation: overlay addresses must be inside the configured prefixes and unique;
`listen_port` in 1–65535; at least one peer or `mdns = true`; `network_secret` at
least 16 bytes.

The static private key is stored separately from the config, in
`<config-dir>/identity.key`, created with mode `0600`. `skein genkey` writes it and
prints the matching public key and node ID. The key is never written to the config
file and never exposed by the API.

---

## 8. Failure handling

| Failure | Response |
|---|---|
| handshake timeout | backoff, try next candidate |
| peer silent 45 s | mark down, re-probe |
| no route to `to_final` | drop, increment `route_miss` |
| `ttl` exhausted | drop, increment `ttl_exhausted` |
| oversized packet | drop, increment `oversize` |
| TUN read/write error | fatal, exit non-zero with errno |
| duplicate peer ID, different key | reject peer, log at `error` |
| API port in use | fatal at startup, clear message |
| insufficient privilege | fatal at startup, tell the user to use root/Administrator |

---

## 9. Security model

| Property | Mechanism |
|---|---|
| Confidentiality | ChaCha20-Poly1305 via Noise |
| Integrity | AEAD tag; header bound as associated data |
| Authentication | Noise IK static keys |
| Group membership | `network_secret` as PSK modifier |
| Forward secrecy | ephemeral X25519 per handshake |
| Replay | 64-bit counter + sliding window |
| Forwarding privacy | relay never holds the session key |

**Threats not addressed:** a peer holding the secret and a stolen private key can
impersonate that peer. Traffic analysis by a forwarding node reveals who talks to
whom and how much. Denial of service is not mitigated.

---

## 10. Testing strategy

### Unit (no privileges)

| Package | Cases |
|---|---|
| `wire` | encode/decode roundtrip; truncated and malformed frames; fuzz target |
| `session` | handshake succeeds with matching PSK; fails with wrong PSK; replay rejected |
| `path` | candidate selection over a synthetic family matrix |
| `route` | convergence, split horizon, loop rejection, max hops |
| `conf` | validation errors; invite string roundtrip; TOML defaults |

### Integration (Linux netns, root)

```bash
ip netns add n4      # IPv4 only
ip netns add n6      # IPv6 only
ip netns add ndual   # both
```

| # | Assertion |
|---|---|
| 1 | `n4 ↔ ndual` connects over v4, path reports `v4` |
| 2 | `n6 ↔ ndual` connects over v6, path reports `v6` |
| 3 | `n4 ↔ n6` has no direct path — this is the thesis |
| 4 | `n4 → ndual → n6` works with forwarding enabled, `to_next != to_final` |
| 5 | Kill `ndual` → routes recompute, `n4 ↔ n6` becomes unreachable |
| 6 | Kill one path of a dual-stack pair → session survives, path switches |

### Cross-platform smoke

- Linux: two `skeind` processes on loopback, `ping 10.144.144.2`.
- Windows: same, via a VM, with `wintun.dll` present; verify `netsh` address and
  route installation and that `ping` traverses the overlay.

### Metrics for the report

Handshake time (v4 vs v6), throughput vs MTU, 50-byte per-packet overhead
measured against `tcpdump`, RTT inflation vs raw `ping`, failover duration,
route convergence time after a peer disappears, CPU at 100 Mbps.

---

## 11. Build phases

| Phase | Deliverable | Days |
|---|---|---|
| 0 | module skeleton, config, `genkey`, invite string, CI build for both OSes | 2 |
| 1 | TUN + UDP, unencrypted packet A→B on loopback (Linux) | 2 |
| 2 | wire format + Noise session | 3 |
| 3 | peer table, static peers, direct path selection | 2 |
| 4 | STATE gossip + mDNS | 2 |
| 5 | route table, bridge selection, forwarding | 4 |
| 6 | local API + `skein` CLI client | 3 |
| 7 | Windows port: Wintun, `netsh`, service | 3 |
| 8 | keepalive, failover, systemd/service units | 2 |
| 9 | netns integration suite + measurements | 3 |

**≈26 working days.** Each phase ends with something runnable.

---

## 12. Who can use it

| Use case | Works | Why |
|---|---|---|
| 2–5 of your own devices | yes | full mesh, static peers |
| Every pair shares a reachable family | yes | one reachable side suffices |
| v4-only ↔ v6-only, dual-stack member present | yes | forwarding through the bridge |
| Devices on the same LAN | yes | mDNS, zero config |
| **v4-only ↔ v6-only, no dual-stack member** | **no** | no common L3 protocol |
| **CGNAT ↔ CGNAT** | **no** | no inbound on either side; needs an external relay |
| >10 nodes | no | O(n²) tunnels, no routing protocol |
| Untrusted or multi-tenant users | no | one shared secret, no ACLs |
| UDP-blocked networks | no | no TCP/WSS transport in v1 |

---

## 13. Future work

1. **TCP/WSS transport** — implement `Link` over a length-prefixed stream. Unlocks
   port-443 egress and enables an external relay without changing any other layer.
2. **External relay** — `relay = ["udp://…"]`; a forwarder is already the same code
   path as bridging.
3. **Hole punching** — STUN via the relay's observed-address echo, then
   simultaneous probing. Worth it for one-hard-NAT pairs; not for two.
4. **Route protocol** — link-state with cost flooding for >10 nodes.
5. **ACLs and per-peer revocation.**

---

## Appendix — Decision log

| # | Decision | Rationale |
|---|---|---|
| D1 | Go, two binaries | fast enough, static binaries, easy cross-compile |
| D2 | Full mesh, ≤5 nodes | removes the need for a routing protocol |
| D3 | Dual overlay (IPv4 + ULA) | demonstrates overlay/underlay independence |
| D4 | Three layers: session / transport / overlay | path changes never touch crypto |
| D5 | Noise IK + PSK modifier | mutual auth, forward secrecy, simple |
| D6 | Forwarding on in v1 | v4-only ↔ v6-only is the project's core claim |
| D7 | No external relay, no hole punching | out of scope; documented as limits |
| D8 | `Link` is an interface from day one | TCP drops in later without a refactor |
| D9 | Daemon + API; CLI and UI are clients | one source of truth, UI-agnostic |
| D10 | MTU 1280, no fragmentation | kernel never emits larger packets |
| D11 | Plaintext transport header, encrypted payload | forwarding without decrypting |
| D12 | Shell out to `ip`/`netsh` for netcfg | no cgo, debuggable, ~60 lines per OS |
