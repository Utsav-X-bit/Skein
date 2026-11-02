# Skein Mission-Control TUI Design

**Date:** 2026-11-02  
**Status:** Approved design — implementation planning pending  
**Scope:** `skein dashboard` and the authenticated telemetry it consumes

## Summary

Replace the current fixed-width dashboard with a responsive, keyboard-driven,
mission-control TUI. It must be readable and useful at every terminal size,
show only topology the node can substantiate, and use restrained animation to
make live mesh activity, throughput, latency, and health immediately legible.

The dashboard remains a local authenticated API client. It has navigation only:
it does not change network state.

## Goals

- Reflow deliberately for compact, standard, wide, and short terminals; never
  wrap a line merely because a renderer assumed a wider terminal.
- Provide keyboard navigation, peer selection, a peer inspector, panel changes,
  help, and an animation pause without network-mutating controls.
- Visualize direct links and routed paths truthfully, including UDP/TCP,
  next-hop, hop count, and whether activity is local or remotely reported.
- Make measured connection speed, RTT, jitter, recency, reachability, and
  transport unmistakable.
- Animate only observed activity. Idle topology should remain calm.
- Preserve a static, pipe-safe, no-motion output mode and work across Linux and
  Windows terminals with color/Unicode fallbacks.

## Non-goals

- A GUI or mouse-first control surface.
- Peer administration, probes, invite copying, or any other state-changing
  dashboard action.
- Inferring remote peer-to-peer links or traffic that was not reported.
- Reporting packet payloads, overlay IP flows, or other application-traffic
  metadata in telemetry.

## Information architecture and responsive modes

All views are composed from panels with an explicit width and height budget.
Text is shortened at its panel boundary; a terminal never receives a line wider
than its current detected width. Resizing invalidates the current layout and
draws a complete new frame, clearing unused cells.

| Mode | Condition | Composition |
|---|---|---|
| Compact | Narrow terminal | One focusable panel: Overview, Mesh, Peers, Routes, or Events. The selected peer opens in a full-panel inspector. |
| Standard | Enough room for two principal regions | Health header, route-map canvas, selectable peers list, and selected-peer inspector strip. |
| Wide | Enough room for three regions | Persistent peer navigator, central route map, and inspector/activity column. |
| Short | Insufficient vertical budget for the chosen composition | Minimum viable status and focused panel, with an explicit navigation hint; no clipped partial panels. |

The mode selection is based on current dimensions, not a hard minimum terminal
width. The renderer uses ASCII-safe glyphs and no color when terminal support
does not permit richer output.

## Keyboard model

| Key | Action |
|---|---|
| `Tab`, `h`, `l` | Change focused panel or page |
| Arrow keys, `j`, `k` | Move selection within a list |
| `Enter` | Open selected peer inspector |
| `Esc` | Return to the preceding dashboard view |
| `Space` | Pause or resume decorative/activity motion |
| `?` | Open the key reference |
| `q`, `Ctrl-C` | Quit |

Selection is preserved across ordinary updates and resize where the selected
peer still exists. If it disappears, focus returns predictably to the first
available peer or the Overview panel.

## Truthful route map

The map is a rendered local route graph rather than a decorative radial mesh.
It uses the local route table's already-known path information.

- The local node is the map origin.
- A direct neighbour has a solid local edge.
- A destination reached through another node is drawn along its actual known
  path, segmented by hop, and labelled with its next hop and hop count.
- Transport, direct/relayed state, RTT, health, and live traffic decorate an
  edge but never create an edge.
- Where only a next hop is known, the display marks the route as inferred and
  does not manufacture unknown remote links.

The API route response must include the route `path` already retained by
`internal/route.Table`. This supplies an accurate local view of a complete
chosen route without guessing a global physical topology.

## Performance signals

Each selected link and its map label show the following:

| Signal | Definition |
|---|---|
| RX / TX speed | Current directional byte-counter delta, formatted as B/s, kB/s, or MB/s; accompanied by a 30-second trace and peak. |
| RTT | Current authenticated control-ping round-trip time. |
| RTT range | Minimum/maximum measured RTT in the rolling 30-second window. |
| Jitter | Variation between consecutive RTT samples in the rolling window. |
| Recency | Time since a successful peer observation or telemetry report. |
| Route context | UDP, TCP, mixed direction, direct/relayed state, and hop count. |

Latency states are derived from values, not color alone: excellent, good,
elevated, unavailable, and stale. A link with no RTT sample explicitly reads
unavailable; it is never shown as zero latency.

## Mesh-wide activity telemetry

The current local API exposes local peer counters only. To animate an observed
connection between any two mesh nodes, Skein will add small authenticated,
short-lived aggregate activity records to the state gossip protocol and to the
local API.

An activity record contains:

- origin node ID and monotonically increasing sequence;
- source and destination peer IDs;
- directional byte deltas for a bounded sampling interval;
- transport/path classification, RTT, and RTT variation when the origin can
  measure them;
- sampled-at timestamp and a short expiry.

It contains no application payload, packet headers, overlay addresses, ports,
or flow identifiers. Receivers validate bounds, ordering, identity, and expiry;
their activity cache is bounded and evicts stale records. Duplicate or
out-of-order samples cannot revive an expired animation.

The API exposes normalized, freshness-labelled activity for the dashboard. The
TUI distinguishes local counters, traffic transiting the local node, and remote
telemetry in its inspector and activity feed.

## Motion language

The visual personality is dark, precise, and mission-control-like. Motion has
operational meaning:

- New bytes produce packets travelling along the reported route in the measured
  direction; speed and density scale within a bounded range from throughput.
- Link intensity and the compact throughput trace respond to recent traffic.
- Health glyphs use a slow, low-amplitude pulse only while healthy; transitions
  briefly highlight a changed row/link and then settle.
- Idle links do not receive decorative packet traffic.

The dashboard keeps the existing low-overhead 80 ms cadence, updates only its
alternate screen, and bounds the number of animated glyphs per edge. `Space`
pauses nonessential motion. Non-TTY and no-motion mode render the same facts as
a stable snapshot without the animation layer.

## Failure handling

- On API loss, retain the last valid frame, clearly mark its age as stale, and
  show daemon recovery status without inventing live values.
- On terminal resize, draw a fully reflowed frame and erase cells outside the
  new composition.
- On telemetry loss or expiry, leave an edge quiet and mark the associated
  remote report stale rather than extrapolating traffic.
- On unavailable color/Unicode, use clear ASCII states and textual labels.
- Empty meshes give a useful join/invite prompt; sparse and degraded meshes
  remain navigable.

## Component boundaries

| Component | Responsibility |
|---|---|
| Dashboard model | Polling, event/telemetry cache, selection, navigation state, and rolling measurement windows. |
| Layout resolver | Converts terminal dimensions and focus state into compact, standard, wide, or short panel geometry. |
| Panel renderers | Pure functions for overview, navigator, route map, inspector, routes, events, help, and compact views. |
| Route graph builder | Converts local routes plus known peer identities into only the drawable, substantiated graph. |
| Motion scheduler | Turns fresh activity samples into bounded glyph positions; disabled independently from data collection. |
| Activity gossip/API | Encodes, authenticates, bounds, expires, and exposes aggregate telemetry. |

## Verification

- Table-driven renderer snapshots cover compact, standard, wide, short, empty,
  degraded, TCP fallback, direct, relayed, Unicode-disabled, and no-motion
  states.
- Each snapshot asserts visible line width is never greater than the supplied
  terminal width and that stale cells are cleared after a resize.
- Route-graph tests prove direct and relayed paths are correct and verify no
  unreported edges appear.
- Activity tests cover aggregation, bounds, expiry, duplicate/out-of-order
  rejection, source labels, and lack of payload/flow fields.
- Measurement tests cover counter rollover safety, speed formatting, rolling
  RTT range, jitter, unavailable latency, and stale labels.
- Navigation tests cover all keys, focus persistence, peer removal, pause, and
  static/no-motion behavior.

## Acceptance criteria

1. The dashboard is usable without wrapping or clipping after arbitrary terminal
   resizes, including constrained dimensions.
2. Every rendered topology edge is explainable by local routes or explicitly
   labelled remote telemetry; no fake mesh edges are displayed.
3. Selecting any reported connection shows current directional throughput,
   latency, jitter, route/transport context, and data freshness.
4. Fresh activity between any reporting peers visibly traverses its reported
   route; idle and stale edges do not animate as active.
5. All functionality is keyboard navigable, read-only, and works without
   color, Unicode, or motion.
