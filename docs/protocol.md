# Skein protocol

This document is the byte-level contract implemented by `internal/wire`,
`internal/session`, `internal/discovery`, `internal/route`, and
`internal/dataplane`.

## Layers

| Layer | Responsibility |
|---|---|
| Overlay | IPv4/IPv6 packets read from and written to the TUN device |
| Session | Noise IKpsk2 between the final peers; independent of the underlay |
| Transport | UDP datagrams, one socket per node (dual-stack or paired) |

Path changes never touch the session. Relays only inspect and rewrite the
outer header.

## Frame format

Every datagram is one frame:

```
[type:1][to_next:8][to_final:8][from:8][ttl:1] payload
```

All integers are big-endian. The header is 26 bytes; a frame is at most 1400
bytes.

| Field | Meaning |
|---|---|
| `type` | message type (below) |
| `to_next` | peer ID of the next hop; must equal the receiver's ID |
| `to_final` | peer ID of the end-to-end destination |
| `from` | peer ID of the origin |
| `ttl` | 1–4; each relay decrements it |

### Message types

| Value | Name | Body |
|---|---|---|
| 1 | HANDSHAKE | plaintext Noise message |
| 2 | DATA | encrypted overlay IP packet |
| 3 | PING | encrypted keepalive |
| 4 | PONG | encrypted keepalive reply |
| 5 | STATE | encrypted peer/route advertisement |
| 6 | CHALLENGE | encrypted endpoint probe |
| 7 | CHALLENGE_REPLY | encrypted probe answer |
| 8 | CONFIRM | encrypted session confirmation |

Every type except HANDSHAKE uses the same encrypted envelope:

```
[key_epoch:4][counter:8][ciphertext][tag:16]
```

So an encrypted frame costs 26 + 12 + 16 = **54 bytes** before UDP and IP. A
1280-byte inner packet becomes 1382 bytes over IPv6 or 1362 bytes over IPv4.

## Peer IDs

`peer_id = big-endian uint64 of the first 8 bytes of SHA-256(X25519 public key)`.
Zero is never valid. A node ID that resolves to a different key is rejected.

## Handshake

Pattern: `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` (`flynn/noise`, IK pattern
with `PresharedKeyPlacement: 2`).

Handshake payloads are:

```
[attempt:16][epoch:4][noise message]
```

The Noise prologue binds the attempt, the epoch, and both peer IDs:

```
"skein/1" || attempt(16) || epoch(4) || initiator_id(8) || responder_id(8)
```

Rules:

- Exactly one configured dialer per WAN pair. For mDNS peers the lower peer ID
  dials.
- A responder must present the static key the peer table holds; a mismatch
  fails the handshake.
- A session is installed only after the peer sends a CONFIRM encrypted under
  the new keys. CONFIRM is itself sent with the not-yet-installed session, so
  the receiver tries every completed handshake for that epoch and matches the
  attempt ID inside the ciphertext.
- Competing attempts: a higher epoch always wins; at the same epoch the
  lexicographically smaller 128-bit attempt ID wins.

### Epochs and rekey

- The first session uses epoch 1.
- A fresh IKpsk2 handshake runs after 2^32 DATA packets or one hour,
  whichever comes first; the lower peer ID initiates.
- The receiver keeps the current and previous epoch during a bounded overlap
  and rejects older epochs.
- If rekey cannot complete, DATA pauses rather than reusing a key/nonce.

### Nonces and replay

- `counter` is an explicit 64-bit nonce per direction, set with
  `CipherState.SetNonce` before every `Encrypt`/`Decrypt`.
- Each epoch has a 64-packet replay bitmap. A counter is accepted if it is
  new and within 64 of the highest seen; the bitmap is updated only after
  successful AEAD authentication.
- The counter is never reused. Exhaustion stops the session.

### Associated data

```
[type:1][to_final:8][from:8][epoch:4]
```

`to_next` and `ttl` are excluded because relays rewrite them. Tampering with
any authenticated field fails decryption at the final peer.

## Forwarding

On ingress the receiver requires `to_next == local_id`. If `to_final` is
someone else:

1. drop if `ttl <= 1`;
2. look up a route to `to_final`;
3. set `to_next` to the route's next hop and decrement `ttl`;
4. send the opaque frame on.

The relay never decrypts DATA. A relayed handshake is forwarded the same way;
the reply travels back along the path it arrived on.

## Routing

- Direct neighbours are one-hop routes with a measured RTT.
- Each node re-originates its routes as STATE advertisements:
  `Path` starts at the advertising node and ends at the destination.
- `cost` is the sum of measured RTTs; ties resolve on the smaller next-hop ID.
- Paths containing the local node, repeated IDs, or more than four links are
  rejected.
- Split horizon: a node never advertises a route back to its next hop.
- When a direct link is lost, its routes are withdrawn and recomputed.

## STATE payload

```
[version:1][origin:8][seq:8][ttl_seconds:4]
[peer_count:1]
  per peer: [id:8][flags:1][v4:4?][v6:16?][candidate_count:1]
            per candidate: [family:1][address:4|16][port:2]
            [name_len:1][name:name_len]        (version 2)
            [pubkey:32]                        (version 3)
[route_count:1]
  per route: [destination:8][cost_ms:4][hops:1][path_len:1][path ids...]
```

Version 2 adds the optional node name; version 3 adds the peer's public key.
Version 1 and 2 payloads still decode (name or key empty).

Limits: 5 peers, 8 candidates per peer, 8 routes, 32-byte names, path length 4,
and the whole payload must fit one frame. Local and configured records are preserved; excess
transitive hints are dropped deterministically (by hop count, then peer ID).

STATE is authenticated by the session that carries it. The `origin` must equal
the sending peer, the sequence must increase per origin, and expired payloads
are rejected.

Version 3 carries each peer's public key. The key is self-certifying — the peer
ID is the first 8 bytes of `SHA-256(key)` — so a member cannot impersonate
another member's ID. An unknown peer advertised this way is registered and can
be dialed, which is what lets a three-node mesh form without every pair
exchanging invites. A member can still advertise sock-puppet peers it controls;
the peer table is capped, and a configured key is never overwritten. Overlay
addresses and names are learned the same way (first authenticated claim wins).

## Joining a network

`skein init` prints a bootstrap string:

```
skein:2:<base64url payload>
```

The payload carries the 32-byte network secret, the inviter's peer ID and
public key, both overlay prefixes, the overlay IPv4 addresses already in use,
and the inviter's underlay endpoints. It is a secret: holding it is equivalent
to holding the network PSK.

`skein join` on another node:

1. imports the secret and prefixes;
2. picks the lowest overlay IPv4 host address not listed as used (a re-join
   keeps the address already assigned);
3. writes the inviter as a peer with `dialer = true`;
4. creates an identity if none exists.

The inviter needs no configuration entry for the joiner. Its session manager
accepts an unknown peer only after that peer proves the network secret: the
responder learns the peer's static key from message one, but the peer is
registered and trusted only when it decrypts message two and sends the
encrypted confirmation. A peer that presents a key whose hash does not match
its claimed ID is rejected immediately.

## Punch (relayed introductions)

A node that can only reach a peer through a relay asks the relay to introduce
them:

```
[target:8][nonce:16][0]                                  request
[target:8][nonce:16][1][family:1][address][port:2]       delivery
```

The relay answers the requester with the target's observed endpoint and sends
the target the requester's observed endpoint, so both dial at once. A delivery
is honoured at most once per peer per 30 s. If the punch fails, the relayed
route stays in place.

## TCP fallback

Every node listens on TCP on the same port as UDP. After two silent UDP
handshake attempts the dialer opens a TCP connection to the candidate address;
frames are length-prefixed (`[len:4]`, big-endian) and carry the same 26-byte
header and encryption. Replies reuse the accepted connection, keyed by its
source address, so the existing datagram-oriented `Link` contract still holds.
Set `listen_port = 443` to make both transports bind 443.

## Discovery

mDNS service `_skein._udp.local` with TXT records:

```
id=<16 hex>       peer ID
pk=<base64>       X25519 public key
net=<base64url>   first 8 bytes of SHA-256(network_secret)
a=<ip:port>       candidate endpoint (repeated)
```

Entries with a different `net` tag, a bad key, or an ID that does not match the
key are ignored. The PSK itself is never published.

## Configuration

TOML loaded by `skeind`:

```toml
[node]
overlay_ipv4_prefix = "10.144.144.0/24"
overlay_ipv6_prefix = "fd00:6d65:7368::/64"
overlay_ipv4        = "10.144.144.2"
overlay_ipv6        = "fd00:6d65:7368::2"   # optional
listen_port         = 11010
network_secret      = "<base64url, exactly 32 bytes>"
mtu                 = 1280
interface           = "skein0"

[api]
listen = "127.0.0.1:11011"
token  = "<base64url, exactly 32 bytes>"

[discovery]
mdns        = true
stun        = true
stun_server = "stun.l.google.com:19302"

[[peer]]
id     = "a3f9c2e1b7d0456a"
pubkey = "base64..."
dialer = true
addrs  = ["udp://[2406:b400:72:4f1e::1]:11010"]
```

Validation: canonical non-default prefixes, unicast host addresses inside their
prefix, unique node addresses, 1–65535 listen port, MTU 576–1346 (≥1280 when
the IPv6 overlay is enabled), 32-byte secrets, a loopback API bind, peer keys
matching their IDs, numeric `udp://` endpoints, and at least one peer when
mDNS is disabled.

## Failure handling

| Failure | Response |
|---|---|
| handshake timeout | backoff 1 s → 2 s → 4 s → 30 s cap |
| peer silent 45 s | mark down, withdraw routes, re-probe |
| no route | drop, increment `route_miss` |
| TTL exhausted | drop, increment `ttl_exhausted` |
| oversized frame or packet | drop, increment `oversize` |
| authentication failure | drop, increment `auth_failure` |
| TUN error | fatal, exit non-zero |
| duplicate ID, different key | reject and log |
| insufficient privilege | fatal at startup with a clear message |
