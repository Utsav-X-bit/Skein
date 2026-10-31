# Skein Mesh VPN Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build Skein, a Linux/Windows userspace dual-stack overlay VPN for 2–5 nodes, with end-to-end Noise sessions, forwarding through reachable mesh members, a complete CLI, and a UI-facing local API.

**Architecture:** `skeind` owns the TUN, UDP links, encrypted packet plane, peer/route state, and localhost API. `skein` and any separately built UI are API clients. Direct and forwarded paths carry the same end-to-end session ciphertext; forwarding nodes only inspect and rewrite the outer `to_next` field.

**Tech Stack:** Go; standard library for networking, HTTP/JSON, CLI parsing, tests, and subprocess execution; `golang.zx2c4.com/wireguard/tun` for OS TUN creation only; `github.com/flynn/noise` for Noise handshakes; `github.com/BurntSushi/toml` for TOML; `github.com/grandcat/zeroconf` for mDNS.

## Global Constraints

- Module path: `github.com/utsavgupta5467/skein`.
- Platforms: Linux and Windows; all non-OS packages MUST compile and test on both.
- Overlay prefixes default to IPv4 `10.144.144.0/24` and IPv6 ULA `fd00:6d65:7368::/64`; allow operator-configured prefixes and optional IPv6 per node.
- Inner TUN MTU defaults to 1280 bytes. Permit MTU below 1280 only when IPv6 overlay is disabled; no outer fragmentation in v1.
- UDP listen port defaults to 11010; authenticated endpoint changes require a path challenge before becoming active.
- Forwarding is on in v1; maximum route length is 4 links for five-node line topologies; initialize TTL to 4 and decrement at each relay.
- No external relay, STUN service, UDP hole punching, TCP/WSS, NAT64, or multi-tenant ACLs in v1.
- End-to-end protocol: `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s`; explicit packet nonces, 64-packet replay windows per key epoch, and a fresh IKpsk2 rekey handshake every 2^32 packets or one hour. The lower peer ID initiates rekey; epoch is bound into the handshake prologue. Higher epochs supersede lower epochs; at the same epoch, the lexicographically smaller 128-bit attempt ID wins. Install only after encrypted confirmation.
- Header is 26 bytes: `[type:1][to_next:8][to_final:8][from:8][ttl:1]`; DATA adds `[key_epoch:4][counter:8]` and a 16-byte AEAD tag, for 54 bytes total overhead before UDP/IP.
- Daemon API binds only to `127.0.0.1:11011`; bearer token is 32 random bytes; never return the private key, network PSK, or API token.
- Use focused packages under `internal/`; do not add a UI implementation.
- The Go toolchain is already installed. Install no system packages as part of this plan.

## Detailed limitations and mitigations

| Limitation or risk | Mitigation in this plan | Residual boundary |
|---|---|---|
| CGNAT-to-CGNAT and UDP-blocked paths cannot form a direct link without a rendezvous/relay. | Document the required public inbound endpoint or reachable bridge; provide static peer invites and actionable startup/probe diagnostics. | External relay, STUN, hole punching, and TCP/WSS remain out of scope; affected peers cannot connect in v1. |
| Five nodes can have a four-hop shortest path. A three-hop cap would contradict the 2–5-node mesh goal. | Raise the cap to four links, use a four-hop TTL, and test a five-node line topology. | More than five nodes and routes longer than four hops remain unsupported. |
| UDP reorders, duplicates, and loses packets; implicit Noise counters would fail. Rekeying without epochs could accept stale ciphertext or desynchronize peers. | Put a 32-bit key epoch and 64-bit nonce counter in DATA; use per-epoch replay windows, perform a fresh IKpsk2 handshake at 2^32 packets or one hour, and accept only current/previous epoch during a bounded overlap. | If rekey cannot complete, stop sending under the expired epoch and report the peer degraded; never reuse a key/nonce. |
| Simultaneous or delayed handshakes can install different sessions at the endpoints. | Use exactly one configured WAN dialer per peer pair; elect the lower peer ID as dialer for mDNS peers. Bind every handshake to a 128-bit attempt ID; at one epoch choose the lexicographically smaller attempt ID and install only after encrypted confirmation. | Automatic NAT-role detection is not promised without STUN/rendezvous; operators must configure the reachable/NAT-side dialer. |
| Plain PING/PONG/STATE and unauthenticated routing headers allow spoofing, state poisoning, and reflection. | Encrypt/authenticate every post-handshake control frame; bind message type, `to_final`, `from`, and key epoch as AEAD associated data. Validate `to_next` on receipt and rate-limit handshakes/probes. | A legitimate shared-PSK member can still lie, drop, or flood traffic; no per-peer ACL exists. |
| Transitive gossip can poison endpoints/routes, create stale routes, or direct probes at arbitrary hosts. | Treat third-party records as hints, require each destination to prove its advertised key through Noise before direct use, keep configured keys authoritative, attach origin sequence/expiry, cap endpoints/records, reject loops, and rate-limit probes. | A trusted shared-PSK member can lie about its own reachability or route traffic through itself. |
| mDNS is link-local and can be blocked by host firewalls or VLANs; wildcard UDP bind does not reveal usable local endpoints. | Enumerate interface addresses, publish reachable LAN candidates in mDNS/invites, and retain static invites as fallback. Exclude loopback, unspecified, multicast, and unscoped link-local IPv6 candidates. | Discovery does not cross routed networks; share invites or configure endpoints manually. |
| Requiring one dual-stack UDP socket is not portable across OS/network configurations. | Prefer one dual-stack socket; if unsupported, bind IPv4 and IPv6 sockets on the same port behind one `Link`, with startup rollback if either bind fails. | A host with only one enabled family runs with that family; dual-family tests require both interfaces. |
| A fixed MTU can blackhole traffic on underlays with path MTU below 1382 bytes. | Keep IPv6-enabled TUN MTU at least 1280; allow lower MTU only for IPv4-only nodes, report the exact outer size, and document `1200` only for IPv4-only tunnels. | No PMTUD, ICMP handling, or fragmentation in v1; dual-stack over a path below 1382 bytes remains unsupported. |
| Windows cross-builds do not prove Wintun, routes, privileges, or Service Control Manager behavior. | Use a Windows VM smoke test with two distinct interface names, Wintun DLL, route cleanup, service start/stop, and config paths containing spaces. | Windows runtime proof requires a VM/runner; Linux CI only cross-compiles. |
| Startup failures or abrupt termination can leave OS routes/configuration behind. | Configure transactionally, roll back completed steps on startup failure, remove routes before closing TUN, and test repeated start/stop and partial failure. | Power loss can interrupt cleanup; startup reconciles only Skein-owned interface/routes and never deletes unrelated routes. |
| API bearer tokens or network PSKs stored in ordinary config can be weak or exposed. | Add `skein init` to create a random 32-byte PSK/token, owner-only config/key permissions on both OSes, constant-time bearer comparison, and redaction in every output. | A local administrator/root account can read process memory and secrets. |
| A loopback API is reachable by other local processes and can be targeted by browser-origin requests. | Require bearer auth on every route, reject browser `Origin` values, send no CORS headers, cap bodies/concurrency, and rate-limit state-changing/probe operations. | Local malware running as the same user can read the token/config; this is not a same-user isolation boundary. |
| The relay sees peer IDs, packet sizes, timing, and traffic direction. | State this plainly in the threat model and avoid logging per-packet metadata by default. | Traffic-analysis resistance is not provided. |
| Configurable overlay prefixes and addresses can collide with LAN/VPN routes or special addresses. | Validate unicast host addresses, uniqueness, route overlap, and local/peer ownership; warn on host-route conflicts and fail on default-route overlap. Test configured-prefix conflicts and inspect existing OS routes before mutation. | The operator must choose prefixes compatible with their local networks. |

The user-visible workarounds for intentionally excluded connectivity are: connect
through a publicly reachable peer, use a dual-stack bridge, or wait for a separately
scoped relay/TCP transport feature. Do not imply that changing retries or keepalive
can solve a missing network path.

---

## File map

| Path | Responsibility |
|---|---|
| `.gitignore` | Go build/test artifacts and local configs |
| `.github/workflows/ci.yml` | Linux tests and Linux/Windows cross-builds |
| `cmd/skeind/main.go` | daemon command entry and exit codes |
| `cmd/skein/main.go` | CLI command dispatch |
| `internal/cli/cli.go` | CLI commands and authenticated local API client |
| `internal/identity/identity.go` | X25519 identity key, peer ID, invite encoding |
| `internal/identity/identity_windows.go` | Windows owner-only identity file ACL |
| `internal/conf/config.go` | TOML schema, defaults, validation, config path |
| `internal/wire/frame.go` | fixed header, message types, bounded frame codec |
| `internal/session/session.go` | Noise IKpsk2 handshake, key epochs, packet AEAD/replay window |
| `internal/link/link.go` | cross-transport `Link` interface |
| `internal/link/udp.go` | dual-stack or paired-socket UDP implementation |
| `internal/tun/device.go` | TUN creation wrapper around WireGuard TUN package |
| `internal/netcfg/netcfg.go` | platform-neutral address/route/MTU API |
| `internal/netcfg/netcfg_linux.go` | Linux `ip` command configuration |
| `internal/netcfg/netcfg_windows.go` | Windows `netsh` configuration |
| `internal/peer/table.go` | peer identity, endpoints, direct-link health, counters |
| `internal/path/candidates.go` | usable interface endpoint enumeration and candidate validation |
| `internal/path/probe.go` | candidate probing, RTT, active-path choice |
| `internal/route/table.go` | bounded distance-vector route computation and split horizon |
| `internal/discovery/mdns.go` | LAN advertisements and service discovery |
| `internal/discovery/state.go` | authenticated peer/route state encoding and merge |
| `internal/dataplane/engine.go` | TUN ingress/egress, sessions, dispatch, forwarding |
| `internal/control/control.go` | authenticated keepalive, state sync, timers |
| `internal/api/server.go` | authenticated localhost REST + SSE API |
| `internal/api/types.go` | stable API request/response DTOs |
| `internal/daemon/daemon.go` | lifecycle and component composition |
| `internal/service/service_linux.go` | systemd install/uninstall operations |
| `internal/service/service_windows.go` | Windows Service install/uninstall |
| `testdata/config/*.toml` | deterministic valid/invalid config fixtures |
| `test/netns/` | Linux network namespace test harness |
| `test/windows/` | Windows VM smoke and service lifecycle |
| `docs/superpowers/specs/2026-11-01-skein-mesh-vpn-design.md` | protocol/config contract updated during implementation |

---

### Task 1: Initialize the Go module and cross-platform CI

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `.github/workflows/ci.yml`
- Create: `cmd/skeind/main.go`
- Create: `cmd/skein/main.go`

**Interfaces:**
- Produces module `github.com/utsavgupta5467/skein` and two independently buildable commands.

- [ ] Create `go.mod` with module path `github.com/utsavgupta5467/skein` and the Go version reported by the installed toolchain.
- [ ] Create both command entry points with clear usage errors; replace initial behavior in Tasks 13–14.
- [ ] Add `.gitignore` entries for `/bin/`, `*.exe`, `*.test`, `/coverage.out`, and local `skein.toml` / `identity.key` files.
- [ ] Add CI matrix: `go test ./...` on `ubuntu-latest`, and `GOOS=windows GOARCH=amd64 go build ./cmd/skeind ./cmd/skein` on Ubuntu.
- [ ] Run `go test ./...`; expected: PASS with no tests and both command packages compiling.
- [ ] Run `GOOS=windows GOARCH=amd64 go build ./cmd/skeind ./cmd/skein`; expected: both Windows executables build.
- [ ] Commit `build: initialize cross-platform Skein module`.

### Task 2: Implement identity, peer IDs, and invite strings

**Files:**
- Create: `internal/identity/identity.go`
- Create: `internal/identity/identity_test.go`
- Create: `internal/identity/identity_windows.go`
- Create: `internal/identity/identity_windows_test.go`

**Interfaces:**
- `Generate() (Identity, error)` creates an X25519 keypair.
- `Load(path string) (Identity, error)` reads a 32-byte private key.
- `Save(path string, id Identity) error` creates the key file with mode `0600` on Unix and owner-only ACL on Windows.
- `PeerID(publicKey []byte) (uint64, error)` returns the first 8 bytes of SHA-256 in big-endian order.
- `EncodeInvite(Invite) (string, error)` and `DecodeInvite(string) (Invite, error)` use `skein:1:<base64url-payload>`.

- [ ] Write tests for generated key length/public-private correspondence and deterministic peer ID derivation.
- [ ] Write tests rejecting invalid private-key lengths, malformed peer IDs, truncated invite payloads, and unsupported invite version `2`.
- [ ] Run `go test ./internal/identity`; expected: the new tests fail because symbols are absent.
- [ ] Implement identity generation with `crypto/ecdh.X25519` and `crypto/rand`; use SHA-256 for the 64-bit peer ID.
- [ ] Reject invites over 512 decoded bytes, invalid numeric `netip.AddrPort` endpoints, unspecified/multicast addresses, invalid ports, and more than 8 endpoints.
- [ ] Implement `Save` with exclusive create; use mode `0600` on Unix and a Windows owner-only DACL; refuse to overwrite an existing identity key.
- [ ] Run `go test ./internal/identity`; expected: PASS, including malformed-input cases.
- [ ] Run `GOOS=windows GOARCH=amd64 go test -c -o /tmp/identity.test.exe ./internal/identity`; expected: cross-compilation succeeds; execution is reserved for Windows CI.
- [ ] Commit `feat(identity): add node keys and invite encoding`.

### Task 3: Implement config parsing and validation

**Files:**
- Create: `internal/conf/config.go`
- Create: `internal/conf/config_test.go`
- Create: `testdata/config/valid.toml`
- Create: `testdata/config/invalid-prefix.toml`

**Interfaces:**
- `Load(path string) (Config, error)` parses TOML and applies defaults.
- `Config` includes configurable IPv4/IPv6 overlay prefixes and local addresses, interface name, UDP listen port, MTU, 32-byte network PSK, 32-byte API token, static peers with an explicit dialer role, and discovery settings.
- `Validate(Config) error` validates prefixes, unicast host addresses, unique assignments, interface name, port, MTU, PSK/token sizes, peer endpoints, route overlaps, and loopback API bind.
- `DefaultPath() (string, error)` uses `os.UserConfigDir()` and appends `skein/skein.toml`.

- [ ] Write table tests for defaults, configurable non-overlapping prefixes, valid dual-stack and IPv4-only configs, duplicate/invalid host addresses, route overlap/default-route rejection, port 0/65536, MTU boundaries including IPv6 requiring ≥1280, PSK/token lengths 0/16/31/32/33, interface-name validation, per-peer dialer role, and non-loopback API bind.
- [ ] Run `go test ./internal/conf`; expected: FAIL until `Config` and functions exist.
- [ ] Add TOML parsing using `github.com/BurntSushi/toml`; parse addresses/prefixes with `net/netip`, PSK/token as base64url, and restrict endpoint strings to numeric IP literals plus ports.
- [ ] Default prefixes from Global Constraints, UDP port to 11010, MTU to 1280, API bind to `127.0.0.1:11011`, interface name to `skein0` on Linux/`Skein` on Windows, and mDNS to true; require a 32-byte PSK, 32-byte API token, and at least one static peer when mDNS is disabled.
- [ ] Runtime rejects group/world-readable secret-bearing config files; `skein init` creates the config, PSK, API token, and identity key with owner-only permissions and refuses overwrite.
- [ ] Validate API host as an exact loopback IP; reject wildcards, hostnames, and non-loopback literals.
- [ ] Run `go test ./internal/conf`; expected: PASS for all boundary/error cases.
- [ ] Run `GOOS=windows GOARCH=amd64 go test -c -o /tmp/conf.test.exe ./internal/conf`; expected: cross-compilation succeeds.
- [ ] Commit `feat(config): parse and validate Skein configuration`.

### Task 4: Implement the bounded wire codec

**Files:**
- Create: `internal/wire/frame.go`
- Create: `internal/wire/frame_test.go`
- Create: `internal/wire/frame_fuzz_test.go`

**Interfaces:**
- `Header` fields: `Type MessageType`, `ToNext uint64`, `ToFinal uint64`, `From uint64`, `TTL uint8`.
- `Marshal(Header, []byte) ([]byte, error)` serializes header and payload.
- `Parse([]byte) (Header, []byte, error)` validates and returns a payload view without copying.
- Maximum frame size is 1400 bytes; maximum payload is 1374 bytes. DATA payload starts with `[key_epoch:4][counter:8]`.

- [ ] Add tests for each type, big-endian byte order, zero/invalid IDs, initial TTL, minimum header size, maximum frame size, DATA epoch/counter prefix, and control-frame encryption envelope.
- [ ] Add tests rejecting unknown types, short/oversized frames, malformed lengths, invalid TTL, unsupported epochs, and type/header tampering.
- [ ] Run `go test ./internal/wire`; expected: FAIL until codec exists.
- [ ] Implement fixed-offset encoding into caller-provided buffers where possible; `Parse` must not allocate.
- [ ] Require receivers to reject frames whose `to_next` is not the local ID; relays rewrite only mutable `to_next` and TTL fields.
- [ ] Add a Go fuzz target that calls `Parse` for arbitrary byte slices and asserts it never panics and never returns an out-of-range payload slice.
- [ ] Run `go test ./internal/wire`; expected: PASS.
- [ ] Run `go test ./internal/wire -run=^$ -fuzz=FuzzParse -fuzztime=10s`; expected: no panic or invalid slice.
- [ ] Commit `feat(wire): add bounded frame codec`.

### Task 5: Implement datagram-safe Noise sessions

**Files:**
- Create: `internal/session/session.go`
- Create: `internal/session/session_test.go`
- Modify: `go.mod`, `go.sum`

- `NewHandshake(local identity.Identity, remotePublic []byte, psk []byte, initiator bool, attemptID [16]byte, epoch uint32) (*Handshake, error)` uses Noise IKpsk2 with X25519, ChaChaPoly, and BLAKE2s.
- `Handshake.WriteMessage(payload []byte) ([]byte, error)` and `Handshake.ReadMessage(message []byte) ([]byte, error)` advance the standard Noise handshake state.
- `Handshake.Split() (*Session, error)` produces directional Noise cipher states only after handshake completion.
- `Session.Seal(messageType MessageType, headerAD, plaintext []byte) (epoch uint32, counter uint64, ciphertext []byte, err error)` uses explicit nonces.
- `Session.Open(messageType MessageType, epoch uint32, counter uint64, headerAD, ciphertext []byte) ([]byte, error)` authenticates before committing the receive replay-window update.
- The peer session manager uses a configured single dialer for WAN peers and lower-ID dialer for mDNS peers; attempt IDs are included in the Noise prologue and encrypted confirmation.

- [ ] Pin `github.com/flynn/noise` in `go.mod`; use supported `IKpsk2` and `CipherState.SetNonce` APIs, never implicit sequential nonces for UDP.
- [ ] Test matching/wrong PSK, wrong responder key, altered type/AD/ciphertext, replay, 64-packet reordering boundary, delayed/simultaneous attempts, encrypted confirmation loss, higher-epoch precedence, lexicographic same-epoch winner agreement, fresh rekey handshake, previous-epoch overlap, expiry, and counter exhaustion.
- [ ] Implement a 64-bit replay bitmap per epoch; update it only after successful AEAD authentication.
- [ ] Serialize nonce selection plus Noise `Encrypt`/`Decrypt` with per-direction locks; include message type, `to_final`, `from`, and epoch in AEAD associated data, but exclude relay-mutable `to_next` and TTL.
- [ ] Initiate rekey with a fresh IKpsk2 handshake at 2^32 DATA packets or one hour, whichever comes first; the lower peer ID initiates over the established route, binds the new epoch in the prologue, and retains only current/previous epochs during bounded grace.
- [ ] Reject stale epochs and epoch wrap; pause DATA rather than reuse a key/nonce if rekey fails, and surface the peer as degraded after bounded retries.
- [ ] Cap handshake bytes to the UDP frame limit, expire pending attempts, and rate-limit handshakes before allocating full session state.
- [ ] Verify final outer length for 1280-byte inner packets: 1382 bytes over IPv6 and 1362 bytes over IPv4.
- [ ] Cross-compile Windows tests with `GOOS=windows GOARCH=amd64 go test -c -o /tmp/session.test.exe ./internal/session`; expected: compilation succeeds.
- [ ] Commit `feat(session): add Noise-authenticated datagram sessions`.

### Task 6: Implement the transport interface and dual-stack UDP link

**Files:**
- Create: `internal/link/link.go`
- Create: `internal/link/udp.go`
- Create: `internal/link/udp_test.go`

**Interfaces:**
- `Link` matches the approved spec: `Send(netip.AddrPort, []byte) error`, `Recv([]byte) (int, netip.AddrPort, error)`, `Observe() (netip.AddrPort, error)`, `LocalAddrs() []netip.AddrPort`, and `Close() error`.
- `ListenUDP(port uint16) (Link, error)` prefers one dual-stack socket and falls back to paired IPv4/IPv6 sockets on the same port when dual-stack is unavailable.
- `Observe()` returns an error in v1; without a configured rendezvous endpoint the UDP socket cannot infer a public mapped address by itself.

- [ ] Test IPv4/IPv6 loopback, source normalization, both socket modes, port-collision rollback, closed-socket behavior, and datagram truncation.
- [ ] Implement with `net.ListenConfig`/`net.UDPConn`; atomically bind both sockets as fallback, closing the first if the second fails.
- [ ] Normalize IPv4-mapped addresses with `netip.Addr.Unmap()`; reject frames over 1400 bytes and detect `MSG_TRUNC` or the platform equivalent on receive.
- [ ] Run `go test ./internal/link`; expected: PASS for IPv4 and IPv6 loopback.
- [ ] Cross-compile tests with `GOOS=windows GOARCH=amd64 go test -c -o /tmp/link.test.exe ./internal/link`; expected: compile passes.
- [ ] Commit `feat(link): add dual-stack UDP transport`.

### Task 7: Implement TUN creation and OS network configuration

**Files:**
- Create: `internal/tun/device.go`
- Create: `internal/netcfg/netcfg.go`
- Create: `internal/netcfg/netcfg_linux.go`
- Create: `internal/netcfg/netcfg_windows.go`
- Create: `internal/netcfg/netcfg_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- `tun.Open(name string, mtu int) (Device, error)` wraps WireGuard's TUN device package.
- `Device` exposes `Read([]byte) (int, error)`, `Write([]byte) (int, error)`, `Name() string`, and `Close() error`.
- `netcfg.Configure(name string, mtu int, addresses []netip.Prefix, routes []netip.Prefix) error` and `netcfg.Remove(name string, addresses, routes []netip.Prefix) error` use per-OS implementations.
- [ ] Test command construction and injected failures; assert Linux `ip` and Windows `netsh` never alter the default route; assert default-route conflicts fail, host-route conflicts warn, stale Skein-interface routes are reconciled, and unrelated routes remain untouched.
- [ ] Add the pinned `golang.zx2c4.com/wireguard/tun` dependency and wrap its packet/buffer API without importing WireGuard dataplane packages.
- [ ] Implement Linux/Windows configuration through argument arrays and a command runner; return command, exit status, and stderr without invoking a shell.
- [ ] Inspect the existing route table before mutation; fail if an overlay prefix would replace a default route, warn on more-specific host-route conflicts, and reconcile only stale overlay routes attached to the named Skein interface. Never delete or rewrite unrelated routes.
- [ ] Run `go test ./internal/tun ./internal/netcfg`; expected: PASS.
- [ ] Cross-build both commands for Windows; expected: Wintun-backed code compiles; the matching DLL is pinned and packaged in Task 17.
- [ ] Commit `feat(platform): add Linux and Windows TUN adapters`.

### Task 8: Implement the peer table and direct path probing

**Files:**
- Create: `internal/peer/table.go`
- Create: `internal/peer/table_test.go`
- Create: `internal/path/candidates.go`
- Create: `internal/path/candidates_test.go`
- Create: `internal/path/probe.go`
- Create: `internal/path/probe_test.go`

**Interfaces:**
- `Peer` contains ID, public key, overlay IPv4/IPv6, candidate `[]netip.AddrPort`, configured dialer role, candidate provenance, direct state, active endpoint, RTT, byte counters, and last-seen time.
- `Prober.Probe(ctx context.Context, peer Peer) (CandidateResult, error)` tests eligible same-family endpoints with bounded concurrency and returns the lowest-RTT validated path.

- [ ] Test peer ID/public-key mismatch, conflicting IDs, endpoint normalization, stable list ordering, and concurrent access under `go test -race`.
- [ ] Test candidate enumeration on IPv4-only, IPv6-only, dual-stack, loopback-only, and no-address interfaces; reject unspecified, loopback, multicast, and unscoped link-local candidates.
- [ ] Test path selection with no-common-family, timeout, RTT winner, endpoint rate limits, configured dialer role, and endpoint migration; do not activate a changed endpoint until a random 128-bit challenge receives a matching authenticated response over the existing session.
- [ ] Implement endpoint migration as a random 128-bit challenge sent over the candidate path and authenticated with the existing session; change the active endpoint only after receiving the matching authenticated response.
- [ ] Implement peer table with `sync.RWMutex`; copy slices/maps on ingress/egress so callers cannot mutate shared state.
- [ ] For static/WAN peers require explicit per-peer dialer role; for mDNS peers use lower peer ID as dialer. Do not infer inbound reachability from interface address class.
- [ ] Run `go test -race ./internal/peer ./internal/path`; expected: PASS.
- [ ] Commit `feat(path): track peers and probe direct endpoints`.

### Task 9: Implement TUN packet plane and end-to-end routing sessions

**Files:**
- Create: `internal/dataplane/engine.go`
- Create: `internal/dataplane/engine_test.go`

**Interfaces:**
- `Engine.Run(ctx context.Context) error` owns TUN read/write loops and link receive loop.
- `Engine.SendOverlayPacket(packet []byte) error` validates the IP header, maps destination overlay IP to peer ID, then encrypts for `to_final`.
- `Engine.HandleFrame(from netip.AddrPort, frame []byte) error` parses the outer frame, authenticates DATA at the final endpoint, or forwards the opaque frame at an intermediate node.

- [ ] Use fake `tun.Device`, fake `Link`, and fake routes to assert packet source/destination validation, IPv4/IPv6 selection, and authenticated session use.
- [ ] Test one-hop Noise handshake and DATA delivery; test relay forwarding and prove the relay cannot decrypt the end-to-end ciphertext.
- [ ] Test altered message type, `to_final`, `from`, epoch, and ciphertext rejection at the final peer; test `to_next` mismatch drops at ingress.
- [ ] Run `go test ./internal/dataplane`; expected: FAIL until engine exists.
- [ ] Validate IP header lengths and versions; require inner source IP to match the local overlay address on send, destination to be a configured remote overlay unicast address, and reject multicast/broadcast/out-of-prefix packets.
- [ ] On receive, require decrypted inner source IP to match the overlay address for authenticated header `from`; reject source spoofing before writing to TUN.
- [ ] On ingress require `to_next == localID`; at a relay drop if TTL ≤1, otherwise decrement once, set `to_next` to the selected neighbor, preserve `from`, `to_final`, epoch, counter, and ciphertext, then forward both HANDSHAKE and established opaque ciphertext.
- [ ] Drop TTL exhaustion, unknown destination/next hop, invalid IP packet, oversized packet, unauthenticated frame, and wrong-session data with typed counters.
- [ ] Run `go test ./internal/dataplane`; expected: PASS for direct transport, malformed packet, and opaque relay cases.
- [ ] Commit `feat(dataplane): route encrypted overlay packets through peers`.

### Task 10: Implement authenticated state gossip and mDNS

**Files:**
- Create: `internal/discovery/state.go`
- Create: `internal/discovery/state_test.go`
- Create: `internal/discovery/mdns.go`
- Create: `internal/discovery/mdns_test.go`

**Interfaces:**
- `State.Encode(local Peer, routes []RouteAdvertisement) ([]byte, error)` creates a bounded STATE payload.
- `State.Merge(from uint64, payload []byte) error` validates identities and merges permitted address/route information.
- `MDNS.Start(ctx context.Context, local Invite, onPeer func(Invite)) error` advertises and listens for `_skein._udp.local`.

- [ ] Test state at zero/five nodes, malformed family, duplicate IDs, invalid key-derived ID, oversized payload, worst-case valid peer/candidate/path inventory, deterministic omission that preserves local/configured records, stale sequence, expired record, forged third-party endpoint, path loops, and route cap.
- [ ] Test mDNS TXT parsing independently; ignore malformed invites, wrong network IDs, and invalid endpoints.
- [ ] Bound each STATE datagram to 1400 bytes with explicit maximums (5 peers, 8 candidates per peer, bounded route path); preserve local/configured records, then sort transitive hints by hop count and peer ID and include in that order until the cap. Reject a local/configured record that cannot fit; never fragment.
- [ ] Authenticate each STATE frame with its session. Treat transitive peer/address data as hints only; install a direct endpoint only after the advertised peer proves its key via Noise, and never let third-party gossip overwrite a configured key.
- [ ] Attach per-origin monotonically increasing sequence and expiry to advertisements; reject replayed/stale records, cycles, more than four hops, and invalid next hops.
- [ ] Implement mDNS using `github.com/grandcat/zeroconf`, publishing identity, a fingerprint derived from the random 32-byte PSK, UDP port, and eligible non-loopback interface endpoints; never publish secrets.
- [ ] Run `go test ./internal/discovery`; expected: PASS; run a loopback mDNS integration test only on CI runners that permit multicast, otherwise mark only that integration case skipped with the specific OS reason.
- [ ] Cross-build for Windows; expected: compile passes.
- [ ] Commit `feat(discovery): add authenticated state gossip and mDNS`.

### Task 11: Implement route computation and forwarding state

**Files:**
- Create: `internal/route/table.go`
- Create: `internal/route/table_test.go`

**Interfaces:**
- `DirectLink{PeerID uint64, RTT time.Duration}`.
- `Advertisement{Destination uint64, Cost time.Duration, Hops uint8, Path []uint64}`.
- `Route{Destination uint64, NextHop uint64, Cost time.Duration, Hops uint8}`.
- `Table.UpdateDirect(DirectLink)`, `Table.RemoveDirect(peerID)`, `Table.MergeAdvertisement(from uint64, Advertisement) error`, `Table.Lookup(destination uint64) (Route, bool)`, `Table.AdvertisementsFor(peerID uint64) []Advertisement`.

- [ ] Test direct routes, two-hop bridge, five-node line (four links), deterministic equal-cost tie-break, lower-cost replacement, withdrawal, split horizon, path-loop rejection, and rejection of routes longer than four links.
- [ ] Run `go test ./internal/route`; expected: FAIL until table exists.
- [ ] Implement bounded distance-vector relaxation; cost is the sum of measured RTTs, and ties resolve on lexicographically smaller peer ID for deterministic convergence.
- [ ] Track full peer path in advertisements for loop detection; do not rely on split horizon alone.
- [ ] On direct link loss, withdraw routes using that next hop and recompute from remaining advertisements.
- [ ] Run `go test ./internal/route`; expected: PASS, including three-node A→B→C route selection and loop rejection.
- [ ] Integrate `route.Table` with `dataplane.Engine` through `RouteLookup`; add the full end-to-end relay test using three route tables and a forwarding node that never holds the A↔C session.
- [ ] Commit `feat(route): compute bounded mesh routes`.

### Task 12: Implement keepalive, path failover, and counters

**Files:**
- Create: `internal/control/control.go`
- Create: `internal/control/control_test.go`
- Modify: `internal/peer/table.go`, `internal/path/probe.go`, `internal/route/table.go`

**Interfaces:**
- `Controller.Run(ctx context.Context) error` schedules keepalive, timeout detection, state sync, and reprobe.
- `Controller.HandlePing(from uint64, payload []byte) []byte` returns an authenticated PONG containing the sequence and echoed monotonic timestamp.

- [ ] Use a fake clock to test encrypted PING at 15 s, peer-down after 45 s without authenticated traffic, and retry delays 1/2/4/30-second cap.
- [ ] Test path-loss recovery, preservation of overlay addresses, and rekey timer/packet threshold with lost or reordered transition frames.
- [ ] Implement counters for rx/tx, route miss, TTL exhaustion, oversize, invalid packet, auth failure, handshake failure, probe failure, and rekey failure; expose immutable snapshots.
- [ ] Run `go test -race ./internal/control ./internal/peer ./internal/path ./internal/route`; expected: PASS.
- [ ] Commit `feat(control): add keepalive and path recovery`.

### Task 13: Implement daemon lifecycle and authenticated local API

**Files:**
- Create: `internal/api/types.go`
- Create: `internal/api/server.go`
- Create: `internal/api/server_test.go`
- Create: `internal/daemon/daemon.go`
- Modify: `cmd/skeind/main.go`

**Interfaces:**
- `daemon.New(Config) (*Daemon, error)`, `Daemon.Run(ctx context.Context) error`, `Daemon.Close() error`.
- `api.NewServer(token string, StateReader, PeerWriter, Prober) http.Handler`.
- API response types are versioned DTOs; no internal Go structs are serialized directly.

- [ ] Add `httptest` tests for every endpoint in spec: status, peers, routes, config redaction, add/remove peer, probe, and SSE event delivery.
- [ ] Implement exact full paths `/api/v1/status`, `/api/v1/peers` (GET/POST), `/api/v1/routes`, `/api/v1/config`, `/api/v1/peers/{id}` (DELETE), `/api/v1/peers/{id}/probe` (POST), and `/api/v1/events`.
- [ ] Require constant-time bearer-token comparison; reject any browser `Origin`, send no CORS headers, cap bodies at 16 KiB and concurrent SSE clients, and rate-limit peer mutations/probes.
- [ ] Implement SSE with bounded per-client queues; on overflow send a `resync` event rather than blocking the dataplane.
- [ ] Compose link, TUN, peer table, sessions, discovery, control, routing, dataplane, and API in `Daemon`; close all resources on context cancellation.
- [ ] Replace the initial `skeind` command behavior with `skeind run -c <path>` and non-zero exit codes on invalid config, privilege failure, and listener collision.
- [ ] Run `go test -race ./internal/api ./internal/daemon ./cmd/skeind`; expected: PASS.
- [ ] Commit `feat(daemon): expose authenticated local control API`.

### Task 14: Implement the complete CLI

**Files:**
- Create: `internal/cli/cli.go`
- Create: `internal/cli/cli_test.go`
- Modify: `cmd/skein/main.go`

**Interfaces:**
- `cli.Run(ctx context.Context, args []string, stdout, stderr io.Writer) int` returns process exit code.
- API client has explicit methods matching each REST endpoint and a configurable local API URL/token source.

- [ ] Add command parsing tests for `init`, `genkey`, `invite`, `status`, `peers list|add|rm`, `routes`, `probe`, `ping`, and `version`; unknown commands/missing args return 2 with usage.
- [ ] Implement `init` to create a random 32-byte network PSK, random 32-byte API token, identity key, and owner-only default config without overwriting existing files; print only out-of-band values the operator must share.
- [ ] Implement `genkey` and `invite` offline; never print private key, PSK, or API token except the one-time `init` output with explicit disclosure.
- [ ] Implement daemon-dependent commands over authenticated HTTP; `skein` reads the API token from the owner-only config, supports `--json`, and never includes the token in logs/output.
- [ ] Implement `ping` with OS-specific argument arrays, bounded context, and overlay-IP validation; never invoke a shell or accept arbitrary binary/flags.
- [ ] Run `go test ./internal/cli ./cmd/skein`; expected: PASS for parsing, auth, API errors, and command output.
- [ ] Cross-build both binaries for Linux amd64 and Windows amd64; expected: all builds pass.
- [ ] Commit `feat(cli): add Skein management commands`.

### Task 15: Implement system service installation

**Files:**
- Create: `internal/service/service_linux.go`
- Create: `internal/service/service_windows.go`
- Create: `internal/service/service_test.go`
- Modify: `cmd/skeind/main.go`

**Interfaces:**
- `Install(executable, configPath string) error` and `Uninstall() error` are OS-specific.
- Service execution calls `skeind run -c <configPath>` and does not store secrets in service metadata.

- [ ] Test systemd unit generation, Windows SCM argument quoting, service stop/shutdown cleanup, and partial installation rollback.
- [ ] Implement Linux systemd and Windows SCM install/uninstall; Windows daemon entry must use `svc.Run`/control handling, report SERVICE_STOP/SERVICE_SHUTDOWN, and close TUN/routes cleanly.
- [ ] Store no secrets in service metadata; use explicit executable/config args, owner-only config ACLs, and clean partial installs on failure.
- [ ] Add commands `skeind install-service` and `skeind uninstall-service`; require root/Administrator and print exact start/stop commands.
- [ ] Run package tests and Linux cross-build; Windows CI compiles service code; expected: PASS.
- [ ] Commit `feat(service): add systemd and Windows service support`.

### Task 16: Add end-to-end Linux namespace tests

**Files:**
- Create: `test/netns/setup.sh`
- Create: `test/netns/cleanup.sh`
- Create: `test/netns/run.sh`
- Create: `test/netns/README.md`
- Create: `internal/integration/netns_test.go`

- Harness creates per-run namespaces named `skein-<uid>-<8-hex-run-id>-{n4,n6,ndual}`, with veth links and deterministic interface addresses; it passes those exact names to configs and removes only those exact namespaces.

- [ ] Write cleanup first; accept only the exact `skein-<uid>-<8-hex-run-id>` prefix form, delete only the three exact suffix names, and test that absent or unrelated namespaces remain untouched.
- [ ] Generate a random 8-hex run ID, verify all three names are absent, then create the veth topology: n4 has only IPv4, n6 only IPv6, ndual both; disable IPv6 in n4 and IPv4 in n6; allow UDP 11010 in namespace firewall rules.
- [ ] Add a preflight test that verifies each namespace address/family before starting daemon processes.
- [ ] Run three daemon instances with config fixtures; assert n4↔ndual direct v4 and n6↔ndual direct v6 in the API.
- [ ] Add a five-node line-topology route test in the pure Go route suite; assert four-link reachability and path length five.
- [ ] Run `sudo ./test/netns/run.sh`; expected: every listed connectivity assertion passes and cleanup leaves no namespace bearing that run's unique prefix.
- [ ] Commit `test(integration): verify dual-stack bridging in netns`.

### Task 17: Verify Windows runtime and release packaging

**Files:**
- Create: `test/windows/smoke.ps1`
- Create: `release/windows/README.md`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Windows release archive contains `skeind.exe`, `skein.exe`, and the matching `wintun.dll`.

- [ ] Extend CI to cross-build both Windows binaries and verify the release artifact layout; do not claim runtime success from cross-compilation alone.
- [ ] Write `smoke.ps1` to check elevation, start two local daemons with separate API/UDP ports and distinct configured interface names, verify routes, ping the remote overlay address, stop both, and remove only test-owned routes/interfaces.
- [ ] Pin the official Wintun DLL release/version and verify its SHA-256 before packaging; include its license/notices in the archive.
- [ ] Run the script in a Windows VM; expected: overlay ping succeeds, routes and processes are removed on stop, service lifecycle works with a config path containing spaces, and mDNS is tested on a multicast-capable LAN.

### Task 18: Measure and document the project

**Files:**
- Create: `README.md`
- Create: `docs/protocol.md`
- Create: `docs/measurements.md`
- Modify: `docs/superpowers/specs/2026-11-01-skein-mesh-vpn-design.md` to align the contract with wire epochs/overhead, four-link routing, dialer policy, and explicit remaining limitations.

**Interfaces:**
- README documents Linux/Windows setup, privileges, config, CLI, API link, limitations, and how to reproduce the netns demonstration.
- Protocol document records byte-level header, Noise pattern, nonce/replay behavior, state gossip, routing, and forwarding.

- [ ] Record baseline `ping` RTT and throughput for each namespace link before Skein is started.
- [ ] Measure direct v4/v6 handshake time, RTT inflation, throughput at 1280 MTU, CPU at 100 Mbps (or report the highest available test rate), route convergence, and lower-MTU IPv4-only behavior; store raw commands and output.
- [ ] Verify outer packet length with `tcpdump`; expected maximum DATA packet is 1382 bytes over IPv6 and 1362 bytes over IPv4 for a 1280-byte inner packet.
- [ ] Document which tests require root, a Windows VM, a multicast-capable LAN, and external connectivity; distinguish measured results from expected behavior and provide IPv4-only MTU workarounds.
- [ ] Run final `go test -race ./...`, Linux/Windows builds, namespace integration suite, and Windows VM smoke; report each actual result separately.
- [ ] Update the design spec with the protocol/config contract implemented by this plan; include residual connectivity, PMTU, traffic-analysis, and shared-PSK-member boundaries.
- [ ] Commit `docs: document Skein setup, protocol, and measurements`.

---
## Self-review against the approved spec

| Spec requirement | Plan coverage |
|---|---|
| Linux and Windows TUN, address, route, MTU | Tasks 7, 17 |
| Daemon, full CLI, UI-agnostic API + SSE | Tasks 13, 14 |
| Link interface now, TCP later | Tasks 6, future transport remains out of scope |
| Dual overlay IPv4 + IPv6 ULA | Tasks 3, 7, 9, 16 |
| Noise end-to-end sessions, PSK, replay, forwarding privacy | Task 5, Task 9 |
| Direct path probes, RTT selection, keepalive, failover | Tasks 8, 12 |
| mDNS, invite, authenticated state gossip | Tasks 2, 10 |
| Bounded route table, bridge forwarding, TTL, split horizon | Tasks 9, 11, 16 |
| MTU 1280, no fragmentation | Tasks 4, 6, 9, 18 |
| systemd and Windows Service | Task 15 |
| Linux netns test and Windows smoke | Tasks 16, 17 |
| measurements and user-facing docs | Task 18 |

### Clarifications made while planning

- Noise IKpsk2 with the random PSK uses the standard `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` pattern.
- Explicit nonce counters and per-epoch replay windows support UDP reordering; rekey uses a fresh IKpsk2 handshake bound to the next epoch, with current/previous epoch overlap.
- DATA adds a 32-bit key epoch, changing per-packet overhead from 50 to 54 bytes.
- A static WAN peer needs one configured dialer; mDNS LAN peers elect the lower peer ID. Automatic NAT-role detection is excluded.
- `Link.Observe()` returns an error because no rendezvous endpoint exists; static endpoints and eligible interface addresses are used instead.
- A relay forwards initial Noise handshakes and established ciphertext without decrypting.
- A 1280-byte inner packet requires 1382 bytes over IPv6 and 1362 bytes over IPv4; dual-stack paths below 1382-byte PMTU remain unsupported.
- Endpoint migration requires a random 128-bit challenge and authenticated response over the candidate path before the active endpoint changes.
- Windows cross-compilation is not runtime verification; Task 17 requires a Windows VM with Wintun and route cleanup.

## Plan self-review

- **Coverage:** every approved goal is covered; explicit v1 boundaries are listed in “Detailed limitations and mitigations.”
- **Unresolved v1 boundaries:** CGNAT-to-CGNAT and UDP-blocked paths; link-local/firewall-dependent mDNS; more than five nodes or four route links; dual-stack PMTU below 1382; abrupt power-loss cleanup; malicious shared-PSK members; traffic-analysis resistance; same-user API isolation; and per-peer revocation.
- **Security checks:** random 32-byte PSK/API token, owner-only files, constant-time API auth, authenticated control/data, nonce epochs, replay windows, bounded state, route validation, and redacted output are explicit.
- **Platform checks:** Linux netns is runnable locally; Windows service/Wintun requires a Windows VM; mDNS runtime verification requires multicast-capable networking.
- **Type consistency:** peer IDs are `uint64`; route/path APIs use `netip.AddrPort`, `time.Duration`, and the same `Header`/`Link` shapes defined in Tasks 4–6.
- **Sequence:** identity/config/wire/session precede links and dataplane; state precedes route integration; API precedes CLI; Linux and Windows end-to-end checks follow implementation.
