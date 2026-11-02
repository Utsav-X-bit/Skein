# Skein Mission-Control TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a responsive, keyboard-navigable Skein dashboard that truthfully visualizes routes, live throughput, latency, and authenticated mesh activity telemetry.

**Architecture:** Preserve the existing standard-library ANSI renderer and alternate-screen lifecycle. Split dashboard state, layout, rendering, topology, and motion into focused Go files; use a declared screen mode for every terminal size. Add bounded aggregate link-activity records to authenticated STATE gossip, store remote records in an expiry cache, and expose them through the local API. The dashboard joins this data with the local route path already held by the route table.

**Tech Stack:** Go 1.26, standard library, `golang.org/x/term`, existing authenticated STATE protocol, existing local HTTP/JSON API, Go testing.

## Global Constraints

- Keep the dashboard read-only: keyboard input may change local dashboard state only.
- Retain the current Linux and Windows terminal support and use no additional Go module dependency.
- Never render a line wider than the terminal width supplied to the renderer.
- Do not synthesize topology edges: route paths and activity records are the only edge sources.
- Activity telemetry carries aggregate counters and performance only; it must not contain packet payloads, overlay addresses, ports, or flow identifiers.
- Preserve a useful static result with `--no-motion`, non-TTY output, no color, and no Unicode support.
- Bound every new protocol list/cache and reject malformed, stale, replayed, or oversized data.

---

### Task 1: Expose full local route paths through the API

**Files:**
- Modify: `internal/api/types.go`
- Modify: `internal/api/server_test.go`
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/dashboard_test.go`

**Interfaces:**
- Consumes: `route.Route.Path []uint64`, whose final ID is `Destination`.
- Produces: `api.Route.Path []string` and `cli.routeDTO.Path []string`, each value formatted as a zero-padded 16-character hex peer ID.

- [ ] **Step 1: Write the failing API route-path test**

In `internal/api/server_test.go`, make the fake state return a route with a two-hop path and assert the JSON body preserves it:

```go
func (f *fakeState) Routes() []Route {
	return []Route{{
		Destination: "0123456789abcdef", NextHop: "1111111111111111",
		CostMs: 12.4, Hops: 2,
		Path: []string{"1111111111111111", "0123456789abcdef"},
	}}
}

func TestRoutesIncludePath(t *testing.T) {
	rec := do(t, NewServer(token, fakeState{}, fakeWriter{}, fakeProber{}).Handler(), http.MethodGet, "/api/v1/routes", "", token)
	var routes []Route
	if err := json.Unmarshal(rec.Body.Bytes(), &routes); err != nil { t.Fatal(err) }
	if got, want := routes[0].Path, []string{"1111111111111111", "0123456789abcdef"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `go test ./internal/api -run TestRoutesIncludePath -count=1`

Expected: compile failure because `Route.Path` does not exist.

- [ ] **Step 3: Add the API and daemon fields**

Add the JSON field and serialize the existing route-table data; do not calculate another path in the daemon:

```go
// internal/api/types.go
type Route struct {
	Destination string   `json:"destination"`
	NextHop     string   `json:"next_hop"`
	CostMs      float64  `json:"cost_ms"`
	Hops        uint8    `json:"hops"`
	Path        []string `json:"path"`
}

// internal/daemon/daemon.go, inside Daemon.Routes
row := api.Route{
	Destination: fmt.Sprintf("%016x", r.Destination),
	NextHop:     fmt.Sprintf("%016x", r.NextHop),
	CostMs:      float64(r.Cost) / float64(time.Millisecond),
	Hops:        r.Hops,
}
for _, id := range r.Path {
	row.Path = append(row.Path, fmt.Sprintf("%016x", id))
}
out = append(out, row)
```

Extend `routeDTO` with `Path []string` and print `path` only in the ordinary CLI route output when it is non-empty, so the existing dashboard can consume it without another DTO.

- [ ] **Step 4: Add dashboard coverage for a relay path**

Update `TestMeshHandlesRelayedPaths` to provide `Path: []string{"1111111111111111", "2222222222222222"}` and assert the renderer can distinguish it from a direct route.

- [ ] **Step 5: Run tests and format**

Run: `gofmt -w internal/api/types.go internal/api/server_test.go internal/daemon/daemon.go internal/cli/cli.go internal/cli/dashboard_test.go && go test ./internal/api ./internal/daemon ./internal/cli -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the route-path slice**

```bash
git add internal/api/types.go internal/api/server_test.go internal/daemon/daemon.go internal/cli/cli.go internal/cli/dashboard_test.go
git commit -m "feat(api): expose full route paths"
```

### Task 2: Add a bounded, expiring activity cache

**Files:**
- Create: `internal/activity/cache.go`
- Create: `internal/activity/cache_test.go`

**Interfaces:**
- Consumes: core `activity.Record` values; Task 3 will serialize the same type into authenticated STATE messages.
- Produces: `activity.Record`, `activity.Cache.Put(origin uint64, seq uint64, record activity.Record, expires time.Time)`, `activity.Cache.Snapshot(now time.Time) []activity.Sample`, and `activity.Cache.Prune(now time.Time)`.

- [ ] **Step 1: Write failing cache tests**

Create `internal/activity/cache_test.go` with deterministic timestamps. Cover accepted insertion, replacement only by a higher sequence, expiry removal, capacity eviction, and a copied result:

```go
func TestCacheKeepsFreshestSample(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewCache(2)
	r := Record{Destination: 2, RxBytes: 50, TxBytes: 10, RTT: 4 * time.Millisecond}
	c.Put(1, 7, r, now.Add(time.Second))
	c.Put(1, 6, Record{Destination: 2, RxBytes: 1}, now.Add(time.Second))
	got := c.Snapshot(now)
	if len(got) != 1 || got[0].RxBytes != 50 || got[0].Seq != 7 { t.Fatalf("snapshot = %+v", got) }
}

func TestCacheExpiresAndBoundsEntries(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewCache(2)
	for _, tc := range []struct {
		origin, destination uint64
		expires             time.Time
	}{
		{1, 2, now.Add(2 * time.Second)},
		{1, 3, now.Add(3 * time.Second)},
		{1, 4, now.Add(4 * time.Second)},
	} {
		c.Put(tc.origin, uint64(tc.destination), Record{Destination: tc.destination, ObservedAt: now}, tc.expires)
	}
	got := c.Snapshot(now)
	if len(got) != 2 || got[0].Destination != 3 || got[1].Destination != 4 { t.Fatalf("bounded snapshot = %+v", got) }
	if got := c.Snapshot(now.Add(4 * time.Second)); len(got) != 0 { t.Fatalf("expired snapshot = %+v", got) }
}
```

- [ ] **Step 2: Run the focused cache tests to verify they fail**

Run: `go test ./internal/activity -count=1`

Expected: FAIL because package `internal/activity` has not been created.

- [ ] **Step 3: Implement the cache with no background goroutine**

Create `internal/activity/cache.go` with the following public shape. Guard the map with `sync.RWMutex`; make `Snapshot` prune expired entries while holding the write lock and sort by origin then destination for deterministic API responses.

```go
package activity

type Sample struct {
	Origin      uint64
	Destination uint64
	Seq         uint64
	RxBytes     uint64
	TxBytes     uint64
	RTT         time.Duration
	Jitter      time.Duration
	Transport   string
	Direct      bool
	ObservedAt  time.Time
	ExpiresAt   time.Time
}

type Transport uint8
const (
	TransportUnknown Transport = iota
	TransportUDP
	TransportTCP
	TransportMixed
)

func TransportFromStrings(tx, rx string) Transport
func (t Transport) String() string

type Record struct {
	Destination uint64
	RxBytes     uint64
	TxBytes     uint64
	RTT         time.Duration
	Jitter      time.Duration
	Transport   Transport
	Direct      bool
	ObservedAt  time.Time
	Path        []uint64
}

type cacheKey struct { origin, destination uint64 }

type Cache struct {
	mu       sync.RWMutex
	capacity int
	samples  map[cacheKey]Sample
}

func NewCache(capacity int) *Cache
func (c *Cache) Put(origin, seq uint64, record Record, expires time.Time)
func (c *Cache) Snapshot(now time.Time) []Sample
func (c *Cache) Prune(now time.Time)
```

Reject zero origin/destination, non-positive expiry, and records whose expiry is not after their observation timestamp. The map key is `{origin, destination}`. When full, evict the sample with the earliest `ExpiresAt`, breaking ties by origin then destination.

- [ ] **Step 4: Run and format the cache package**

Run: `gofmt -w internal/activity && go test ./internal/activity -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the cache**

```bash
git add internal/activity
git commit -m "feat: add expiring mesh activity cache"
```

### Task 3: Carry aggregate activity in authenticated STATE gossip

**Files:**
- Modify: `internal/discovery/state.go`
- Modify: `internal/discovery/state_test.go`

**Interfaces:**
- Consumes: the existing encrypted/authenticated `wire.State` payload and `discovery.State` origin sequence and expiry.
- Produces: `State.Activity []activity.Record`, `Build(records, routes, activity, seq, ttl, now)`, and `Merger.OnActivity func(origin, seq uint64, records []activity.Record, expires time.Time)`.

- [ ] **Step 1: Write wire round-trip and rejection tests**

Add an activity record to `TestStateRoundTrip` and assert all fields survive `Encode`/`Decode`. Add table cases that reject an invalid activity count, a zero destination, an invalid transport byte, an impossible route path, and a truncated activity payload. Add a merger test that verifies `OnActivity` receives the authenticated `State.Origin`, its sequence, copied records, and the state expiry.

Use this test record:

```go
Activity: []activity.Record{{
	Destination: b.ID, RxBytes: 4096, TxBytes: 2048,
	RTT: 7 * time.Millisecond, Jitter: time.Millisecond,
	Transport: activity.TransportUDP, Direct: true,
	ObservedAt: time.Now().Add(-time.Second),
	Path: []uint64{b.ID},
}},
```

- [ ] **Step 2: Run discovery tests to verify they fail**

Run: `go test ./internal/discovery -run 'TestStateRoundTrip|TestMerger' -count=1`

Expected: compile failure because `State.Activity` does not exist.

- [ ] **Step 3: Define a compact, versioned record format**

Increase `Version` to `4` while keeping `minVersion = 3`; decode versions 3 and 4. A version-3 state ends after routes with no activity records. Add these constants and types:

```go
const MaxActivity = 8
```

Import `internal/activity` and define `State.Activity []activity.Record`. After routes, v4 encodes one activity count byte and at most eight records. Encode fixed-width numeric fields (`destination`, `rx`, `tx`, `observed-at Unix milliseconds`, `rtt microseconds`, `jitter microseconds`), then a flags byte, a transport byte, a one-byte path length, and path IDs. Reject an activity path longer than `MaxPath`, a non-direct record whose path is empty or ends outside its destination, an unknown flag/transport value, and any total state exceeding `maxStateBytes`.

Update `Build` to take activities and retain records in deterministic destination order only while the full state remains under the frame cap. It must retain the local peer record even when every activity record is dropped for size.

- [ ] **Step 4: Forward accepted telemetry from the merger**

After peer and route merging succeeds, call `OnActivity` only when it is non-nil and `len(s.Activity) > 0`. Supply defensive copies. The callback receives the state origin; records do not redundantly encode origin or sequence.

- [ ] **Step 5: Run all discovery tests and fuzz-style malformed coverage**

Run: `gofmt -w internal/discovery/state.go internal/discovery/state_test.go && go test ./internal/discovery -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the protocol change**

```bash
git add internal/discovery/state.go internal/discovery/state_test.go
git commit -m "feat(discovery): gossip bounded activity telemetry"
```

### Task 4: Publish local activity, retain remote samples, and expose `/activities`

**Files:**
- Modify: `internal/control/control.go`
- Modify: `internal/control/control_test.go`
- Modify: `internal/api/types.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/daemon_test.go`

**Interfaces:**
- Consumes: `peer.Table.List()`, route paths, the Task 2 cache, and Task 3 `State.Activity`.
- Produces: `api.Activity`, `StateReader.Activities() []Activity`, authenticated `GET /api/v1/activities`, and local publisher records emitted through `Controller.statePayload`.

- [ ] **Step 1: Write failing controller publisher tests**

Add a test that gives the controller a direct UDP peer with known counters, calls `statePayload` twice one second apart, decodes the second payload, and expects one record with the directional counter delta, current RTT, and `Path: []uint64{peerID}`. Add a second test with zero counter delta and assert it publishes no record.

- [ ] **Step 2: Run focused controller tests to verify they fail**

Run: `go test ./internal/control -run TestStatePayloadPublishesActivity -count=1`

Expected: FAIL because `statePayload` has no activity publisher.

- [ ] **Step 3: Add controller-side sampling without leaking application data**

Add a `trafficPrev map[uint64]trafficSample` and `now func() time.Time`-protected sequence state to `Controller`. In `statePayload`, sample only `p.State == peer.StateUp && p.Direct`; subtract prior values only when counters are monotonic, then store the new sample. For each non-zero delta, derive a path from `routes.Lookup(p.ID)` and emit this record:

```go
activity.Record{
	Destination: p.ID, RxBytes: rxDelta, TxBytes: txDelta,
	RTT: p.RTT, Jitter: 0, Transport: activity.TransportFromStrings(p.Transport, p.RxTransport),
	Direct: true, ObservedAt: now, Path: []uint64{p.ID},
}
```

Add `lastRTT map[uint64]time.Duration` to calculate `Jitter` as the absolute difference between the new and previous non-zero RTT. Pass the records to `discovery.Build(records, routes, activities, seq, discovery.TTLFor, now)`.

- [ ] **Step 4: Write failing API and daemon tests**

Add a fake state that implements `Activities` and an authenticated API test asserting `GET /api/v1/activities` serializes an empty array instead of `null` and rejects absent bearer authentication. Add a daemon test that invokes the merger callback with a remote record and verifies `Daemon.Activities()` returns its hex IDs, directions, milliseconds, freshness timestamps, and path.

- [ ] **Step 5: Implement cache/API plumbing**

Add this API shape and endpoint:

```go
type Activity struct {
	Origin      string   `json:"origin"`
	Destination string   `json:"destination"`
	Seq         uint64   `json:"seq"`
	RxBytes     uint64   `json:"rx_bytes"`
	TxBytes     uint64   `json:"tx_bytes"`
	RTTMs       float64  `json:"rtt_ms"`
	JitterMs    float64  `json:"jitter_ms"`
	Transport   string   `json:"transport"`
	Direct      bool     `json:"direct"`
	ObservedMS  int64    `json:"observed_ms"`
	ExpiresMS   int64    `json:"expires_ms"`
	Path        []string `json:"path"`
}
```

Extend `StateReader`, register `GET /api/v1/activities`, and follow the existing routes handler's empty-array behavior. Construct `activity.NewCache(32)` in `Daemon.New`, set `merger.OnActivity` to put every validated record into it, and map cache snapshots to `api.Activity` in `Daemon.Activities`. Use only signed timestamp differences supplied by the daemon clock; the dashboard must not trust an unvalidated remote wall clock.

- [ ] **Step 6: Run telemetry/API test suite**

Run: `gofmt -w internal/control internal/api internal/daemon && go test ./internal/activity ./internal/discovery ./internal/control ./internal/api ./internal/daemon -count=1`

Expected: PASS.

- [ ] **Step 7: Commit telemetry exposure**

```bash
git add internal/control internal/api internal/daemon
git commit -m "feat: expose authenticated mesh activity"
```

### Task 5: Establish dashboard state, metrics, and responsive layout primitives

**Files:**
- Create: `internal/cli/dashboard_state.go`
- Create: `internal/cli/dashboard_layout.go`
- Create: `internal/cli/dashboard_state_test.go`
- Create: `internal/cli/dashboard_layout_test.go`
- Modify: `internal/cli/dashboard.go`
- Modify: `internal/cli/cli.go`

**Interfaces:**
- Consumes: `/status`, `/peers`, `/routes`, `/activities`, terminal `(width,height)`, and raw key bytes.
- Produces: `dashboardMode`, `dashboardFocus`, `dashboardState`, `linkMetrics`, `resolveLayout(width,height) layout`, and `dashboard.handleKey(byte) bool` where the result requests exit.

- [ ] **Step 1: Write failing layout and state tests**

Create a table-driven layout test with widths/heights `40x12`, `80x24`, `120x36`, and `160x45`. Assert modes `short`, `compact`, `standard`, and `wide`, every panel rectangle stays inside the supplied bounds, and no rectangle has a negative size. Create state tests covering `Tab`, `h/l`, arrows and `j/k`, `Enter`, `Esc`, `Space`, `?`, `q`, and selected-peer removal.

```go
func TestResolveLayoutNeverExceedsTerminal(t *testing.T) {
	for _, tc := range []struct{ w, h int; want dashboardMode }{{40, 12, modeShort}, {80, 24, modeCompact}, {120, 36, modeStandard}, {160, 45, modeWide}} {
		got := resolveLayout(tc.w, tc.h)
		if got.mode != tc.want { t.Fatalf("%dx%d = %v", tc.w, tc.h, got.mode) }
		for _, r := range got.rects { if r.x < 0 || r.y < 0 || r.right() > tc.w || r.bottom() > tc.h { t.Fatalf("out of bounds: %+v", r) } }
	}
}
```

- [ ] **Step 2: Run dashboard-state tests to verify they fail**

Run: `go test ./internal/cli -run 'TestResolveLayout|TestDashboardKey' -count=1`

Expected: compile failure because layout and key model types do not exist.

- [ ] **Step 3: Create focused dashboard state types**

Put navigation and rolling measurements in `dashboard_state.go`:

```go
type dashboardMode uint8
const ( modeShort dashboardMode = iota; modeCompact; modeStandard; modeWide )
type dashboardFocus uint8
const ( focusOverview dashboardFocus = iota; focusMesh; focusPeers; focusRoutes; focusEvents; focusHelp )
type linkMetrics struct { rx, tx, peak, rtt, rttMin, rttMax, jitter float64; samples []metricSample }
type dashboardState struct { focus dashboardFocus; selected string; inspector, paused bool; help bool }
```

Give metric history a maximum of 30 one-second samples. `observePeer` computes directional rates only from monotonic counters, keeps `rttMin/rttMax` over the window, and calculates jitter from consecutive non-zero RTT values. `handleKey` changes only `dashboardState`; it never calls HTTP or daemon mutation code.

- [ ] **Step 4: Add the geometry resolver and remove artificial width inflation**

Create `dashboard_layout.go` with `rect`, `layout`, `resolveLayout`, and `visibleWidth`. The renderer must use the actual terminal width (including widths below 64), use a single-column no-border compact composition before a line would overflow, and choose short mode before vertically truncating a panel. Remove the current `if width < 64 { width = 64 }` behavior from `draw`.

- [ ] **Step 5: Add the activities DTO/poll and atomic view snapshot**

Add `activityDTO` matching `api.Activity`, fetch `/api/v1/activities` within `poll`, and copy it into `view`. Store the dashboard's interactive state and metric map under the existing mutex. A failed activities request follows the same stale/error path as the other dashboard API calls.

- [ ] **Step 6: Run focused CLI tests**

Run: `gofmt -w internal/cli && go test ./internal/cli -run 'TestResolveLayout|TestDashboardKey|TestRatesAndFormatting' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit dashboard state foundations**

```bash
git add internal/cli/dashboard.go internal/cli/dashboard_state.go internal/cli/dashboard_layout.go internal/cli/dashboard_state_test.go internal/cli/dashboard_layout_test.go internal/cli/cli.go
git commit -m "feat(tui): add responsive dashboard state"
```

### Task 6: Render the mission-control panels and truthful route graph

**Files:**
- Create: `internal/cli/dashboard_render.go`
- Create: `internal/cli/dashboard_graph.go`
- Create: `internal/cli/dashboard_render_test.go`
- Create: `internal/cli/dashboard_graph_test.go`
- Modify: `internal/cli/dashboard.go`
- Modify: `internal/cli/dashboard_test.go`

**Interfaces:**
- Consumes: `view`, `layout`, `dashboardState`, `routeDTO.Path`, and `linkMetrics`.
- Produces: `renderDashboard(v view, l layout, state dashboardState, width int) []string` and `buildRouteGraph(selfID string, peers []peerDTO, routes []routeDTO) routeGraph`.

- [ ] **Step 1: Write graph truthfulness tests**

Create a graph fixture for `self → relay → destination`. Assert it emits exactly two edges (`self,relay` and `relay,destination`) and no edge `self,destination`. Add direct and unavailable-peer fixtures. Add a test where a route has no usable `Path`; expect a single labelled inferred next-hop edge, not a complete peer clique.

- [ ] **Step 2: Run graph tests to verify they fail**

Run: `go test ./internal/cli -run TestBuildRouteGraph -count=1`

Expected: compile failure because `buildRouteGraph` does not exist.

- [ ] **Step 3: Implement graph construction and panel renderers**

`dashboard_graph.go` maps peer IDs to metadata and deduplicates undirected hop edges by `{from,to}`. It retains a route's `destination`, `nextHop`, path, and inferred flag on the resulting edge so labels can explain it.

`dashboard_render.go` contains independent renderers for `Overview`, `Mesh`, `Peers`, `Routes`, `Events`, `Inspector`, and `Help`. Render the selected row with reverse video or a textual `>` marker in no-color mode. The inspector must show RX/TX current/peak, RTT current/range, jitter, last-seen, transport, direct/relayed, hops, and freshness.

Make `dashboard.build` delegate to `resolveLayout` and these panel renderers. Retire the old all-sections-in-one-box renderer only after the equivalent static snapshot paths are covered by tests.

- [ ] **Step 4: Write width and content snapshot tests**

Use a `lineWidth` helper that strips ANSI and counts runes. Build all four layouts from the same direct/relay/TCP fixture and assert every line is at most the passed width, compact contains a panel navigator, wide contains peer/map/inspector headings, short contains a navigation hint, and the inspector includes `RTT`, `jitter`, `RX`, and `TX`.

- [ ] **Step 5: Run renderer tests and existing dashboard tests**

Run: `gofmt -w internal/cli && go test ./internal/cli -run 'TestBuildRouteGraph|TestMissionControl|TestDashboard' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the rendered dashboard**

```bash
git add internal/cli/dashboard.go internal/cli/dashboard_render.go internal/cli/dashboard_graph.go internal/cli/dashboard_render_test.go internal/cli/dashboard_graph_test.go internal/cli/dashboard_test.go
git commit -m "feat(tui): render responsive route map"
```

### Task 7: Add data-driven motion, stale states, and end-to-end verification

**Files:**
- Create: `internal/cli/dashboard_motion.go`
- Create: `internal/cli/dashboard_motion_test.go`
- Modify: `internal/cli/dashboard.go`
- Modify: `internal/cli/dashboard_render.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: fresh local rates, remote `activityDTO`, route graph edges, `view.phase`, `dashboardState.paused`, and `view.err`.
- Produces: `motionPacket` positions derived from fresh observed data and visible stale/recovery labels.

- [ ] **Step 1: Write failing motion tests**

Create tests that assert zero-rate/expired records create zero packets; a fresh local TX record creates a packet travelling source-to-destination; a fresh RX record reverses it; a paused state creates zero packets; and a relay packet uses the two graph edges rather than jumping from source to destination. Add a stale-error renderer test asserting it retains a `last updated` age plus `disconnected from daemon`.

- [ ] **Step 2: Run motion tests to verify they fail**

Run: `go test ./internal/cli -run 'TestMotion|TestStaleDashboard' -count=1`

Expected: compile failure because `motionPackets` does not exist.

- [ ] **Step 3: Implement bounded packet scheduling**

Create:

```go
type motionPacket struct { edge edgeKey; t float64; reverse bool; remote bool }
func motionPackets(v view, graph routeGraph, paused bool, now time.Time) []motionPacket
```

Discard activity whose `ExpiresMS` is not after `now.UnixMilli()`. Convert each active rate/sample to at most two packets per directed edge, cap the total frame to 24 packets, and calculate `t` from `phase` plus a stable ID hash. Only `view.motion && !paused` permits packets. Pulse and changed-state highlights must use the same pause gate; health, text, and error indicators remain visible when paused.

- [ ] **Step 4: Render the motion and recovery states**

Draw packet glyphs after static route lines and before node labels so labels remain readable. Select ASCII `o` and Unicode `●` according to capability. Add a footer showing `SPACE pause/resume`, current focus, motion state, and `? help`; in error state add the age of `lastFetch` and retain the last valid metrics without extrapolation.

- [ ] **Step 5: Document user-visible controls and metrics**

Replace the README's one-line dashboard feature description with the actual keys, responsive behavior, activity-telemetry privacy boundary, and definitions of RX/TX speed, RTT, and jitter. State that mesh-wide activity reflects authenticated aggregate samples and may be marked stale.

- [ ] **Step 6: Run formatting, targeted tests, race tests, and the full suite**

Run: `gofmt -w internal/cli && go test ./internal/activity ./internal/discovery ./internal/control ./internal/api ./internal/daemon ./internal/cli -count=1 && go test -race ./internal/activity ./internal/discovery ./internal/control ./internal/api ./internal/daemon ./internal/cli -count=1 && go test ./... -count=1`

Expected: every command exits 0.

- [ ] **Step 7: Commit the completed TUI**

```bash
git add internal/cli README.md
git commit -m "feat(tui): animate measured mesh activity"
```

## Coverage review

| Approved requirement | Planned tasks |
|---|---|
| Responsive compact, standard, wide, and short terminal modes | 5, 6, 7 |
| Keyboard-only navigation and no network mutation | 5, 6 |
| Direct/relayed truthful route graph | 1, 6, 7 |
| Measured speed, RTT, RTT range, jitter, recency, transport | 4, 5, 6 |
| Mesh-wide aggregate activity with privacy bounds and expiry | 2, 3, 4 |
| Activity-driven motion and pause | 7 |
| Static/no-color/no-Unicode/disconnected fallbacks | 5, 6, 7 |
| Unit, resize, protocol, API, renderer, race, and full-suite verification | 1–7 |
