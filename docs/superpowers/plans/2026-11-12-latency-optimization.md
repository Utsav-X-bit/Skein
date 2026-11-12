# Skein Latency Optimization Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to execute this plan task-by-task. This is an evidence-gated investigation and optimization plan, not authorization to apply every candidate optimization below.

**Goal:** Identify and reduce Skein-attributable RTT inflation and latency spikes on the real Linux/Windows connection without sacrificing reliability, security, or throughput.

**Architecture:** Measure the underlay, encrypted control path, overlay ICMP, and application separately. Reuse existing diagnostics, UDP integration tests, and network-namespace topology; instrument only the boundary that existing evidence cannot resolve. Change one demonstrated bottleneck at a time and compare the same workload before and after.

**Tech Stack:** Go, UDP, Linux TUN, Windows Wintun, native ping, packet captures, Go profiles/traces, existing integration tests.

## Global constraints

- Planning only in this change. Do not alter the running service, Windows installation, router, firewall, or system-wide networking settings while preparing the plan.
- Build on existing automatic MTU negotiation; do not reimplement it or assume it fixes small-packet latency.
- No new transport, protocol revision, dependency, worker pool, batching delay, persistent telemetry service, or kernel rewrite without evidence that the simpler path cannot solve the measured issue.
- Preserve authentication, replay protection, nonce uniqueness, source validation, bounded memory, cancellation, and safe buffer ownership.
- No disabling encryption, turning off the firewall, busy spinning, real-time priority, or globally disabling power management as a default optimization.
- Record raw measurements and failures. Report timeouts separately; never discard them to improve percentile results.
- Never infer live Windows performance from a Linux test or a cross-build.
- Use local/synthetic tests in subagents; real-network measurements require the parent or an operator with access to both endpoints.

## Correct the premise

The earlier diagnosis was too strong:

- RTT values near multiples of a baseline do not establish retransmissions, Nagle delays, or any particular cause.
- ICMP echo does not use TCP. A lost echo normally produces a timeout, not a TCP retransmission-delayed reply. Minecraft/application latency is a different measurement.
- A quoted dataplane p99 of 0.21 ms cannot explain or exclude a real-world 2 ms gap without the original workload, harness, and timestamp boundaries. The inspected `BenchmarkEngineThroughput` reports benchmark throughput, time/op, and allocations; it does not establish live RTT percentiles.
- Increasing MTU may improve large-transfer efficiency. It does not establish lower small-packet RTT or fewer Nagle stalls. Existing TCP flows do not simply renegotiate their MSS when the interface MTU changes.
- Public-address ICMP and encapsulated UDP can receive different network treatment. Their RTT difference is a useful comparison, not a pure measurement of encryption overhead.

## Existing code and tools to reuse

| Location | Observed behavior / role |
|---|---|
| `internal/dataplane/engine.go:250-407` | One TUN read loop and one link read loop; outbound validation, encryption, routing. Zero-length TUN reads back off 1 ms; transient link errors back off 10 ms. Neither is a normal per-packet delay. |
| `internal/dataplane/engine.go:550-665` | Inbound decryption, synchronous control callbacks, and TUN delivery share the receive path. Measure callback and TUN-write duration before changing scheduling. |
| `internal/link/udp.go:60-180` | Requests 4 MiB socket buffers. Paired sockets use a 256-entry channel, allocate/copy received packets, and silently drop on channel saturation. Single-socket mode bypasses that channel. Confirm which mode the affected host uses. |
| `internal/tun/device.go:47-140` | Existing batched reads with pending packets, separate read/write locks, and reused write scratch. Do not add duplicate batching or a shared read/write lock. |
| `internal/control/control.go:567-752` | Authenticated ping handling and candidate probing. `Probe` can change the active endpoint: it is not a passive high-frequency latency sampler. |
| `internal/cli/cli.go:1252-1265` | `skein ping` invokes native ping for four samples. Use native ping directly for a meaningful distribution. |
| `internal/daemon/mtu.go`, `internal/dataplane/engine.go:737-865` | Existing once-per-connection MTU aggregation and authenticated DF probes. Preserve as the baseline and test separately from small-packet jitter. |
| `internal/dataplane/engine_test.go:565-587` | Existing `BenchmarkEngineThroughput`; useful for isolated CPU/allocation comparisons, not live RTT claims. |
| `internal/integration/udp_test.go` | Real loopback UDP tests, including `TestPathMTUProbeOverUDP`. |
| `test/netns/{setup,run,cleanup}.sh`, `internal/integration/netns_test.go` | Existing privileged Linux IPv4/IPv6/relay topology and real daemon integration. |
| `test/windows/smoke.ps1` | Real Windows runtime smoke coverage. Read its setup requirements before execution. |
| `docs/measurements.md` | Existing measurement record. Append dated evidence; distinguish historical results from new results. |

## Task 1: Establish a reproducible baseline

**Files:** Read `docs/measurements.md` and the relevant existing harness. Append actual results to `docs/measurements.md` during execution; no production edits.

**Consumes:** Running endpoints, installed build identities, active endpoint/route information, physical and overlay interface names.

**Produces:** Raw samples and a scenario table, including build, OS, IP family, route, packet size, load, loss, and RTT distribution.

- [ ] Resolve endpoint addresses and interface names from the running configuration/status and OS, not guesses. Record direct versus relay, selected next hop, Wi-Fi versus Ethernet, socket mode, configured and effective MTU, daemon debug logging, CPU load, and power mode on both hosts. Do not export identities, secrets, or tokens.
- [ ] Warm up the session, then measure underlay and overlay concurrently from the same host, with the same payload and cadence. Repeat from the other endpoint. Keep startup/reconnect observations in separate runs.
- [ ] Collect at least 1,000 samples per path per run. Linux examples, after setting `UNDERLAY_IP` and `OVERLAY_IP` from the recorded endpoints, in two terminals:

```bash
ping -n -D -i 0.2 -c 1000 -W 1 -s 56 "$UNDERLAY_IP"
ping -n -D -i 0.2 -c 1000 -W 1 -s 56 "$OVERLAY_IP"
```

Windows examples in two PowerShell terminals, with the corresponding variables set from recorded endpoints:

```powershell
ping.exe -n 1000 -w 1000 -l 56 $UNDERLAY_IP
ping.exe -n 1000 -w 1000 -l 56 $OVERLAY_IP
```

Windows native ping has a different cadence and timing precision; record them and compare baseline/candidate on the same platform. Preserve command output in per-run files. If public ICMP is blocked or rate-limited, mark that baseline unavailable rather than treating its timeouts as UDP loss.

- [ ] Run idle, controlled upload, and controlled download scenarios separately. Use an authorized peer's iperf3 server if available; measure sustainable throughput first, then test near 80% and near saturation. Do not mix direction or offered load between baseline and candidate.
- [ ] Record median, p95, p99, maximum, loss, and fraction of successful replies above 20 ms. Define percentiles using nearest rank over successful replies; state sample count and report loss beside them. Preserve per-run results instead of pooling away instability.
- [ ] Capture the game's reported latency and server tick/load metrics separately under the same scenarios. Do not mix application timings into the ICMP distribution.

**Gate:** A repeatable distinction between underlay-wide jitter, overlay-only jitter, and application-only jitter, or an explicit finding that more samples are needed. No optimization claim yet.

## Task 2: Locate the delay boundary

**Files:** Inspect the packet-path files above. Use temporary instrumentation only if packet captures and existing counters cannot localize the delay; remove it after diagnosis unless it proves necessary for a reproducible regression check.

**Consumes:** Task 1 scenario and raw data.

**Produces:** A timestamped causal trace for spikes, with a named component or a clearly bounded unresolved interval.

- [ ] Capture on both hosts: physical interface for the actual UDP endpoint/port, overlay interface for ICMP or the selected application flow. Linux capture templates, after resolving variables:

```bash
sudo tcpdump -ni "$PHYSICAL_IF" -s 128 -w underlay.pcap "udp port $SKEIN_PORT"
sudo tcpdump -ni "$OVERLAY_IF" -s 128 -w overlay.pcap 'icmp or icmp6'
```

Use Windows Wireshark/Npcap on the physical and Wintun adapters when installed; otherwise use supported Windows packet/ETW capture and document any missing boundary. Keep captures local and access-restricted. Capture only the selected test flow for application diagnosis; even truncated packets may expose data.

- [ ] Correlate inner ICMP sequence numbers and outer authenticated frame identity fields within each host. If captures cannot unambiguously map inner/outer packets, use a bounded diagnostic mapping of sequence to frame counter, without logging decrypted payloads or keys. Do not subtract timestamps from different hosts to claim one-way latency without an independently established clock-error bound. Capture points bracket kernel/userspace work but do not isolate encryption by themselves.
- [ ] Record local intervals: overlay outbound to physical outbound; physical inbound to overlay inbound; and peer-side echo/application turnaround. Compare low-latency samples with spikes.
- [ ] Compare packet-drop/oversize/authentication/route counters before and after the run, plus OS socket drops and interface errors. On Linux, inspect `ss -u -a -n -m -p`, `ip -s link`, and `tc -s qdisc show`; distinguish host-wide counters from Skein-specific evidence. Inspect actual socket buffer sizes rather than assuming the 4 MiB request succeeded.
- [ ] If delay is inside the daemon, obtain a short CPU, allocation, blocking, mutex, and scheduler trace from a local diagnostic build or existing test harness. Do not expose a public profiling endpoint. Measure instrumentation overhead against an uninstrumented run.
- [ ] Specifically measure whether the 10 ms error backoff is entered during spikes, whether synchronous control processing or TUN writes block receiving, and whether the paired-socket queue saturates. A timer value or allocation in source code alone is not evidence of the cause.

Existing isolated benchmark/profile command (not a live RTT test):

```bash
go test ./internal/dataplane -run '^$' -bench '^BenchmarkEngineThroughput$' -benchmem -count=5
```

**Gate:** Name the demonstrated delay/drop source and supporting trace. If underlay or application accounts for it, stop proposing dataplane changes and move to the appropriate conditional action below.

## Task 3: Apply only the supported optimization

**Files:** Only the demonstrated owning component and its existing tests. Before changing an exported symbol, resolve its callers with language-server references. Read complete affected functions and caller contracts before editing.

**Consumes:** Task 2 evidence.

**Produces:** One minimal candidate change, a targeted behavioral regression check where needed, and a before/after measurement for the same scenario.

| Evidence required | Conditional action | Likely scope |
|---|---|---|
| Both underlay and overlay spike together under load | Test queue management/pacing on the actual bottleneck, or an Ethernet comparison for Wi-Fi jitter. Apply router/system changes only with permission and a rollback. | Deployment/network, not necessarily Skein code. |
| Active path is unnecessarily relayed or switches to a measurably worse candidate | Correct the demonstrated selection/reconvergence behavior; retain reachability and failover. Do not force direct or IPv6 as universal winners. | `internal/control/control.go`, `internal/path/probe.go`, route owner identified through references. |
| Paired-socket channel saturation or allocation/GC correlates with spikes | Address measured queue service or buffer ownership. Consider bounded reuse only if allocation is material; preserve close/unblock semantics. Do not just increase queue capacity. | `internal/link/udp.go` and existing link tests. |
| Control callback, lock, or TUN write stalls the data receive loop | Shorten the measured critical section or separate only the proven blocking work with bounded ownership and ordering. Do not add arbitrary packet workers. | `internal/dataplane/engine.go`, `internal/control/control.go`, `internal/tun/device.go` as demonstrated. |
| Transient receive errors precede the 10 ms stalls | Correct the error source or use an error-specific bounded recovery policy. Preserve cancellation and prevent busy loops. Never remove error handling merely to hide the delay. | `internal/dataplane/engine.go`, platform error handling identified by references. |
| Profiling shows material per-packet allocation/copy cost | Reuse existing buffers or construct the frame with fewer copies, respecting AEAD nonce/replay rules and asynchronous buffer lifetime. | `internal/dataplane/engine.go`; session/wire owner only if profiling requires it. |
| Only large packets fail or retransmit; captures indicate MTU trouble | Verify probe/data route equivalence and packet sizes first. Exercise asymmetric paths, mixed direct/relay traffic, a later smaller-path peer, and reconnect during probing. Fix only the demonstrated MTU lifecycle/path issue. | Existing `internal/daemon/mtu.go`, control/dataplane probe code, relevant tests. |
| ICMP stays stable but TCP/application stalls | Examine retransmissions, receive-window stalls, ACK timing, application writes, and game-server ticks. Test TCP_NODELAY only at the owning application's socket if captures implicate small-write interaction. | Application/host settings; Skein's UDP socket has no TCP Nagle setting. |

- [ ] State a single hypothesis, the exact trace supporting it, and predicted effect before editing.
- [ ] Create or reuse the smallest behavioral reproduction. For timing-only performance changes use the repeatable measurement workload rather than fragile wall-clock assertions in CI.
- [ ] Implement the minimal owning-component change; no unrelated cleanup or speculative optimization.
- [ ] Run the narrow relevant check once after the change. For concurrency changes, run the affected package with `-race`; verify shutdown and buffer ownership. For packet changes, retain validation, replay, oversize, and real UDP coverage.
- [ ] Reject candidates that improve only a microbenchmark while leaving the reported end-to-end issue unchanged.

**Gate:** The demonstrated defect is corrected or the candidate is rejected. If evidence points outside Skein, deliver the measured diagnosis and reversible operational recommendation instead of manufacturing a code change.

## Task 4: Validate end to end and set the stop condition

**Files:** Relevant existing tests; append the result to `docs/measurements.md`. Update `CHANGELOG.md` and relevant usage documentation only for a shipped behavior change.

**Consumes:** Baseline and one candidate.

**Produces:** Comparable A/B measurements, regression evidence, and a ship/reject decision.

- [ ] Run at least three alternating baseline/candidate pairs on the affected real hosts with identical packet size, offered load, path, and MTU. Record path changes; stratify or repeat changed-path runs rather than silently comparing different paths.
- [ ] Proposed optimization acceptance: at least 20% lower p95 and lower p99 in the reproducible affected scenario, with improvement larger than baseline run-to-run spread. No increased loss, reconnect failures, or more than 5% throughput regression. If the actual defect affects rare events rather than p95, define its failure-rate criterion from the baseline before changing code and report unchanged percentiles honestly.
- [ ] For idle controlled direct-link tests, use no more than 1 ms additional median RTT as an investigation target, not a promised WAN result or a brittle CI assertion. Never promise every WAN sample will remain below 10 or 20 ms.
- [ ] Exercise idle and loaded traffic, IPv4/IPv6, direct/relay, and reconnect/MTU transitions where the changed path applies. Run actual Windows traffic for a Windows latency claim.
- [ ] Select verification proportionally. Existing relevant commands are:

```bash
# Real UDP probe behavior, when probing/MTU changes:
go test ./internal/integration -run '^TestPathMTUProbeOverUDP$' -count=1

# MTU application behavior, when aggregation/application changes:
go test ./internal/daemon -run '^TestNegotiatedMTU' -count=1

# Privileged real TUN/dual-stack/relay proof when packet-path changes require it:
sudo -E env "PATH=$PATH" ./test/netns/run.sh

# Compile portability only; not Windows runtime evidence:
GOOS=windows GOARCH=amd64 go build ./cmd/skeind ./cmd/skein
```

The daemon suite previously encountered user-namespace socket/chown failures. Use an isolated supported environment for affected tests; do not change ownership or permissions on the live installation to make tests pass. Report environment failures explicitly, not as passes.

- [ ] Remove diagnostic scaffolding, keep only consumer-visible regression coverage, and record instrumented versus production-build results. After code changes, run `graphify update .` as required by the repository.

**Stop condition:** Ship only the measured improvement that satisfies the applicable criterion. Otherwise retain the working baseline and document the actual limiting component and the next evidence needed. No speculative rewrite.

## Deliverable

A dated measurement table containing scenario, build, path/family, effective MTU, sample count, median/p95/p99/max RTT, loss, above-20-ms rate, throughput, and CPU; the localized root cause; the minimal accepted change (or operational recommendation); exact checks exercised; and limitations of any unavailable Windows/remote capture.

No latency results, root-cause confirmation, or performance improvement are claimed by creating this plan.
