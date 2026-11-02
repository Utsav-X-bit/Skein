# Skein Btop-Style TUI Redesign

**Date:** 2026-11-02  
**Status:** Approved design — implementation planning pending  
**Scope:** Visual composition, terminal-brand treatment, and motion for `skein dashboard`

## Summary

Rework the mission-control dashboard into a dense, btop-inspired network
console. The screen must use large terminals as an intentional grid rather than
a vertically stacked report. It retains the existing verified route, telemetry,
and keyboard data model; this iteration changes how those facts are composed,
branded, and animated.

## Visual direction

- Dark, precise, information-dense monitoring interface inspired by btop.
- Graphite/near-black terminal background, slate separators, and cool cyan as
  the Skein brand and selection color.
- Blue represents traffic, green a healthy link, amber elevated latency or
  degraded state, and red disconnection. Textual state accompanies every color.
- Use terminal truecolor where color output is enabled, with the existing ANSI
  fallback when it is disabled or unavailable.
- Prefer compact bordered cards and hierarchy over page-wide horizontal rules.

## Brand treatment

`Logo.png` is the source mark: a woven, infinity-shaped mesh. The terminal
does not depend on image protocols to render it. Instead, Skein renders a
small, refined Unicode line-art interpretation beside the **Skein** wordmark.
It preserves the existing infinity silhouette, uses fewer evenly spaced strands
for legibility, and has a static text fallback for terminals without Unicode.

The mark sits in the upper-left header with clear space around it. Node identity
and version remain concise metadata, not part of the wordmark.

## Responsive composition

### Wide mode

Wide mode begins at 130 columns with sufficient height.

```
╭─ Skein [woven mark] ─ node / health / uptime ───────────────╮
│  MESH 4/4     ↓ 2.4 MB/s   ↑ 680 kB/s    RTT 12 ms ±1.4    │
├───────────────────────────────┬──────────────────────────────┤
│                               │  PEERS                       │
│       LIVE ROUTE MAP          │  › laptop   UDP  12 ms       │
│       real links + packets    │    server   TCP  18 ms       │
│                               │                              │
│                               │  SELECTED LINK               │
│                               │  RX/TX trace · path · health │
├───────────────────────────────┴──────────────────────────────┤
│  EVENT / ACTIVITY STREAM                    SPACE pause · ?  │
╰───────────────────────────────────────────────────────────────╯
```

- The header is compact and branded.
- A one-line metric strip reports health, total directional throughput, and
  current latency/jitter.
- The left two-thirds is a truthful route-map canvas.
- The right third holds the peer navigator and selected-link detail.
- The final line is a compact activity/event stream and context-sensitive keys.

### Medium mode

At 90–129 columns, render a compact brand header and metric strip, then give
the map the majority of height. The selectable peer list and selected-link
metrics form a lower detail strip. It does not attempt a squeezed three-column
layout.

### Compact and short modes

Below 90 columns, render a single btop-like focused panel, switched with
keyboard navigation: map, peers, selected link, or activity. Below the vertical
budget, retain a minimal brand/status header and the focused panel; no partial
cards or clipped footer are rendered.

## Motion system

Motion communicates live network state and remains optional.

| Layer | Behavior | Timing |
|---|---|---|
| Brand reveal | The woven mark resolves from outer strands toward the crossover; the `Skein` wordmark settles last. | 600 ms total; 40–70 ms strand stagger; ease-out settle. |
| Traffic | Fresh byte deltas send a small packet glyph along the reported route. Packet duration scales only within a readable 320–600 ms range. | Frame cadence remains 80 ms. |
| State response | New/changed link state briefly highlights its card or row, then returns to its semantic color. | 180–260 ms ease-out. |
| Ambient | A healthy local status indicator breathes gently; idle routes never receive fake packets. | 1.8 s low-amplitude loop. |

At most one-third of visual elements are in active motion. Packets follow known
route segments, including remote authenticated activity paths. `Space` pauses
nonessential motion. Non-TTY and no-motion modes draw only the final static
state; they never depend on an animation completing.

## Renderer boundaries

The renderer becomes a card/grid compositor.

1. A layout resolver returns non-overlapping rectangles from actual terminal
   width and height.
2. Panel renderers draw only within their rectangle: header/brand, metric
   strip, route map, peer navigator, selected-link card, and activity footer.
3. A row compositor joins panel output for wide mode; no panel can cause its
   neighbor to wrap.
4. The graph, telemetry, selection, and motion models remain data sources;
   their route/telemetry truth guarantees are unchanged.

## Verification

- Snapshot wide (160×45), medium (110×36), compact (80×24), and short (40×12)
  frames with empty, direct, relayed, degraded, TCP, and fresh-activity data.
- Assert visible line width never exceeds the supplied terminal width, every
  card rectangle remains in bounds, and wide mode exposes populated map, peer,
  and detail columns.
- Assert the route canvas shows only local route edges or fresh authenticated
  remote activity paths.
- Test Unicode and ASCII brand-mark fallbacks, no-color, no-motion, paused
  motion, and static non-TTY output.
- Capture terminal frames at the beginning, midpoint, and final state of the
  startup reveal; verify the final frame is complete and readable.

## Acceptance criteria

1. A wide terminal is visibly a dense monitoring console, not a stacked report
   with unused whitespace.
2. Medium and compact layouts recompose intentionally and remain legible.
3. Skein has a recognizable terminal-native woven-infinity brand mark and
   wordmark with a static fallback.
4. Animation is meaningful, bounded, pausable, and absent from idle routes.
5. No rendering mode wraps outside its terminal bounds or misrepresents mesh
   connectivity.
