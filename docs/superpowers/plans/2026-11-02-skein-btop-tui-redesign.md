# Skein Btop-Style TUI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Skein's stacked dashboard report with a dense, btop-inspired, responsive network-monitoring TUI carrying a terminal-native woven Skein brand mark.

**Architecture:** Keep the dashboard's API, topology, metric, keyboard, and activity models. Make the rendering layer a rectangle-based card compositor: a brand/metric header, responsive map/peer/detail panels, and activity footer. The renderer chooses truecolor semantic tokens when color is enabled and an ASCII mark when `SKEIN_ASCII=1` or color/Unicode output is disabled.

**Tech Stack:** Go 1.26, standard library, `golang.org/x/term`, ANSI/truecolor escape sequences, current Skein dashboard model and telemetry API.

## Global Constraints

- Preserve existing truthful route and authenticated activity topology; style must not create a link.
- Keep the dashboard read-only and all existing keyboard navigation semantics.
- Use no additional Go module dependency and preserve Linux/Windows terminal support.
- All visible output must fit the detected width; every card rectangle must remain within detected bounds.
- `--no-motion`, non-TTY output, disabled color, and `SKEIN_ASCII=1` must show static readable end states.
- Motion is packet/activity-driven, pausable with `Space`, capped to the existing 80 ms frame cadence, and never applied to idle links.
- The portable dashboard renders a Unicode/ASCII interpretation of `Logo.png`; it must not rely on Kitty, iTerm2, or Sixel image protocols.

---

### Task 1: Define screen geometry and lock responsive layout behavior

**Files:**
- Modify: `internal/cli/dashboard_state.go`
- Modify: `internal/cli/dashboard_test.go`
- Modify: `internal/cli/dashboard_render.go`

**Interfaces:**
- Produces: `resolveLayout(width, height int) dashboardLayout`, `dashboardRect`, and explicit `header`, `metrics`, `mesh`, `peers`, `detail`, and `footer` rectangles.
- Consumes: actual terminal dimensions from `dashboard.draw` and existing `dashboardMode` breakpoints.

- [ ] **Step 1: Add failing geometry tests**

Add tests that define the exact responsive contract:

```go
func TestWideLayoutUsesWholeScreenAsCards(t *testing.T) {
	l := resolveLayout(160, 45)
	if l.mode != modeWide { t.Fatalf("mode = %v", l.mode) }
	if got := l.rects[focusMesh]; got.w < 72 || got.h < 28 { t.Fatalf("mesh = %+v", got) }
	if got := l.rects[focusPeers]; got.w < 28 || got.h < 20 { t.Fatalf("peers = %+v", got) }
	if got := l.rects[focusOverview]; got.w < 34 || got.h < 20 { t.Fatalf("detail = %+v", got) }
	assertRectsInBounds(t, l, 160, 45)
}

func TestMediumLayoutPrioritizesMap(t *testing.T) {
	l := resolveLayout(110, 36)
	if l.mode != modeStandard { t.Fatalf("mode = %v", l.mode) }
	if l.rects[focusMesh].h <= l.rects[focusPeers].h { t.Fatalf("map=%+v peers=%+v", l.rects[focusMesh], l.rects[focusPeers]) }
	assertRectsInBounds(t, l, 110, 36)
}
```

- [ ] **Step 2: Run the focused tests to confirm the pre-redesign layout fails**

Run: `go test ./internal/cli -run 'TestWideLayoutUsesWholeScreenAsCards|TestMediumLayoutPrioritizesMap' -count=1`

Expected: FAIL because the current layout lacks complete rectangles or does not allocate the required map area.

- [ ] **Step 3: Implement complete layout rectangles**

Make `resolveLayout` assign non-overlapping rectangles for all modes. `dashboardLayout` must include:

```go
type dashboardLayout struct {
	mode dashboardMode
	brand, metrics, footer dashboardRect
	rects map[dashboardFocus]dashboardRect
}
```

For wide mode, reserve three header rows, two metric rows, one footer row, then use a 28-column peer column, a 34-column detail column, and give all remaining columns to the map. For standard mode, allocate brand/metric rows, at least half the remaining height to `focusMesh`, then a bounded peer/detail lower strip. Compact retains exactly one full focused panel. Short remains the minimal status view.

- [ ] **Step 4: Make all dashboard rendering consume rectangles**

Change `buildSized` to pass layout rectangles to panel renderers and use a row compositor for wide mode. Delete the experimental height constants (`min(11, ...)`, fixed peer counts, and sequential section assumptions) once the rectangle paths are used. Do not truncate a wide layout after composing it; rectangle sizing must make it fit beforehand.

- [ ] **Step 5: Run format and focused layout tests**

Run: `gofmt -w internal/cli/dashboard_state.go internal/cli/dashboard_render.go internal/cli/dashboard_test.go && go test ./internal/cli -run 'TestWideLayout|TestMediumLayout|TestMissionControlAdapts' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit layout geometry**

```bash
git add internal/cli/dashboard_state.go internal/cli/dashboard_render.go internal/cli/dashboard_test.go
git commit -m "feat(tui): compose dashboard as responsive cards"
```

### Task 2: Add Skein brand art and semantic terminal palette

**Files:**
- Create: `internal/cli/dashboard_brand.go`
- Create: `internal/cli/dashboard_brand_test.go`
- Modify: `internal/cli/dashboard.go`
- Modify: `internal/cli/dashboard_render.go`
- Modify: `README.md`

**Interfaces:**
- Produces: `brandLines(elapsed time.Duration, unicode, motion bool) []string`, `dashboardPalette`, `dashboard.paintRole(role colorRole, s string) string`, and `dashboard.unicode bool`.
- Consumes: `Logo.png` as the visual source, `SKEIN_ASCII`, `--no-color`, `--no-motion`, and the dashboard start time.

- [ ] **Step 1: Write failing brand-frame tests**

Create tests that freeze the startup reveal at zero, midpoint, and final time, then verify the final frame contains the full mark and wordmark while ASCII mode contains only ASCII characters:

```go
func TestBrandRevealSettlesToSkeinWordmark(t *testing.T) {
	start := brandLines(0, true, true)
	mid := brandLines(300*time.Millisecond, true, true)
	end := brandLines(800*time.Millisecond, true, true)
	if strings.Contains(strings.Join(start, "\n"), "Skein") { t.Fatal("wordmark appeared before the mark") }
	if !strings.Contains(strings.Join(end, "\n"), "Skein") { t.Fatalf("end = %q", end) }
	if len(mid) != len(end) { t.Fatalf("mid=%q end=%q", mid, end) }
}

func TestASCIIBrandContainsNoUnicode(t *testing.T) {
	for _, r := range strings.Join(brandLines(time.Second, false, false), "\n") {
		if r > 127 { t.Fatalf("non-ASCII rune %q", r) }
	}
}
```

- [ ] **Step 2: Run the focused brand tests to verify they fail**

Run: `go test ./internal/cli -run 'TestBrandReveal|TestASCIIBrand' -count=1`

Expected: compile failure because `brandLines` does not exist.

- [ ] **Step 3: Implement the terminal-native infinity mark**

In `dashboard_brand.go`, define a static final Unicode mark and its ASCII fallback, preserving the woven-infinity outline without attempting to render the PNG directly:

```go
var unicodeMark = []string{
	"╭╲╲    ╱╱╮",
	" ╲╲╲╳╱╱╱ ",
	"╰╱╱    ╲╲╯",
}
var asciiMark = []string{
	"/\\    /\\",
	" \\\\--// ",
	"\\/    \\/",
}
```

Reveal one outer-to-centre strand every 60 ms, show the wordmark at 420 ms, and show the static final frame immediately when motion is disabled. The reveal duration must not exceed 600 ms. Keep `Logo.png` as the committed source-art asset and add a short README attribution sentence that identifies it as the source for the terminal mark.

Define terminal color roles using truecolor codes only when `d.color` is true: brand `38;2;94;234;212`, traffic `38;2;96;165;250`, healthy `38;2;74;222;128`, warning `38;2;251;191;36`, error `38;2;248;113;113`, dim `38;2;100;116;139`, and border `38;2;51;65;85`. When color is off, return plain text. Read `SKEIN_ASCII` once in `newDashboard` to select the fallback.

- [ ] **Step 4: Replace the generic header**

Render the brand mark/wordmark in the brand rectangle. Put node ID, overlay, version, uptime, and health in small adjacent status fields. Do not draw the old `SKEIN // MISSION CONTROL` full-width title or theme-dependent cyan horizontal rule.

- [ ] **Step 5: Run brand tests and static dashboard snapshots**

Run: `gofmt -w internal/cli/dashboard_brand.go internal/cli/dashboard_brand_test.go internal/cli/dashboard.go internal/cli/dashboard_render.go && go test ./internal/cli -run 'TestBrand|TestDashboardBuildShowsMeshState' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit brand and palette**

```bash
git add Logo.png README.md internal/cli/dashboard_brand.go internal/cli/dashboard_brand_test.go internal/cli/dashboard.go internal/cli/dashboard_render.go
git commit -m "feat(tui): add Skein terminal brand and palette"
```

### Task 3: Build btop-style metric, peer, and detail cards

**Files:**
- Create: `internal/cli/dashboard_cards.go`
- Create: `internal/cli/dashboard_cards_test.go`
- Modify: `internal/cli/dashboard_render.go`
- Modify: `internal/cli/dashboard_test.go`

**Interfaces:**
- Produces: `renderMetricStrip(v view, r dashboardRect) []string`, `renderPeerCard(v view, r dashboardRect) []string`, `renderDetailCard(v view, r dashboardRect) []string`, and `card(title string, body []string, r dashboardRect) []string`.
- Consumes: existing `linkMetrics`, peer selection, route context, and activity freshness.

- [ ] **Step 1: Write failing card-content tests**

Add a fixture with two up peers, current traffic, RTT samples, a TCP peer, and an activity record. Assert the metric strip contains `MESH`, `DOWN`, `UP`, `RTT`, and `JITTER`; the peer card contains selection plus transport; and the detail card contains current RX/TX, peak, RTT range, jitter, route mode, and last-seen age.

- [ ] **Step 2: Run focused card tests to verify they fail**

Run: `go test ./internal/cli -run 'TestMetricStrip|TestPeerCard|TestDetailCard' -count=1`

Expected: compile failure because card renderers do not exist.

- [ ] **Step 3: Implement fixed-bound card renderer**

`card` draws a rounded box only when both dimensions are at least 3; otherwise it returns fitted body lines without a border. It must always return exactly `r.h` strings of visible width `r.w`. Use `paintRole` for the title and semantic values, but every state must retain an explicit text label.

The metric strip has three compact fields: mesh health (`up/down`), aggregate directional speed (`↓/↑` plus sparkline), and selected/current RTT plus jitter. The peer card contains the keyboard-selected list and a one-line state/transport/rate summary. The detail card gives the selected peer's full performance context without repeating a wide table.

- [ ] **Step 4: Integrate cards into wide and standard layouts**

Wide mode uses peer, map, and detail rectangles directly. Standard mode uses the metric strip followed by the map, then a two-column peer/detail lower strip when width permits. Reuse the existing compact focus renderers below 90 columns.

- [ ] **Step 5: Run card/layout snapshot tests**

Run: `gofmt -w internal/cli/dashboard_cards.go internal/cli/dashboard_cards_test.go internal/cli/dashboard_render.go internal/cli/dashboard_test.go && go test ./internal/cli -run 'TestMetricStrip|TestPeerCard|TestDetailCard|TestWideLayout|TestMissionControl' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit dashboard cards**

```bash
git add internal/cli/dashboard_cards.go internal/cli/dashboard_cards_test.go internal/cli/dashboard_render.go internal/cli/dashboard_test.go
git commit -m "feat(tui): render btop-style network cards"
```

### Task 4: Refine map/event motion and visual fallbacks

**Files:**
- Modify: `internal/cli/dashboard_render.go`
- Modify: `internal/cli/dashboard_state.go`
- Modify: `internal/cli/dashboard_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `view.phase`, known route segments, activity expiry, fresh byte deltas, `dashboardState.paused`, `view.motion`.
- Produces: bounded packet positions with 320–600 ms route travel, startup brand phase, and final static cards for no-motion mode.

- [ ] **Step 1: Write failing behavior tests**

Add tests proving a paused/no-motion dashboard has no packet glyphs but retains cards; idle links have no packet glyphs; a fresh direct or remote activity record emits a packet only on a reported route segment; and the compact footer contains the entire shortened key legend without clipping.

- [ ] **Step 2: Run focused motion tests to verify they fail**

Run: `go test ./internal/cli -run 'TestPausedDashboard|TestIdleMap|TestFreshActivity|TestCompactFooter' -count=1`

Expected: FAIL until the new timing and compact-footer behaviors exist.

- [ ] **Step 3: Implement bounded motion timing**

Replace the current hash-speed pulse with normalized activity rates mapped into 320–600 ms travel. Apply a cubic ease-out approximation to discrete frame progress (`1 - (1-t)^3`) so packets settle rather than mechanically advancing at a constant visual speed. Limit a frame to 24 packet glyphs, never animate an expired/zero-rate activity, and leave health/card text static while paused.

Use the existing 80 ms ticker. The only continuous loop is the low-amplitude local health indicator, with a 1.8 second period; it is also disabled by pause/no-motion.

- [ ] **Step 4: Document controls, fallback, and motion semantics**

Update the dashboard README section with the btop-style modes, wordmark/mark fallback, `SKEIN_ASCII=1`, truecolor fallback, all keys, and the rule that packet motion means fresh measured or authenticated aggregate activity.

- [ ] **Step 5: Run formatting and the full suite**

Run: `gofmt -w internal/cli && go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit motion and documentation**

```bash
git add README.md internal/cli/dashboard_render.go internal/cli/dashboard_state.go internal/cli/dashboard_test.go
git commit -m "feat(tui): refine dashboard motion and fallbacks"
```

## Coverage review

| Approved requirement | Tasks |
|---|---|
| Dense btop-style screen use | 1, 3 |
| Responsive wide, medium, compact, short layouts | 1, 3 |
| Preserved woven-infinity Skein logo | 2 |
| Terminal-safe Unicode and ASCII logo fallback | 2, 4 |
| Truecolor semantic palette with no-color fallback | 2 |
| Meaningful, natural, bounded animation | 2, 4 |
| Truthful map and activity behavior | 1, 4 |
| Width, fallback, and static snapshot verification | 1–4 |
