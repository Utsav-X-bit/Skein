# Measurements

This file records what was actually run and what still requires privileges or a
Windows VM. Do not copy expected numbers into the "recorded" column: rerun the
commands and paste the output.

## Environment

| Item | Value |
|---|---|
| Host | Linux x86-64 workstation |
| Go | `go1.27.3` |
| Root available | no (netns and TUN runs were skipped here) |
| Windows VM | not available in this environment |

## What was run

```bash
go vet ./...
go test -race ./...
GOOS=windows GOARCH=amd64 go build ./cmd/skeind ./cmd/skein
go test ./internal/integration -run TestOverlayOverLoopbackUDP -v
```

Recorded result: all packages pass; the loopback integration test completes a
real UDP handshake and delivers an IPv4 overlay packet end to end in well under
a second (the test asserts within a 5 s deadline).

## Protocol overhead (from the implementation)

A 1280-byte inner packet produces:

| Underlay | Bytes |
|---|---|
| IPv6 | 1382 (26 header + 12 nonce + 16 tag + 1280 + 8 UDP + 40 IPv6) |
| IPv4 | 1362 (… + 8 UDP + 20 IPv4) |

`internal/wire` enforces the 1400-byte frame cap and `internal/session`
verifies the outer length in its tests.

## Not yet measured (requires root or a Windows VM)

Run these after `sudo -E env "PATH=$PATH" ./test/netns/run.sh` has built the
topology, or replace the namespace names with the run prefix printed by
`setup.sh`.

### Baseline before starting Skein

```bash
ip netns exec <n4> ping -c 20 192.168.10.2
ip netns exec <n6> ping -c 20 fd00:10::2
```

Record min/avg/max RTT for each underlay link.

### Handshake time

Start one daemon per namespace, then time the first peer transition:

```bash
time (until ip netns exec <n4> skein -c <cfg> peers --json | grep -q '"up"'; do sleep 0.05; done)
```

Do this once for the IPv4 pair (n4 ↔ ndual) and once for the IPv6 pair
(n6 ↔ ndual).

### RTT inflation

```bash
ip netns exec <n4> ping -c 20 10.144.144.2      # overlay
ip netns exec <n4> ping -c 20 192.168.10.2      # underlay
```

Report the difference.

### Throughput and MTU

```bash
ip netns exec <ndual> iperf3 -s &
ip netns exec <n4> iperf3 -c 10.144.144.2 -t 10
```

Repeat with `-M 1200` and with the config's `mtu` lowered to 1200 on an
IPv4-only node. Record bits per second and retransmissions.

### Outer packet size

```bash
ip netns exec <ndual> tcpdump -ni any -c 20 'udp port 11010' -vv
```

A 1280-byte inner packet must show 1382 bytes over IPv6 and 1362 bytes over
IPv4.

### Failover and convergence

```bash
# While pinging across the overlay, stop one daemon:
ip netns exec <ndual> pkill -f 'skeind run'
```

Record the time until the overlay ping resumes and the route table settles.

### CPU at 100 Mbps

```bash
ip netns exec <n4> top -b -n 1 -p $(pgrep -f 'skeind run' | head -1)
```

Record the percentage while iperf3 runs at the highest sustainable rate.

## Windows

`test/windows/smoke.ps1` is the runtime check: two daemons, distinct interface
names, a config path containing spaces, an overlay ping, and cleanup of the
Wintun interfaces. Cross-compilation is not evidence for any of this.
