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

- 2–5 devices form a routed overlay mesh; the maximum supported shortest route is four links.
- Overlay is dual-stack by default: IPv4 and IPv6 ULA, independent of the underlay; overlay prefixes are configurable.
- End-to-end encryption; a forwarding node sees no plaintext.
- Zero privileged nodes; any reachable member can bridge.
- Cross-platform: Linux and Windows; Windows release includes the required Wintun DLL.
- Full CLI plus a stable local API for an external UI.
- The v4-only ↔ v6-only failure is **demonstrable** (netns test) and the fix (bridge) is **demonstrable** too.

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

The data/control logic is pure Go and unit-testable without privileges. `tun`,
`netcfg`, `link`, identity-file permissions, and service lifecycle have OS-specific
adapters.

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
  1 HANDSHAKE   payload = bounded Noise IKpsk2 message
  2 DATA        payload = [key_epoch:4][counter:8][AEAD(...)]
  3 PING        payload = [key_epoch:4][counter:8][AEAD(seq:4)]
  4 PONG        payload = [key_epoch:4][counter:8][AEAD(seq:4, echo_us:8)]
  5 STATE       payload = [key_epoch:4][counter:8][AEAD(bounded state records)]

DATA plaintext
  [IP packet]                       version nibble decides v4 vs v6

AEAD associated data
  type || to_final || from || key_epoch

STATE record
  [origin_id:8][origin_seq:8][expires_at:8][id:8][pubkey:32]
  [flags:1][addr_count:1][addr...][hops:1][path...]

  flags: bit0 = has_v4, bit1 = has_v6, bit2 = has_direct_path
  addr:  [fam:1][port:2][ip:4 or 16]
  path: up to four peer IDs, including the advertising origin
```

State is bounded to one 1400-byte UDP frame; oversized records are rejected, not
fragmented. Records expire and require monotonically increasing origin sequence.
The relay-mutable `to_next` and `ttl` are excluded from associated data; receivers
require `to_next == self` on ingress. All post-handshake control/data messages are
encrypted.

`to_next` names the receiver for this hop; `to_final` names the session endpoint.
For direct paths they are equal; relays rewrite only `to_next` and decrement TTL.
Peer IDs are the first 8 bytes of SHA-256(static public key), rendered as hex.

**Overhead:** 26-byte header + 4-byte key epoch + 8-byte counter + 16-byte AEAD
tag = **54 bytes** per packet.
### 4.7 Session

- Noise `IKpsk2` with X25519, ChaChaPoly, and BLAKE2s. The initiator knows the
  responder's static public key from configuration or discovery.
- `network_secret` is a random 32-byte PSK. Wrong keys fail the handshake.
- Replay protection is a 64-bit counter plus a 64-packet sliding window per epoch.
- Rekey after 2^32 packets or 1 hour, whichever comes first. DATA carries a
  32-bit key epoch; receivers accept current and previous epochs during a bounded
  reordering grace, then reject the old epoch.
- One end-to-end session per peer identity; transport changes do not change keys.
  The dialer starts a bounded handshake attempt and both sides confirm its attempt
  ID before installing the session.

### 4.8 Path selection

Enumerate configured and eligible interface candidates filtered by address families
the node actually holds, then probe with bounded concurrency. Exactly one dialer is
configured for static/WAN peers; mDNS peers elect the lower peer ID. A peer behind
NAT must be configured as the dialer toward a reachable endpoint; NAT role is not
inferred automatically.

| Candidate | Source |
|---|---|
| global IPv6 | config, authenticated peer state |
| public IPv4 | config, authenticated peer state |
| LAN address | mDNS and active non-loopback interfaces |

The dialer selects the lowest-RTT endpoint that completes the authenticated
handshake. Candidate endpoint updates require an authenticated path challenge
before use. Path changes retain the existing end-to-end session.

Per-peer state machine: `down → probing → up → degraded → probing`.
### 4.9 Routing and forwarding

Each node maintains `route[dest] = { next_hop, cost_ms, links, path }`.

- Direct neighbours have one link and measured RTT.
- Learned routes add local RTT to advertised cost; the lowest cost wins.
- Split horizon and full path-vector loop rejection are required.
- Maximum four links; longer paths are dropped. This covers a five-node line.
- Routes are recomputed on state updates, RTT changes, expiry, or link loss.

**Forwarding a packet:**

1. Require incoming `to_next == self`; otherwise drop.
2. If `to_final == self`, authenticate/decrypt with the end-to-end session.
3. Otherwise look up `route[to_final]`; if absent, drop and count.
4. If `ttl <= 1`, drop and count; otherwise decrement TTL.
5. Resolve `next_hop` to its current best transport address.
6. Rewrite only `to_next` and send. The payload is never decrypted.

A forwarding node sees peer IDs, packet sizes, timing, and direction, but no
plaintext. `from`, `to_final`, type, and key epoch are authenticated at the final
peer; changing them causes authentication failure.

With at most five nodes, bounded Bellman-Ford relaxation converges after route
updates. The four-link cap covers the worst-case connected topology.
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

Inner MTU defaults to **1280**. With the 54-byte protocol overhead, the outer packet
is:

```
1280 + 54 (header+epoch+counter+tag) + 8 (UDP) + 40 (IPv6) = 1382
1280 + 54                         + 8        + 20 (IPv4) = 1362
```

No fragmentation or PMTUD is implemented. IPv6-enabled interfaces keep MTU ≥1280;
an operator may lower MTU only when IPv6 overlay is disabled. A dual-stack path
whose PMTU is below 1382 bytes is unsupported in v1 and documented as such.

### 4.13 Keepalive and failover

| Event | Action |
|---|---|
| every 15 s | authenticated PING on the active path (holds NAT mappings) |
| 45 s silent | mark peer down, re-probe all candidates |
| path dies, peer alive | re-probe, keep the session, resume on any answering candidate |
| handshake failure | backoff 1 s → 2 s → 4 s → cap 30 s |
| route lost | recompute; existing sessions survive a route change |

---

## 5. Local API

Bound to `127.0.0.1` only. `Authorization: Bearer <token>` from config, with the
raw 32-byte token base64url-encoded so the header stays printable. This is the
contract the UI is built against; the UI is not designed here.

Every route requires bearer authentication. Reject browser `Origin` requests, send no
CORS headers, cap request bodies and concurrent requests, and rate-limit state-changing
operations and probes. This loopback API is not an isolation boundary from same-user
processes.

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

skein init                    create identity, config, PSK, and API token
skein genkey                  print the public key and node ID
skein invite                  print skein:1:<base64> for this node
skein status                  overlay addresses, peer count, uptime
skein peers [list|add|rm]     manage peers
skein routes                  print the reachability table
skein probe <peer-id>         force a re-probe
skein ping <overlay-ip>       ICMP through the overlay (wraps the system ping)
skein version
```

Only `genkey`, `invite`, and `init` work offline; all other `skein` commands use the
authenticated local API. `init` generates cryptographically random 32-byte PSK and
API tokens, creates files with owner-only permissions, and never overwrites an
existing identity/config.

## 7. Configuration

TOML, loaded by `skeind`.

```toml
[node]
overlay_ipv4_prefix = "10.144.144.0/24"
overlay_ipv6_prefix = "fd00:6d65:7368::/64"
overlay_ipv4        = "10.144.144.2"
overlay_ipv6        = "fd00:6d65:7368::2"   # optional
listen_port         = 11010
network_secret      = "<base64url-encoded random 32-byte PSK>"
mtu                 = 1280
interface           = "skein0"              # Windows default: Skein

[api]
listen = "127.0.0.1:11011"
token  = "<base64url-encoded random 32-byte token>"

[discovery]
mdns = true

[[peer]]
id       = "a3f9c2e1b7d0456a"
pubkey   = "base64..."
dialer   = true
addrs    = ["udp://[2406:b400:72:4f1e::1]:11010", "udp://203.0.113.5:11010"]
```

Validation: overlay host addresses must be unicast, inside configured prefixes, and
unique; prefixes must not overlap default routes or conflicting local routes;
`listen_port` in 1–65535; at least one peer or `mdns = true`; network PSK and API
token are each exactly 32 cryptographically random bytes. IPv6 overlay requires
MTU ≥1280. `skein init` creates secrets/config with owner-only permissions.

The static private key is stored separately from config in
`<config-dir>/identity.key`, created with mode `0600` on Unix and an owner-only
Windows ACL. `skein genkey` writes it and prints the matching public key and node
ID. The private key is never written to config or exposed by the API.
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
| Integrity | AEAD; type, final peer, origin peer, and key epoch bound as associated data |
| Authentication | Noise IK static keys |
| Group membership | random 32-byte `network_secret` as PSK |
| Forward secrecy | ephemeral X25519 per handshake and rekey |
| Replay | 64-bit counter + 64-packet window per key epoch |
| Forwarding privacy | relay never holds the end-to-end session key |

**Threats not addressed:** a trusted peer holding the group PSK can lie about routes,
drop traffic, or impersonate members; a stolen private key permits impersonation.
Forwarders see peer IDs, sizes, timing, and direction. Rate limits reduce but do not
eliminate denial-of-service. The loopback API does not isolate processes running as
the same user.

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

Handshake time (v4 vs v6), throughput vs MTU, 54-byte per-packet protocol overhead
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
| 2–5 of your own devices | yes | static peers or mDNS; configured dialer roles |
| Every pair shares a reachable family | yes | one reachable side suffices, with the reachable side configured to dial |
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
| D5 | Noise IKpsk2 + random PSK | mutual auth, forward secrecy, simple |
| D6 | Forwarding on in v1 | v4-only ↔ v6-only is the project's core claim |
| D7 | No external relay, no hole punching | out of scope; documented as limits |
| D8 | `Link` is an interface from day one | TCP drops in later without a refactor |
| D9 | Daemon + API; CLI and UI are clients | one source of truth, UI-agnostic |
| D10 | MTU 1280, no fragmentation | kernel never emits larger packets |
| D11 | Authenticated header, end-to-end encrypted payload | relays route without decrypting DATA |
| D12 | Shell out to `ip`/`netsh` for netcfg | no cgo, debuggable, ~60 lines per OS |
