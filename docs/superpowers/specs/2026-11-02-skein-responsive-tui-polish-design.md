# Skein Responsive TUI Polish Design

**Date:** 2026-11-02  
**Status:** Implemented 2026-11-02.  
**Scope:** `skein dashboard` rendering, input state, responsive composition,
and local presentation of authenticated activity data.

## Problem

The first mission-control dashboard establishes the visual direction but has
four operational problems:

- It clears and redraws the entire alternate screen every 80 ms, producing
  visible flicker in some terminals and multiplexers.
- It crops long inspector and activity strings at card borders without an
  ellipsis or a compact alternative.
- `j`/`k` always changes the peer selection. Routes and activity have no
  cursor, and a single peer gives no visible feedback.
- Compact and short terminal modes leave most of the screen unused instead of
  presenting a deliberately reflowed, keyboard-navigable view.

The route map also presents a single direct route as sparse vertical dots and
does not normalize remotely reported activity around the local node. A peer
receiving `A → B` traffic must see `A → ME` when it is B, rather than an
unfamiliar global-ID direction.

## Goals

- Eliminate steady-state whole-screen clears while retaining a correct first
  frame and resize recovery.
- Make every visible panel concise, color-legible, and bounded by its card.
- Give every focusable panel a meaningful and visible keyboard action.
- Make compact and short terminals fully usable rather than a degraded wide
  dashboard.
- Render direction from the observer's local perspective without inventing
  topology or activity.
- Preserve the dark, precise Skein mission-control aesthetic and existing
  non-TTY, no-color, ASCII, and no-motion fallbacks.

## Responsive composition

The layout resolver preserves selection and focus across breakpoints. It does
not render a partially visible lower panel.

| Mode | Bounds | Composition |
|---|---|---|
| Wide | at least 144 columns and 24 rows | Persistent peers navigator, large route map, and inspector/routes/activity rail. |
| Standard | at least 96 columns and 20 rows | Header and health strip, route map, then peers and inspector in an equal lower split. |
| Compact deck | at least 64 columns and 16 rows | One full-height focused card below a one-line Skein status header and compact tab strip. |
| Short deck | smaller than compact bounds | Minimal status line, focused card body, and one-line keyboard hint. Decorative borders are removed before facts or controls. |

The compact/short deck retains the same five pages—Overview, Map, Peers,
Routes, Activity. `Tab`, `h`/`l`, and left/right arrows change pages; the tab
strip always names the selected page. A selected row remains visible when
changing mode. Long values use an ANSI-aware ellipsis and an alternate compact
field layout instead of silently running into a border.

## Navigation model

Focus and selection are distinct. Focus chooses the panel/page; selection is
owned by its panel.

| Focus | `j`/`k`, up/down | Visible result |
|---|---|---|
| Overview, Map, Peers | Cycle peer cursor | Selected peer row, inspector, and map node/link highlight change. |
| Routes | Cycle route cursor | One route row receives the selection marker and its next hop/path is emphasized on the map. |
| Activity | Cycle fresh activity cursor | One local-perspective activity row and its directed pulse are emphasized. |

`Enter` opens the selected peer inspector from peer-oriented panels. `Esc`
returns from the inspector/help to the previous dashboard focus. `Space`
pauses decorative packet motion but does not stop telemetry updates. The
footer states both the focused panel and a useful position, such as `peer 1/3`
or `route 1/2`; this makes a one-item list intentionally still appear
selected.

## Visual system

The existing dark background and Skein teal wordmark remain. Rendering uses
semantic palette slots rather than widget-specific color decisions:

| Slot | Meaning |
|---|---|
| Brand/focus | Skein teal; also a shape/selection marker in no-color mode. |
| Label/muted | Slate text for field names and secondary metadata. |
| Healthy | Green/teal state, direct UDP link, and good RTT. |
| Traffic out/in | Cyan for local outbound traffic and blue-violet for inbound traffic. |
| Elevated/stale | Amber status plus an explicit text label. |
| Error/down | Red status plus an explicit text label. |

Rows use a high-contrast marker and compact field grouping: state, transport,
and endpoint appear on a first line; speed and latency appear on a second.
Values never depend on color alone. In constrained cards the endpoint yields
to link state and latency; the full endpoint remains available in the focused
inspector.

## Route-map behavior

The route map remains a truthful local graph. Route-table paths supply stable
connection lines; fresh aggregate activity supplies directional pulses.

- A single direct peer renders as a horizontal link lane across the map, with
  `ME` and peer endpoint anchors, link health, transport, RTT, and hop count.
- Multi-peer layouts retain the radial placement but reserve label room and
  highlight the selected peer's route.
- Each fresh activity path is localized before rendering: this node's ID maps
  to `ME`; known IDs map to peer names or short IDs. Therefore activity is
  `ME → yoga` for a local sender and `yoga → ME` for the receiving peer.
- Outbound and inbound pulses travel in their actual localized direction and
  use their respective traffic colors. Bidirectional traffic may show both,
  bounded to a small number of pulses.
- Links remain quiet when there is no fresh telemetry. No remote or inferred
  edge is fabricated.

## Rendering lifecycle

The renderer continues to build an entire immutable frame from a dashboard
snapshot. A presentation layer then compares it with the prior frame.

1. On the first frame or any terminal-size change, erase the alternate screen
   once and write the complete frame.
2. On normal ticks, emit cursor-positioned writes only for changed lines and
   erase the suffix of a changed line. Do not use an unconditional screen-clear
   sequence.
3. If the new frame has fewer rows, explicitly clear former rows. Keep cursor
   movement and writes in one buffered output operation.
4. Retain the existing alternate-screen, raw-mode, cursor restoration, static
   non-TTY, and motion-paused behavior.

This removes repeated whole-screen painting without changing polling,
telemetry, or animation semantics.

## Boundaries

| Unit | Responsibility |
|---|---|
| `dashboardState` | Panel focus, peer/route/activity cursors, inspector/help state, and keyboard transitions. |
| Layout resolver | Returns mode-specific card geometry and tab/header/footer regions from current dimensions. |
| Format helpers | ANSI-aware visible-width truncation, ellipsis, compact fields, and semantic color formatting. |
| Topology view-model | Localizes node IDs and activity paths, derives truthful edges, direction, selection, and link labels. |
| Frame presenter | Diffs complete rendered lines against the last terminal frame and emits minimal cursor writes. |

No render function fetches data or changes dashboard state. The presenter has
no knowledge of API data; it only receives final lines and dimensions.

## Verification

- Table-driven layout tests cover 40×12, 64×16, 80×24, 120×36, 160×45, and
  resizing from wider/taller to narrower/shorter. Every visible line fits the
  supplied width and focused controls remain visible.
- Formatting tests verify color-aware ellipsis, card-safe field grouping, and
  useful ASCII/no-color output.
- Input-transition tests verify Tab/arrow focus changes and panel-local
  `j`/`k` behavior for zero, one, and multiple peers/routes/activity samples.
- Topology tests verify a direct one-peer lane, no fabricated edges, local
  sender `ME → peer`, receiving peer `peer → ME`, and directionally correct
  pulse coordinates.
- Presenter tests prove that identical frames emit no bytes, an ordinary
  animation changes only affected rows, resize triggers one full clear, and
  removed rows are cleared.
- Run the full Go test suite and manually exercise a real narrow terminal,
  rapid resize, arrows, `j`/`k`, no-motion, no-color, and exit restoration.

## Acceptance criteria

1. Steady dashboard motion does not use a whole-screen clear or visibly flicker
   in supported terminals.
2. No card lets a label or value overwrite a border; constrained content is
   compacted or visibly ellipsized.
3. Each focused panel has a visible, panel-specific response to `j`/`k` and
   up/down.
4. Compact and short dashboards dedicate their usable area to the active page
   with discoverable tabs and controls.
5. The route map shows a meaningful direct-link lane and reverses activity
   direction correctly for the receiving peer's local perspective.
