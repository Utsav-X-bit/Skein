# Skein

Skein is a small userspace overlay VPN for Linux and Windows. It connects
2–5 of your own machines over an encrypted full mesh, and it can bridge an
IPv4-only host to an IPv6-only host when a dual-stack member is present.

- End-to-end `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` sessions between every
  pair of nodes; relays never hold the session keys.
- Dual overlay: IPv4 (`10.144.144.0/24`) and IPv6 ULA
  (`fd00:6d65:7368::/64`), both configurable.
- Underlay is UDP on a single port (default `11010`), dual-stack.
- Static peers, invite strings, mDNS on the local link, and authenticated
  state gossip.
- Bounded distance-vector routing with at most four links and TTL-guarded
  forwarding.
- `skeind` daemon plus `skein` CLI; the loopback REST/SSE API is the only
  contract a UI needs.

## Build

```bash
go build ./cmd/skeind ./cmd/skein
```

Cross-build for Windows (the release also needs `wintun.dll` next to
`skeind.exe`):

```bash
GOOS=windows GOARCH=amd64 go build ./cmd/skeind ./cmd/skein
```

## Quick start

On each machine:

```bash
sudo ./skein init                  # identity, config, PSK, API token
sudo ./skein invite                # share this string with the other nodes
```

Edit the config so every node has a unique `overlay_ipv4`, then either add
peers or rely on mDNS on the same LAN:

```toml
[[peer]]
id     = "b3f64ecc491aba35"
pubkey = "K2Ipr1k0juOQxvsbrOFvFo2vMupEJjP78aIA1VytR2U="
dialer = true
addrs  = ["udp://203.0.113.5:11010"]
```

Exactly one side of each pair sets `dialer = true`: the side that can reach the
other. For mDNS peers the lower peer ID dials.

Start the daemon:

```bash
sudo ./skeind run -c /etc/skein/skein.toml
```

Check it:

```bash
./skein status
./skein peers list
./skein routes
./skein ping 10.144.144.2
```

`sudo ./skeind install-service -c /etc/skein/skein.toml` registers a systemd
unit; on Windows use `skeind install-service` from an elevated prompt and
`sc start skein`.

## Configuration

The full schema is documented in
[`docs/protocol.md`](docs/protocol.md#configuration). Secrets:

- `network_secret`: 32 random bytes, base64url, shared by every node.
- `api.token`: 32 random bytes, base64url; the CLI sends it as
  `Authorization: Bearer <base64url>`.
- `identity.key`: the X25519 private key, owner-only, never sent anywhere.

The daemon refuses a config file that group or others can read.

## API

The daemon binds `127.0.0.1:11011` only:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/status` | node ID, overlay addresses, uptime, version |
| `GET` | `/api/v1/peers` | peer table |
| `POST` | `/api/v1/peers` | add a peer from an invite |
| `DELETE` | `/api/v1/peers/{id}` | remove a peer |
| `POST` | `/api/v1/peers/{id}/probe` | force a candidate probe |
| `GET` | `/api/v1/routes` | reachability table |
| `GET` | `/api/v1/config` | effective config with secrets redacted |
| `GET` | `/api/v1/events` | SSE: peer up/down, discovery, removal |

Every route requires the bearer token. Browser `Origin` requests are rejected
and no CORS headers are sent.

## Limits

- No external relay, STUN, hole punching, or TCP/WSS transport: two hosts
  behind CGNAT, or a network that blocks UDP, cannot connect.
- IPv4-only ↔ IPv6-only requires a dual-stack member; with no common underlay
  family there is no path.
- More than five nodes, or routes longer than four links, are unsupported.
- No PMTUD or fragmentation. A dual-stack underlay path below 1382 bytes of
  PMTU is unsupported; IPv4-only tunnels may lower the MTU.
- One shared network secret, no per-peer ACLs or revocation.
- The loopback API does not isolate processes running as the same user.
- Relays learn peer IDs, packet sizes, timing, and direction.

See [`docs/protocol.md`](docs/protocol.md) for the wire contract and
[`docs/measurements.md`](docs/measurements.md) for the measurement procedure
and recorded results.

## Reproduce the dual-stack demo

```bash
sudo -E env "PATH=$PATH" ./test/netns/run.sh
```

The script creates three namespaces (IPv4-only, IPv6-only, dual-stack), starts
one daemon in each, asserts that the IPv4-only node reaches the IPv6-only node
through the dual-stack member, pings across the overlay, and removes only the
namespaces it created.

## Tests

```bash
go test ./...                 # unit and loopback integration
sudo ./test/netns/run.sh      # namespace bridge proof
```

The Windows runtime smoke test is `test/windows/smoke.ps1`; cross-compilation
alone does not prove Wintun, routes, or the service control manager work.
