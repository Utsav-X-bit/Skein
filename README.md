# Skein

[![ci](https://github.com/Utsav-X-bit/Skein/actions/workflows/ci.yml/badge.svg)](https://github.com/Utsav-X-bit/Skein/actions/workflows/ci.yml)

Skein is a small userspace mesh VPN for 2–5 of your own machines on Linux and
Windows. It builds an encrypted full mesh over plain UDP and can bridge an
IPv4-only host to an IPv6-only host when a dual-stack member is online.

```mermaid
flowchart LR
    A["n4<br/>IPv4 only<br/>10.144.144.1"]
    B["ndual<br/>IPv4 + IPv6<br/>10.144.144.2"]
    C["n6<br/>IPv6 only<br/>10.144.144.3"]
    A -- "UDP over IPv4" --> B
    B -- "UDP over IPv6" --> C
    A -. "end-to-end Noise session, relayed by ndual" .-> C
```

`ndual` forwards opaque ciphertext: it never holds the `n4 ↔ n6` session keys.

## Features

- **End-to-end `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s`** between every pair of
  nodes, with explicit nonces, 64-packet replay windows per key epoch, and a
  fresh rekey every hour or 2³² packets.
- **Dual overlay**: IPv4 `10.144.144.0/24` and IPv6 ULA `fd00:6d65:7368::/64`,
  both configurable per node.
- **One UDP port** (default `11010`), dual-stack socket with a paired-socket
  fallback.
- **Discovery**: static peers, `skein:1:…` invite strings, mDNS on the local
  link, and authenticated state gossip that carries overlay addresses and
  routes (never keys).
- **Bounded routing**: distance vector with full paths, split horizon, and at
  most four links. Relays rewrite only `to_next` and `ttl`.
- **One binary**: `skein` runs the daemon (`skein run`), the system service,
  the dashboard, and every client command; `skeind` remains a thin wrapper.
- **Automatic firewall**: `skein run` opens the underlay port and trusts the
  overlay interface (ufw, firewalld, or Windows Firewall), so any protocol
  works without extra rules.
- **Relay introductions**: a peer reachable only through a relay asks it for an
  introduction, both sides dial each other's observed endpoint at once, and a
  failed punch keeps the relayed path.
- **TCP fallback**: every node listens on TCP on the same port; after two
  silent UDP attempts the dialer switches to TCP.
- **Self-maintaining config**: learned peers are written back to the TOML
  atomically, so a restart keeps the mesh.
- **Named nodes**: each node sets `node.name`; names travel in STATE gossip and
  appear in the dashboard, `skein peers`, and the API.
- **Live dashboard**: `skein dashboard` renders an animated mesh view with
  per-peer throughput, transport (udp/tcp), RTT, and an event feed.

## Status

| Area | Verified |
|---|---|
| Unit tests, `-race`, fuzz target | pass |
| Loopback UDP handshake + overlay packet | pass |
| Three-node Linux netns bridge (v4-only ↔ v6-only) | pass |
| mDNS discovery across a veth | pass (2 s to `up`) |
| systemd install / start / stop / uninstall | pass |
| TUN kernel round-trip | pass |
| Relay introductions (punch) | unit tests (control relay + dataplane) |
| TCP fallback over loopback | pass |
| Firewall automation (ufw) | verified live |
| Windows | cross-compiles; runtime smoke script provided, not yet run in a VM |

Version `0.1.0-dev`.

## Build

```bash
go build ./cmd/skein          # daemon and CLI in one binary
go build ./cmd/skeind         # optional thin daemon wrapper
```

Windows (put `wintun.dll` next to the executable):

```bash
GOOS=windows GOARCH=amd64 go build ./cmd/skeind ./cmd/skein
```

## Quick start

**First node** (no privileges needed):

```bash
./skein init          # identity, config, secret, API token, and a join string
```

It prints a `join:` line like `skein:2:…`. That one string carries the network
secret, the inviter's identity and addresses, the overlay prefixes, and the
overlay addresses already in use. Treat it as a secret.

**Every other node**:

```bash
./skein join "skein:2:…"   # sets the secret, a free overlay address, and the inviter as a peer
```

`join` writes the config and, if needed, a fresh identity. Start both ends:

```bash
sudo ./skein run -c ~/.config/skein/skein.toml
```

The inviter accepts the joiner automatically once the joiner proves the network
secret during the handshake; no peer block is needed on the inviter. Static
peers remain supported when you want fixed addresses:

```toml
[[peer]]
id     = "b3f64ecc491aba35"
pubkey = "K2Ipr1k0juOQxvsbrOFvFo2vMupEJjP78aIA1VytR2U="
dialer = true                      # exactly one side of each pair
addrs  = ["udp://203.0.113.5:11010"]
```

Start the daemon as root/Administrator and check it as your normal user:

```bash
sudo ./skein run -c ~/.config/skein/skein.toml      # the shell expands ~ first
./skein status
./skein peers list
./skein routes
./skein ping 10.144.144.2
```

Service mode:

```bash
sudo ./skein install-service -c ~/.config/skein/skein.toml   # systemd / Windows SCM
```

`skeind` remains available as a thin wrapper with the same `run`,
`install-service`, and `uninstall-service` commands.

## Dashboard

```bash
sudo ./skein dashboard
```

If no daemon is answering, the dashboard starts one from the config file
(`-c <path>`, or the default location; under `sudo` the invoking user's config
is used) and stops it again when you quit. If a daemon is already running it
simply attaches. Daemon output from a dashboard-started daemon goes to
`<config>.log`.

An animated full-screen view: the local node in the centre, peers on a ring,
link pulses that speed up with real traffic, per-peer rates eased toward their
targets, transport and RTT, a throughput sparkline, and the live event feed.
`q` quits. `--no-motion` (or `SKEIN_NO_MOTION=1`) renders a static snapshot;
non-TTY output is always static.

## Naming nodes

```bash
./skein name "utsav-laptop"     # show or set this node's name
```

The name is advertised in STATE gossip, so every peer displays it instead of a
peer ID. A peer can also be given a local hint before first contact:

```toml
[[peer]]
id     = "b3f64ecc491aba35"
name   = "yoga"
pubkey = "…"
```

The name a peer advertises about itself always wins over the local hint.

## Configuration

```toml
[node]
overlay_ipv4_prefix = "10.144.144.0/24"
overlay_ipv6_prefix = "fd00:6d65:7368::/64"
overlay_ipv4        = "10.144.144.2"
overlay_ipv6        = "fd00:6d65:7368::2"   # optional; requires mtu >= 1280
listen_port         = 11010
network_secret      = "<32 random bytes, base64url>"
mtu                 = 1280
interface           = "skein0"              # Windows default: Skein
name                = "utsav-laptop"        # optional, shown to peers

[api]
listen = "127.0.0.1:11011"                  # loopback only
token  = "<32 random bytes, base64url>"

[discovery]
mdns = true
```

Unknown keys are rejected. The daemon refuses a config readable by group or
others, and the X25519 key lives in `<config dir>/identity.key` with owner-only
permissions.

Full key-by-key reference: [`docs/protocol.md`](docs/protocol.md#configuration).

## CLI

| Command | Purpose |
|---|---|
| `skein init` | Create identity, config, network secret, API token |
| `skein run` | Run the daemon (root / Administrator) |
| `skein install-service` | Register the system service |
| `skein uninstall-service` | Remove the service and its firewall rules |
| `skein genkey` | Print the public key and node ID |
| `skein invite` | Print `skein:1:<base64url>` (identity + addresses, no secret) |
| `skein invite --network` | Print the `skein:2:` join string (contains the secret) |
| `skein join <invite>` | Configure this node from a network invite |
| `skein name [value]` | Show or set this node's name |
| `skein dashboard` | Live mesh view; starts the daemon if needed |
| `skein status [--json]` | Overlay addresses, peers, uptime, counters |
| `skein peers [list\|add\|rm]` | Manage peers |
| `skein routes [--json]` | Reachability table |
| `skein probe <peer-id>` | Re-probe candidate endpoints |
| `skein ping <overlay-ip>` | Ping through the overlay |
| `skein version` | Version |

### Global flags

| Flag | Meaning |
|---|---|
| `-c, --config <path>` | Configuration file. Defaults to the user config dir; under `sudo` the invoking user's config is used. |
| `--json` | Machine-readable output for `status`, `peers`, `routes`. |
| `--no-color` | Disable ANSI colour. |
| `--no-motion` | Static dashboard (same as `SKEIN_NO_MOTION=1`). |

### Environment

| Variable | Meaning |
|---|---|
| `SKEIN_NO_MOTION=1` | Render the dashboard as a static snapshot. |
| `SKEIN_DEBUG=1` | Per-frame receive/forward/drop diagnostics on the daemon. |
| `SKEIN_TUN_PROBE=1` | Root-only TUN kernel round-trip test. |
| `SKEIN_NETNS_PREFIX` | Namespace prefix used by `test/netns/run.sh`. |

Exit codes: `0` success, `1` runtime or API error, `2` usage error.

## API

Loopback only, `Authorization: Bearer <token from config>`; browser `Origin`
requests are rejected and no CORS headers are sent.

| Method | Path |
|---|---|
| `GET` | `/api/v1/status` |
| `GET` / `POST` | `/api/v1/peers` |
| `DELETE` | `/api/v1/peers/{id}` |
| `POST` | `/api/v1/peers/{id}/probe` |
| `GET` | `/api/v1/routes` |
| `GET` | `/api/v1/config` (secrets redacted) |
| `GET` | `/api/v1/events` (SSE) |

## Firewall and any protocol

`skein run` opens what it needs at startup, idempotently:

- **Linux** — with `ufw` active it allows `<port>/udp` and all traffic on the
  overlay interface; with `firewalld` it adds the port and puts the interface in
  the trusted zone.
- **Windows** — it creates `Skein underlay` (inbound UDP on the port) and
  `Skein overlay` (all inbound traffic on the interface) firewall rules.

Because the overlay interface is trusted, every protocol works without
per-service rules:

```bash
ssh user@10.144.144.2
python3 -m http.server 8000 --bind 10.144.144.1
```

The only underlay requirement is that **one side of each pair is reachable** on
the UDP port: a public IPv4 with a forward, a public IPv6, or the same LAN.
SSH and every other protocol travel inside the tunnel and never need their own
router forward.

## Reliability

- Keepalive every 15 s keeps NAT mappings open.
- 45 s without authenticated traffic marks the peer down, clears the cached
  endpoint, drops the session, and withdraws its routes; a configured dialer
  re-handshakes immediately instead of encrypting for a dead address.
- Handshake retries back off 1 s → 30 s and reset as soon as a route appears.
- A changed endpoint is only adopted after a challenge/response over the
  existing session.
- When a path dies, the session is dropped and the dialer retries UDP, then TCP
  on the same port, then the relayed route.

## Reaching peers behind NAT

One side of every pair must be reachable, or introduced by a relay:

- **Direct** — a public IPv4 with a UDP forward, a public IPv6, or the same LAN.
- **Relayed** — both sides connect to any reachable peer (a cheap VPS is
  enough); it forwards opaque ciphertext.
- **Introductions** — a node that only reaches a peer through a relay asks the
  relay, which reports both observed endpoints; both sides dial at once. This
  works for endpoint-independent NAT; symmetric NAT keeps the relayed path.
- **UDP blocked** — after two silent UDP attempts the dialer opens TCP to the
  same port. Set `listen_port = 443` to make both transports use 443.

Not implemented: TLS camouflage, STUN against an external service, and a
global DERP-style relay network.

## How it works

```
[type:1][to_next:8][to_final:8][from:8][ttl:1] payload
```

The 26-byte header is authenticated as AEAD associated data except the
relay-mutable `to_next` and `ttl`. Encrypted frames add `[epoch:4][counter:8]`
and a 16-byte tag, so a 1280-byte inner packet becomes **1382 bytes over IPv6**
or **1362 bytes over IPv4** — 54 bytes of protocol overhead.

Handshakes are Noise IKpsk2 with a 128-bit attempt ID and the epoch bound into
the prologue. A session is installed only after an encrypted confirmation; at
one epoch the smaller attempt ID wins, and higher epochs always supersede.
Relays forward both handshake messages and opaque data.

Peer down after 45 s of silence, keepalive every 15 s, state sync every 5 s,
handshake backoff 1→30 s.

## Limits

- Two symmetric-NAT peers still need a reachable relay peer; there is no
  global relay network.
- The TCP fallback is plain TCP, not TLS; it defeats UDP blocking but not
  deep-packet inspection.
- IPv4-only ↔ IPv6-only needs a dual-stack member; no common family, no path.
- More than five nodes or routes longer than four links are unsupported.
- No PMTUD or fragmentation; a dual-stack underlay path below 1382 bytes of
  PMTU is unsupported.
- One shared secret, no per-peer ACLs or revocation. The join string contains
  that secret, so anyone holding it can join the network.
- The loopback API is not an isolation boundary between processes of the same
  user, and relays can observe peer IDs, sizes, and timing.

## Testing

```bash
go test -race ./...
sudo -E env "PATH=$PATH" ./test/netns/run.sh        # v4-only ↔ v6-only bridge
SKEIN_TUN_PROBE=1 sudo -E env "PATH=$PATH" \
  go test ./internal/tun -run TestTUNWriteRead -v    # TUN kernel round-trip
```

Windows runtime check: `test/windows/smoke.ps1` (elevated, with `wintun.dll`).

## Docs

- [`docs/protocol.md`](docs/protocol.md) — wire format, handshake, routing,
  discovery, configuration.
- [`docs/measurements.md`](docs/measurements.md) — what was measured and how to
  reproduce it.
- [`docs/superpowers/specs/`](docs/superpowers/specs) — design spec.
- [`docs/superpowers/plans/`](docs/superpowers/plans) — implementation plan.

## Layout

```
cmd/skein                 daemon, dashboard, and CLI in one binary
cmd/skeind                thin daemon wrapper
internal/identity         X25519 keys, peer IDs, invites
internal/conf             TOML schema, defaults, validation
internal/wire             bounded frame codec
internal/session          Noise handshakes, epochs, replay windows
internal/link             UDP transport
internal/tun, internal/netcfg   TUN creation, addresses, routes, MTU
internal/peer, internal/path    peer table, candidate probing
internal/route            bounded distance-vector routing
internal/discovery        mDNS and authenticated state gossip
internal/dataplane        packet plane, forwarding, counters
internal/control          keepalive, state sync, rekey, probes
internal/api, internal/daemon   loopback API and composition
internal/app              daemon entry points shared by both binaries
internal/cli              CLI, API client, and dashboard
internal/firewall         automatic ufw/firewalld/Windows firewall rules
internal/service          systemd and Windows service installation
test/netns, test/windows  runtime harnesses
```
