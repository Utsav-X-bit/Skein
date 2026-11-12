# Skein for Android — Mobile GUI Design Brief (Phase 1)

**Parent plan:** `docs/superpowers/plans/2026-11-13-android-mobile.md` (§4)
**Branch:** `android` (worktree `~/workspace/skein-android`)
**Date:** 2026-11-13
**Status:** Design — for the owner's agreement before Phase 3 builds it. No implementation code was written for this brief.

**Sources read before designing:** the Android plan (§4 especially), the desktop GUI redesign prompt (`~/workspace/skein-work/gui-redesign-prompt.md`), the current frontend (`gui/frontend/src/` — `App.tsx`, `AppShell`, `Sidebar`, `Overview`, `Peers`, `Activity`, `Invites`, `Settings`, `Onboarding`, `tokens.css`, `features/mesh/*`, `features/activity/*`, `gui/services/`), and the skills `local-brainstorming`, `local-frontend-design`, `local-impeccable`, `local-ui-ux-design` (via `ui_ux_design`), `local-diagram-animation`, `local-micro-interaction`, `local-60fps-animation`, `local-accessible-animation`.

**Mode (impeccable): Operate.** The visitor completes a task — connect, check a peer, invite a device. Scanability, consistency, and native expectations outrank expression. Brand lives in precise details (the mesh map, the accent, the plain-language status), not in decoration.

**One deliberate rejection:** the `ui_ux_design` design-database helper, run for "mesh VPN mobile app dark precise", recommended an App Store landing pattern and Orbitron/JetBrains Mono cyberpunk typography. Both were rejected: the pattern is marketing, not an operating tool, and display typography competes with the data. The brief wins — the incumbent Skein tokens and the desktop redesign language govern (see §1).

---

## 0. Design decisions taken here (the short version)

| # | Decision | Why |
|---|---|---|
| D1 | **Home is the compact mesh map, not a dashboard of cards.** Status line + map + Connect button fill the first viewport. | The map is the product's centrepiece on desktop; on a phone it is the fastest answer to "am I connected, to whom, how well?" |
| D2 | **Bottom nav with exactly 5 tabs: Home / Peers / Activity / Invites / Settings.** | Thumb-reachable, one tap to everything, no hamburger. See §2 for what each tab absorbs from the desktop sidebar. |
| D3 | **Peer detail is a bottom sheet, never a side panel.** | The desktop `PeerInspector`/`LinkInspector` side panel has no room on a portrait phone. A sheet keeps the map visible underneath. |
| D4 | **Connect is a single sticky primary button on Home, above the bottom nav.** Pause/Resume is the same button in a different state — never two competing buttons. | One thumb, one action, one state. The VPN permission explainer lives behind this button on first use. |
| D5 | **The map animates less than desktop, on purpose.** One travelling dot per active link, marching dashes only on the selected link. | A phone screen at 5 nodes with flow on every link is noise, and a WebView on a mid-range phone cannot afford it. |
| D6 | **Every state is a full screen with one button, never a banner plus a paragraph.** | Desktop already uses shell banners; on mobile a banner + a cramped page is a dead end with extra steps. |
| D7 | **Dark theme only in v1.** Light/high-contrast tokens exist in `tokens.css` and are not removed — they are simply not exposed in mobile Settings until v1.1. | Halves the visual QA surface for v1; the tokens make it a switch, not a redesign, later. |
| D8 | **No new data sources.** Every screen reads the same `gui/services` bindings and the same `useMesh`/`useDaemonEvents` event stream as desktop. | If a screen needs data the API does not have, that is a Phase 2 backend gap (§8), not a reason to invent a second source. |

---

## 1. Design language

### 1.1 Principles

- **Dark, calm, precise.** The data is the decoration. No gradients-as-wallpaper, no glow, no background animation behind the map. The one permitted ambient element is the map itself.
- **One accent, three meanings.** Accent = direct/healthy, amber (`--relay`) = relayed, red (`--danger`) = broken/down. Colour is never the only signal: every coloured state also has a word ("direct", "relayed", "offline") and, where it matters, an icon.
- **Plain language first, numbers second.** The Home status line reads like a sentence — "Connected to My-Net — 2 peers, 15 ms" — and the numbers (RTT, up/down, overlay IP) sit one level below it.
- **Every feature ≤ 2 taps from Home.** Tab (1) + row/action (2). Anything deeper is a Settings section, not a hidden screen.
- **No dead ends.** Every state — including every error — names what happened, why, and offers exactly one next action as a button. Never a raw API endpoint, never a stack trace, never a paragraph with no button.

### 1.2 Colour tokens (OKLCH)

The incumbent tokens in `gui/frontend/src/styles/tokens.css` are kept verbatim as the source of truth; the OKLCH values below are their equivalents for any new mobile-only token work (Tailwind v4 `color-mix` in OKLCH already powers `--accent-soft`). New mobile tokens must be added to `tokens.css`, never hard-coded in a component.

| Token | Hex (incumbent) | OKLCH | Role |
|---|---|---|---|
| `--bg` | `#08090b` | `oklch(0.145 0.004 250)` | App background |
| `--surface` | `#101216` | `oklch(0.19 0.006 250)` | Cards, sheets, nav bar |
| `--surface-raised` | `#161b22` | `oklch(0.225 0.008 250)` | Pressed / elevated surfaces |
| `--border` | `#5a6a80` | `oklch(0.45 0.03 250)` | Decorative borders only |
| `--border-strong` | `#66788f` | `oklch(0.51 0.03 250)` | Interactive borders (≥3:1) |
| `--text` | `#f4f6f8` | `oklch(0.96 0.003 250)` | Primary text |
| `--text-muted` | `#a7b0be` | `oklch(0.72 0.012 250)` | Secondary text |
| `--text-faint` | `#8a94a3` | `oklch(0.62 0.01 250)` | Metadata only, never body copy |
| `--accent` | `#4fe3c1` | `oklch(0.85 0.13 175)` | Direct / healthy / primary action |
| `--accent-soft` | accent 12% | `color-mix(in oklch, #4fe3c1 12%, transparent)` | Selected-row / active-tab wash |
| `--relay` | `#f2b35d` | `oklch(0.79 0.11 75)` | Relayed path, warning-adjacent |
| `--danger` | `#ff7a7a` | `oklch(0.72 0.13 20)` | Down / error / destructive |
| `--info` | `#8fb4ff` | `oklch(0.76 0.08 255)` | Observed link (dashed), neutral info |
| `--success` | `#7be3a0` | `oklch(0.85 0.12 155)` | Success confirmations |
| `--warning` | `#f2c94c` | `oklch(0.84 0.12 95)` | Paused / degraded / caution |

**Contrast:** all text pairs are AA-checked in `scripts/contrast.test.ts` on desktop; the same check must pass for every mobile-only pairing (tab labels, sheet text, toast text). `--text-faint` is metadata only — never use it for a label, a button, or a status word. Interactive borders use `--border-strong` (≥3:1); `--border` is decorative only.

**New mobile-only tokens** (to be added in Phase 3, all in `tokens.css`):

| Token | Value | Role |
|---|---|---|
| `--nav-h` | `64px` + `env(safe-area-inset-bottom)` | Bottom nav height incl. gesture bar |
| `--sheet-radius` | `20px` (`--radius-xl`) | Bottom-sheet top corners |
| `--tap-min` | `48px` | Minimum touch target (see §1.5) |
| `--connect-h` | `56px` | Home Connect button height |

### 1.3 Typography

- **UI text:** system sans in the WebView (`system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`) — on Android this resolves to Roboto. No webfont download: the app must render instantly offline, and a display face adds nothing to an Operate surface.
- **Data text (IPs, RTT, bytes, IDs, invite strings):** `ui-monospace, "JetBrains Mono", "Roboto Mono", monospace`, tabular numerals. RTT and throughput always use tabular figures so live-updating numbers do not jitter horizontally.
- **Scale (mobile):** display/status `20px/600` (Home status line), screen title `20px/600`, section label `11px/600` uppercase with `0.08em` tracking (the desktop `.mono-label`), body `15px/400`, secondary `13px`, metadata `12px` mono, tab label `11px/600`. Minimum body size is `12px` and only for metadata — never for an action label or a status word.
- **Line length:** body copy wraps at full width but invite strings, overlay IPs, and error text use `break-all` inside a copy field — never truncated mid-string without a copy action.

### 1.4 Spacing and layout grid

- **Base unit 4px;** the desktop `--space-*` scale (4–72) is reused unchanged.
- **Screen padding:** `16px` horizontal, `16px` top, `16px` above the sticky action area. Cards use `16px` internal padding and `--radius-lg` (14px); sheets use `--radius-xl` (20px) top corners only.
- **Vertical rhythm:** `16px` between sections, `12px` between a section label and its content, `8px` between list rows' inner elements.
- **Max content width:** none on phones; on tablets / landscape ≥700dp the content column caps at `560px` centred for text-heavy screens (Settings, Invites detail) while the map stays full-bleed.
- **Cards, not tables, on mobile.** The desktop Peers table (7 sortable columns) becomes a card list (§4). Any desktop table that survives must be horizontally scrollable with a visible fade cue — never a clipped table the user cannot tell is scrollable.

### 1.5 Touch, safe areas, and reach

- **Touch targets ≥ 48×48dp, no exceptions.** Icon-only buttons get `48px` hit areas even when the glyph is `20px`. Tab bar items are `64px` tall with the label included. List rows are `64px` minimum (`72px` when they carry two lines of metadata). The Connect button is `56px` tall and full-width.
- **Safe areas:** the app bar respects `env(safe-area-inset-top)`; the bottom nav adds `env(safe-area-inset-bottom)` as padding below the tab row; sheets and toasts sit above `env(safe-area-inset-bottom)`. Nothing interactive is placed in the gesture-bar zone. Content never renders under the camera cutout — the WebView viewport is inset, not overlaid.
- **Thumb reach:** the primary action on every screen lives in the bottom third — Home's Connect button is sticky above the bottom nav; Peers' "Invite device" is a floating action reachable by thumb (see §4); destructive actions (Leave, Reset, Rotate) are deliberately placed mid-screen inside their Settings section so they are never hit by accident.
- **Press feedback:** every tappable element scales to `0.97` on press (`whileTap` / `:active`, transform only, 120ms) and shows its state change immediately — the button morphs to its working state (spinner → checkmark) in place rather than unmounting.
- **Text scaling:** layouts must survive Android's largest font scale. Fixed-height rows use `min-height`, not `height`; two-line labels wrap rather than truncate; the bottom nav labels shrink to `10px` at the largest scale rather than clipping.

### 1.6 Reduced motion

Tiered, not a kill switch (per `local-accessible-animation`):

| Tier | Motion | Reduced-motion behaviour |
|---|---|---|
| 1 — remove | Map flow dots, marching dashes, sheet slide-up, large translate entrances, staggered node reveal | Removed entirely. Map renders in its final composed state; sheets appear with a ≤150ms opacity fade. |
| 2 — soften | Toast slide, tab indicator move, card press scale, count-up numbers | Replaced by a ≤150ms opacity cross-fade (or instant value change for numbers). |
| 3 — keep | Opacity fades, focus-ring transitions, status-dot colour change, spinner (essential feedback) | Kept, shortened to ≤200ms where long. |

Implementation: the existing `data-motion` attribute + `useTheme`/`useUI` motion setting in `lib/store.ts` already drives the `--dur-*` tokens to `0.01ms` under `prefers-reduced-motion: reduce` (see `tokens.css`). Mobile reuses that mechanism unchanged and adds one `useReducedMotion()` hook (matchMedia + live `change` listener) for JS-driven motion — the SVG flow dots and the sheet drag gesture must read it, because CSS alone cannot stop them. Never use `0s` durations (breaks `transitionend` listeners); `0.01ms` throughout. Any auto-playing motion lasting >5s (the map flow) must also have a pause control — the map's Map/List toggle is that control, and a "Pause animation" switch lives in Settings → Motion.

---

## 2. Information architecture — bottom nav

### 2.1 The five tabs

| Tab | Icon (lucide) | Route | What lives here |
|---|---|---|---|
| **Home** | `LayoutDashboard` | `/` (Overview, mobile) | Status sentence, compact mesh map, throughput strip, "what just happened" strip, Connect/Disconnect button. The single most-checked screen. |
| **Peers** | `Users` | `/peers` | Live peer list (cards), presence, direct/relay, RTT, live up/down. Tap → peer detail sheet. "This device" card pinned at top. |
| **Activity** | `Activity` | `/activity` | Human-readable feed + live throughput chart (last 60 samples). Filter chips. |
| **Invites** | `Radio` | `/invites` | Create peer / network invite, copy, share via Android share sheet, QR, expiry, list of active invites. |
| **Settings** | `Settings` | `/settings` | Node, Service (VpnService start/stop), Switch network wizard, Security, Diagnostics, Motion/Theme, About. |

**Justification for five, and for what each tab absorbed:**

- **Five is the thumb-reachable maximum.** Four would force a "More" tab (a dead end in disguise); six would shrink targets below 48dp on a 360dp-wide phone. Five named tabs, no overflow menu.
- **Home, Peers, Activity, Invites are the four things the owner checks or does daily.** Settings is the fifth because it is visited rarely but must be one tap away when something is wrong (service down, switch network).
- **Network (desktop `/network`) dissolves into Home.** Its route table is the map's "Routes" view (a sheet from Home, §3.4) and its config read-only block moves to Settings → Node → "Configuration". A standalone Network tab would duplicate the map.
- **Security (desktop `/security`) becomes a Settings section.** Roles, access, rotate secret, revoke user are used rarely and are all destructive-adjacent; grouping them under Settings → Security keeps them one tap from Home and next to the confirm dialogs they need.
- **Diagnostics/Logs (desktop `/logs`) becomes Settings → Diagnostics.** Logs (live tail + filter), firewall status ("managed by Android VPN service"), diagnostics bundle export. The owner opens logs when something is wrong — that is a Settings errand, not a daily tab.
- **About (desktop `/about`) becomes the footer of Settings.** Version (live from `AppService.Version`), update check. No tab of its own.
- **Command palette (⌘K) is dropped on mobile.** There is no keyboard. Its jobs are redistributed: search lives on Peers and Activity (a search field, not a palette); the actions (join, invite, ping, switch network) live as buttons on their own screens. If a future tablet/keyboard build wants it back, it returns as a search-first sheet — not as a hidden shortcut.

### 2.2 What the tab bar shows

- Five items: icon (20px) + label (11px). Active tab: accent icon + accent label + `accent-soft` pill background; inactive: `--text-muted`.
- **Badges:** Peers shows a count badge only when a peer is offline or a new peer joined since last view (cleared on opening Peers); Activity shows a dot when an unread error event exists (cleared on opening Activity). Invites never badges. Badges are dots/counts, never red text alone.
- **Labels are always visible** (no icon-only mode): the audience is one user who opens this app weekly at most, and icon-only nav is a memory test.
- **The tab bar is hidden** during Onboarding and during the VPN-permission explainer sheet, and it never overlays a full-screen state screen (§5). It stays visible on every other screen, including sheets (sheets are modal above it, with their own scrim).

### 2.3 Top app bar (replaces the desktop CommandBar)

- One row, `56px` + safe-area inset: left = Skein wordmark (small, 32px mark) + current network name; right = connection status dot + a notifications bell (opens the notifications list as a sheet; badge = unread count).
- No search icon in the app bar (search lives on Peers/Activity where it applies). No hamburger, no overflow menu — if an action does not fit its screen, the screen is wrong.
- The app bar is **not** sticky on scroll for Peers/Activity (content is the point; the tab bar is the anchor). It is sticky on Home only until the map scrolls, then it collapses to the network name + status dot.

---

## 3. Home — the mesh map for a phone

### 3.1 Screen composition (top to bottom, portrait)

1. **Status sentence** (one line, 20px/600): "Connected to My-Net — 2 peers, 15 ms". Below it, one muted line: overlay IP · uptime · "1 of 5 devices" capacity. When paused/degraded the sentence changes, never just the colour — e.g. "Paused — mesh is idle", "Relayed — direct path blocked by NAT".
2. **Compact mesh map** (~52vh, min 320px, max 480px): this node centred, peers arranged on an ellipse around it (reuse the desktop `MeshCanvas.positionsFor` ellipse layout — a circle would push peer cards past the short canvas). Links: solid accent = direct, solid amber = relayed, dashed info = observed, dotted faint = advertised route. Each link carries its RTT label at the midpoint; each peer node shows name, presence dot, direct/relay badge, RTT.
3. **Connect button** (sticky, full-width, 56px, above the bottom nav): "Connect" / "Disconnect" / "Resume" / "Reconnect" depending on state (§5). This is the only primary button on Home.
4. **Throughput strip** (`ThroughputStrip`, reused): live up/down, last-60-sample sparkline, per-peer mini-bars collapsed to total + top talker.
5. **"What just happened" strip** (`EventStrip`, reused): the latest 3 events as human sentences ("yennefer joined via relay", "Route to yennefer learned"), each tappable to open Activity at that event.
6. **Routes entry point:** a quiet text button under the map — "Routes (4) ›" — opens the Routes sheet (§3.4). The route table is visible from the map, not buried in a separate page.

Landscape (width ≥700dp): two columns — map left (60%), status + throughput + events right. The Connect button stays full-width at the bottom.

### 3.2 How the compact topology works

- **Geometry:** this node is a fixed, larger card at the centre. Peers sit on an ellipse (never a circle, never a grid — an ellipse fits a portrait viewport and keeps every peer equidistant in reading order). With 1 peer: peer at 12 o'clock. With 2–4 peers: spread on the ellipse, sorted by RTT ascending clockwise from the top so the fastest peer is always found first. At the 5-node limit the ellipse still fits — four peers is the maximum, and the layout is computed for exactly that.
- **Links:** one link per peer pair that can reach each other, drawn as straight lines (no curves on mobile — curves cost layout precision for zero information at this scale). The link to *this* node is always drawn; peer-to-peer links are drawn only when the route table says the path exists, and are thinner (1px vs 2px) so the eye reads "my links" first.
- **Labels on the map, not in a legend below it:** RTT sits on the link midpoint (`15 ms`); the direct/relay badge sits on the peer node. The legend (direct / observed / advertised / route) is a single horizontally scrollable row of 4 swatches *above* the map, in 11px type — the desktop legend is reused, not redesigned.
- **Tap targets:** peer nodes are ≥48×48dp hit areas (the node card is ~96×64dp; the hit area is the whole card). Links are tappable via a 24dp-wide invisible hit strip along the line. Tapping a peer opens the peer detail sheet (§3.3); tapping a link opens the link detail sheet (RTT sparkline, throughput, direct/relay, uptime — the desktop `LinkInspector` content, re-laid-out vertically). Tapping empty map space dismisses the sheet. Double-tap zooms nothing — there is no zoom; pinch is disabled on the map (the map always fits; zoom is a desktop need).
- **Live updates:** driven by daemon events (`useDaemonEvents`), not polling. A peer joining animates in (node scale 0.8→1 + edge draw-on, 300/500ms per `tokens.css`); a peer leaving fades out and the link draws off. No full-map re-render per event — nodes and edges update independently.
- **Stale data:** if no mesh update arrives for >15s, the map dims to 60% opacity and a one-line banner appears *inside* the map card: "Last update 32 s ago — showing last known state." with a Retry button. Same rule as desktop Overview, same wording.

### 3.3 Tap-to-expand: the peer detail sheet

- **Bottom sheet, three detents:** peek (40% — identity, presence, RTT, direct/relay, live up/down), half (70% — + endpoints, last handshake, bytes sent/received, routes via this peer), full (92% — + "Why can't I reach X?" troubleshooter entry, ping/probe actions, route list). Drag handle on top; drag down dismisses; scrim tap dismisses. Sheet transitions are transform-only (translateY), 220ms ease-out in, 180ms ease-in out.
- **Contents (from desktop `PeerInspector`, re-laid-out vertically):** name + overlay IP + presence + direct/relay badge + RTT + live up/down with direction arrows (↓/↑, never a static number) + endpoints/candidates + last handshake + bytes + routes via this peer + actions: Ping, Probe, View routes. **No "remove peer" anywhere** — the product rule (equal peers, tombstone leave) is enforced by absence, not by a disabled button. Self-leave lives only in Settings → Switch network, with its consequence explained.
- **Shared-element continuity:** the tapped node card's name/RTT morph into the sheet header via a shared `layoutId` (Framer Motion) — the eye tracks the same object from map to sheet. If the motion library is unavailable in the WebView build, the fallback is a plain fade; the sheet must work identically either way.

### 3.4 Routes sheet

- Triggered by "Routes (n) ›" under the map. A bottom sheet listing every live route as a row: destination → via next hop → RTT → direct/relay. Tapping a row opens that peer's detail sheet. This is the desktop `RouteDrawer` re-skinned as a bottom sheet; same data (`model.paths`), same `nameOf` mapping ("this device" for self).

### 3.5 Animated data flow — reduced vs desktop

| Aspect | Desktop | Mobile v1 |
|---|---|---|
| Flow on every link | Yes, dots on all links | **No.** One travelling dot per *active* link only (a link with traffic in the last 2 s sampling window), max 3 dots on screen at once. Idle links are static. |
| Marching dashes | All links | Selected link only (the tapped peer/link). |
| Dot speed | Reflects real throughput | Same, but clamped: dot traverses the link in 0.8–2.0 s regardless of rate, so a fast link does not strobe. Direction = real send/receive direction; when both directions are live, dots alternate. |
| Node reveal | Staggered 30ms per node | Same stagger on first load only; on live updates only the changed node animates. |
| Throughput chart | Full strip, 60 samples | Same data, same component; chart draw-on only on first mount. |

**Performance budget:** the map animates `transform` (dot `offset-distance` / translate) and `opacity` only; `stroke-dashoffset` is allowed for the single selected link (it is compositor-cheap at this scale per the desktop token comment, and it is one element). No `width`/`height`/`top`/`left` animation. `will-change: transform` is applied to travelling dots just before they animate and removed when the link goes idle. The map must hold 60fps on a mid-range Android phone with 4 peers — verified in Phase 4 on a real device, not assumed.

**Pause control:** a small pause/play icon button at the map's top-right freezes all flow animation (state persists per session). Settings → Motion → "Animate mesh flow" is the durable switch. Reduced-motion (§1.6) overrides both.

### 3.6 List fallback (accessibility)

- A segmented Map / List control sits at the top-right of the map card (reuse the desktop `MeshCanvas` mode toggle). **List mode is the desktop `MeshTable` as a vertical card list** — same rows, same data, no topology. It is the screen-reader path: every node and link in the map has an equivalent row in the list, with identical labels ("yennefer, direct, 15 ms, active, ↓ 12 KB/s ↑ 3 KB/s").
- TalkBack order in map mode: status sentence → map (announced as "Mesh map, 2 peers. yennefer, direct, 15 ms. …" — a single summary, not a node-by-node traversal) → "Switch to list view" button → Connect button. Node-by-node exploration happens in List mode.
- The toggle state persists across sessions (`useUI` store, as the desktop mode does).

---

## 4. Each screen

### 4.1 Peers

**Layout:** app bar → "This device" card (pinned, distinct border, "This device" badge, own overlay IP + RTT n/a + uptime) → search field (sticky under app bar, 48px, `Search peers…`) → peer cards, one per peer, sorted by presence (active → idle → routed → offline → unknown) then RTT. No table, no column sorting — sorting controls do not survive the jump to a 360dp screen; presence order is the sort.

**Peer card (one row, 72px min):** presence dot + name (15px) + overlay IP (12px mono, muted) on the left; right side: direct/relay badge + RTT (`15 ms`, tabular) + live rate (`↓ 12 KB/s ↑ 3 KB/s`, 12px). Second line under the name: route summary ("via yennefer" / "direct") + last seen ("12 s ago"). Tap anywhere → peer detail sheet (§3.3). Long-press → context actions (Ping, Probe, View routes) as a small menu — the sheet is the primary path; long-press is the shortcut.

**Primary action:** a floating action button (FAB), 56px, bottom-right above the nav: "+ Invite device" → Invites tab. The FAB is the only FAB in the app.

**Empty states:**
- No peers: illustration-free empty state (logo mark, 48px, muted) — "No peers yet" / "Invite a device to join this mesh. This daemon can be healthy with no peers." + [Invite a device] button inline. Same copy as desktop `EmptyState`.
- Search with no match: "No matching peers" / "Clear the search to see every peer." + [Clear search] button.
- Peer offline: the card stays, dimmed to 60%, presence "offline", last seen shown, actions disabled except Probe — an offline peer is information, not an error.

**What is not here:** no remove-peer action (product rule), no per-peer invite revocation (F-01, deferred), no bulk actions.

### 4.2 Activity

**Layout:** filter chips (horizontal scroll row, 48px tall, chips ≥48px wide: All / Peers / Routes / Invites / Security / Errors) → throughput chart card (live, last 60 samples, up/down, per-peer toggle collapsed to a "Total / per-peer" segmented control) → feed (reverse-chronological, grouped by day, each entry: icon + human sentence + relative timestamp + link to the peer/route where relevant).

**Feed entries** (from desktop `Timeline` + `bucketActivities`): peer joined/left, handshake, route learned/withdrawn, invite created/used, secret rotated, service restarted, firewall repaired, errors. Same human sentences as desktop — the feed is shared code, not a rewrite.

**Empty state:** "Nothing yet" / "Activity appears here as the mesh runs — joins, routes, invites, and errors." No button (there is nothing to do; an empty Activity feed is normal on a fresh mesh).

**Stored history:** same 60-minute window as desktop (`HistoryWindowMinutes`), same 10 s bucketing. Pull-to-refresh re-fetches; the live sampler keeps the chart continuous across tab changes (it already runs from `AppShell`).

### 4.3 Invites

**Layout:** two tabs at top (segmented control, 48px): **Peer introduction** | **Network join string**. Each tab: one explanatory paragraph (the desktop copy, verbatim — it states the security consequence precisely), a large [Generate …] button, then the generated invite as a `CopyField` (mono, break-all) + [Copy] + [Share] + QR code (desktop `QRCodeView`, 192px, centred).

**Share:** [Share] opens the Android share sheet (see §8 — needs a native bridge or a verified `navigator.share` in the WebView; clipboard copy is the guaranteed fallback and is always present). Sharing a network join string shows the same warning as the desktop copy — the sheet is a transport, not a trust decision.

**Active invites list:** below the generator, a list of active invites with expiry ("expires in 9 min", "expired") and, for network invites, who used them. Regenerate replaces the current invite in memory only (same rule as desktop: nothing is written to history, notifications, logs, or the URL; switching tabs drops the plaintext).

**Empty state:** before first generation, the invite area shows "No invite generated yet" + the Generate button. There is no separate empty screen — the generator *is* the empty state.

**What is not here:** no per-peer invite revocation (F-01), no invite history beyond the active list, no QR *scanning* to join (v1 is generate-and-share only; joining by scanning a QR is out of scope, §7).

### 4.4 Settings

Grouped sections, single-column, `16px` gaps. Every section is a `Card`; destructive sections sit at the bottom, after a divider, with red section labels.

| Section | Contents (mobile) | Notes |
|---|---|---|
| **Node** | Name, listen port, MTU, mDNS toggle (same validated form as desktop `Settings`, same 576–65535 MTU / 1–65535 port rules). Read-only block below: overlay IPv4/IPv6, interface (`skein0` via VpnService), config path (app private files dir), STUN. | Save is a sticky [Save] button at the section bottom; invalid input shows the field error inline, never a toast alone. |
| **Service** | Real state first ("Running · started 2 h ago"), then Start / Stop / Restart buttons. **On Android these map to the VpnService start/stop** — there is no system-service install/uninstall; the Install/Repair/Uninstall desktop actions do not appear. After every action the real state is re-queried and shown (desktop bug §2.2.5 rule). | If the daemon is in-process and fails to start, the state screen (§5, "daemon down") is shown instead of a stale Settings page. |
| **Switch network** | The first-class wizard: Leave current network (tombstone explained) → Reset/purge (backup path shown, purge option) → Create a new network **or** Join with a fresh invite. Guided steps with live state at each step; rejoin-the-same-network is a separate, clearly labelled path. Reuses desktop `NetworkRecovery` logic. | This is the acceptance flow from the desktop redesign prompt. Never a raw API endpoint. |
| **Security** | Roles + who has access (`AccessPanel`), rotate secret (confirm dialog: what happens, what is kept, how to undo), revoke user. | Rotate secret is destructive-adjacent: confirm dialog states the consequence and requires typing nothing — one [Rotate secret] confirm tap after the dialog. Never rotate without the dialog. |
| **Diagnostics** | Logs (live tail + filter, virtualised list), firewall status — "Firewall: managed by Android VPN service" (no ufw/netsh rules to show), diagnostics bundle export (via Android share sheet; see §8 if the bridge is not ready). | Logs use the same filter as desktop; the list is virtualised so a long tail does not jank scroll. |
| **Appearance & motion** | Theme (Dark only in v1 — shown as "Dark" with "Light coming in v1.1" as disabled text, not a hidden option), Motion: System / Full / Reduced, "Animate mesh flow" switch. | The motion setting writes the same `data-motion` attribute desktop uses. |
| **About** | Version (live, `AppService.Version`), update check (v1: check only — see §7), licences link, "Skein for Android" + branch note. | No update-apply button in v1. |

**Confirm dialogs (destructive actions):** Leave, Reset/purge, Rotate secret, Revoke user each get a dialog stating what happens, what is kept (backup path where relevant), and how to undo. Buttons are full-width, stacked, 48px, with the destructive action last and in `--danger`. Dialogs are bottom sheets on mobile (Radix `Dialog` with the mobile sheet variant, §6), not centred desktop modals.

### 4.5 Onboarding (first run)

- **Full-screen, no tab bar, no app bar.** Same design language, stacked vertically: logo mark (56px) + "Skein" + one-line purpose ("A private mesh VPN for your devices").
- **Choice first:** two large cards — [Create a network] / [Join with an invite] — plus a quiet third row: [Import a configuration]. Same `welcome → create | join | import → done` modes as desktop `Onboarding`.
- **Forms:** one field per screen where possible (network name → port → MTU, each with its default shown: port `11010`, MTU `1280`). The Android privilege note from desktop ("the helper asks for administrator approval…") is replaced by the VPN-permission note: "Skein runs its VPN inside this app. Android will ask you to allow it when you first connect — nothing leaves the phone until you say yes." No admin/elevation copy on Android — there is no helper and no root.
- **Progress:** a 3-dot step indicator at top ("Choice → Details → Done"), back is a text button, never a hidden gesture.
- **Done state:** "You're in" + network name + [Connect now] (primary, triggers the VPN permission flow, §5) + [Explore first] (secondary, goes to Home disconnected). After Done, onboarding never shows again unless the service is uninstalled/reset.

---

## 5. States and the VPN permission flow

Five states, five different screens, five different buttons. Membership (departed), service lifecycle (daemon down), connectivity (paused), and configuration (no network) are independent facts — never inferred from one another, per the desktop `AppShell` rule.

### 5.1 State screens (full-screen, no tab bar, one primary button each)

| State | What the user sees | Primary button | Secondary |
|---|---|---|---|
| **Daemon down** | "Skein isn't running." / "The VPN engine inside the app stopped or failed to start. Your mesh settings are safe." | [Start Skein] — restarts the in-process daemon and re-queries real state | [Diagnostics] → Settings → Diagnostics |
| **No network** | "No network yet." / "Create a network or join one with an invite." | [Create or join a network] → Onboarding choice | — |
| **Paused** | "Paused." / "The mesh is idle. Peers are still configured; nothing is routed." | [Resume] — resumes the connection (`ConnectionService.Connect`) | [Disconnect fully] (secondary, leaves the paused state for a clean disconnect) |
| **Departed** | "You left My-Net." / "This device is no longer a member. Peers and routes below are the last known state." | [Rejoin this network] — fresh invite + confirm | [Switch to a different network] → Switch-network wizard |
| **VPN permission denied** | "Skein can't start the VPN." / "Android VPN permission was denied. Skein cannot route mesh traffic without it — nothing is connected." | [Open VPN settings] — opens the system VPN settings screen so the user can re-enable it | [Try again] — re-runs the permission flow (the system dialog appears again only if Android allows it; if Android has permanently denied it, the button explains that and points back to settings) |

Each state screen also carries the one-line "what this means" in muted text and, where a previous state exists, a "Last known: …" line. No state screen ever shows a raw API endpoint or a stack trace.

### 5.2 VPN permission flow (first Connect)

This is a system dialog Android owns — it cannot be skipped, faked, or styled. The app's job is to make it unsurprising.

1. User taps **Connect** on Home (first time).
2. **In-app explainer sheet** (bottom sheet, not a dialog): "Android will ask to set up a VPN" — what the dialog will say, why Skein needs it (to create the TUN that carries mesh traffic), what Skein can and cannot see (it routes only the overlay prefix `10.144.144.0/24` + IPv6 overlay; normal internet traffic is untouched; the daemon's own underlay sockets bypass the TUN), and that the user can revoke it anytime in system Settings. Buttons: [Continue] (primary) / [Not now] (secondary, dismisses; Connect stays available).
3. App calls `VpnService.prepare()` (via the Phase 2 bridge, §8). If it returns an Intent, the system dialog appears. If it returns null, permission was already granted — skip to step 5.
4. User taps **OK** in the system dialog → app starts `SkeinVpnService` (foreground service + persistent notification "Skein VPN — Connected to <network>", peer count, Disconnect action). User taps **Cancel** → the permission-denied state screen (§5.1), not a toast.
5. Home updates to the connected state: status sentence, map animates in (nodes → edges → labels), Connect button becomes Disconnect.

**Subsequent Connects:** no explainer, no dialog (permission persists). Disconnect stops the VpnService and the foreground notification; the daemon stays warm in-process so reconnect is fast, but the TUN is gone and the map shows the disconnected state.

**Background:** the foreground-service notification is the source of truth when the app is backgrounded; reopening the app shows live state, never a stale screen. If Android kills the app (OEM battery optimisation — a known limitation, plan §6.3), reopening shows the daemon-down state screen with [Start Skein].

---

## 6. Component inventory

### 6.1 Reused as-is (desktop component, no mobile change)

| Component | Where it lives | Notes |
|---|---|---|
| `Button`, `Badge`, `Card`, `Input`, `Switch`, `Separator`, `Skeleton`, `Metric`, `StatusDot`, `Sparkline`, `Tabs` | `components/ui/*` | Already touch-friendly at 48px when used with `size="lg"`; Phase 3 must audit every usage for the 48px minimum — the desktop `size="sm"` buttons are not mobile-legal. |
| `EmptyState`, `ErrorState` | `components/ui/*` | Copy reused verbatim; illustration slot stays empty (no decorative illustration on mobile). |
| `CopyField` | `components/ui/copy-field.tsx` | Mono + break-all + copy button; used for invites and overlay IPs. |
| `QRCodeView` | `components/ui/qr-code.tsx` | 192px on mobile (desktop renders larger). |
| `PeerHealth` | `features/mesh/PeerHealth.tsx` | Presence + health reason ("relayed — direct path blocked by NAT"). |
| `ThroughputStrip`, `EventStrip` | `features/mesh/*` | Home strips, unchanged data, tighter padding. |
| `Timeline` | `features/activity/Timeline.tsx` | Feed + stored chart; filter chips move to a horizontal scroll row. |
| `AccessPanel`, `IdentityPanel` | `features/security/*` | Inside Settings → Security, stacked vertically. |
| `LogoMark`, `Wordmark` | `components/brand/*` | Wordmark only in the app bar at small size; onboarding uses the mark alone. |
| `useMesh`, `useDaemonEvents`, `useLiveSampler`, `useHistoryRecorder` | `features/mesh/*`, `features/activity/*` | The data layer is shared, not forked. |
| `MeshTable` | `features/mesh/MeshTable.tsx` | Becomes the List-mode fallback (§3.6). |

### 6.2 Needs a mobile variant (same component, new layout/props)

| Component | Mobile change |
|---|---|
| `MeshCanvas` | Compact layout: ellipse positions (already implemented), straight links, max 3 flow dots, selected-link-only marching dashes, `compact` prop gating density and label size. Map/List toggle kept. |
| `PeerInspector` / `LinkInspector` | Re-laid-out as bottom-sheet content (vertical stack, detents, drag handle). Data and actions identical; the side-panel positioning is removed. |
| `RouteDrawer` | Becomes the Routes bottom sheet (§3.4). Same `paths` + `nameOf` props. |
| `Sidebar` + `CommandBar` | Replaced by `BottomNav` + top app bar (§2.2, §2.3). The desktop components are untouched — the mobile shell is a separate component chosen by viewport/platform. |
| `Dialog` (Radix) | Mobile variant renders as a bottom sheet (full-width, top-rounded, drag-to-dismiss) instead of a centred modal. Same API, `variant="sheet"` prop. |
| `Toaster` (sonner) | Position moves from `bottom-right` to `bottom-center`, stacked above the bottom nav (`bottom: calc(var(--nav-h) + 12px)`), max-width `calc(100vw - 32px)`. Action buttons kept — every toast that needs attention carries one ("Peer left — View"). |
| `Onboarding` | Same modes and copy (minus the helper/elevation note, §4.5), stacked single-column with a step indicator; no side panel. |
| `Settings` | Same sections, single column, sticky section Save buttons, dialogs as sheets. Service section drops Install/Repair/Uninstall. |
| `CommandPalette` | **Removed on mobile** (§2.1). No mobile variant. |

### 6.3 New components (mobile only)

| Component | Purpose |
|---|---|
| `BottomNav` | 5-tab bar, badges, safe-area padding, active-pill. New file under `components/layout/`. |
| `MobileAppBar` | Network name + status dot + notifications bell. New file under `components/layout/`. |
| `ConnectButton` | The sticky Home primary button. Owns the state machine (Connect / Disconnect / Resume / Reconnect / working spinner → checkmark) and triggers the VPN-permission explainer on first use. One component, all states — never two buttons. |
| `PeerDetailSheet` | Bottom sheet wrapping the `PeerInspector` content with detents + drag handle + shared-element header. |
| `VpnPermissionSheet` | The first-Connect explainer (§5.2). |
| `StateScreen` | The full-screen state template (§5.1): title, one-line explanation, one primary button, optional secondary. Five instances, one component. |
| `MobileShell` | The mobile `AppShell` equivalent: tab bar + app bar + state gating + event subscriptions. Desktop `AppShell` is untouched; the shell is chosen by platform/viewport in `App.tsx`. |
| `ShareButton` | Wraps the Android share sheet (§8) with clipboard fallback. Used by Invites and Diagnostics export. |
| `MapListToggle` | Segmented Map/List control for the Home map (§3.6). Small, but a distinct component so its persistence and a11y labelling are tested once. |

### 6.4 What the shell keeps from desktop `AppShell`

The state-gating logic is ported, not copied: onboarding owns the window only when the service is known-uninstalled (desktop `onboardingGate`); a stopped service still shows the shell with a state screen; the three independent facts (departed / service / connectivity) are never inferred from one another. On mobile the *presentation* of those facts changes (full-screen state screens instead of shell banners) — the *logic* does not.

---

## 7. Motion

### 7.1 What animates

| Element | Animation | Duration / easing |
|---|---|---|
| Map flow dots | `offset-distance` along the link path (transform-equivalent), linear, infinite while the link is active | Traverse 0.8–2.0 s, clamped (§3.5) |
| Selected-link marching dashes | `stroke-dashoffset`, linear, infinite | 0.6 s loop |
| Node enter / leave | Scale 0.8→1 + opacity (transform-origin centre), `back.out(1.5)` in; opacity out | 300ms in / 180ms out |
| Edge draw-on | `stroke-dashoffset` len→0, easeInOut | 500ms |
| Bottom sheet | translateY 100%→0 in (ease-out), 0→100% out (ease-in) | 220ms in / 180ms out |
| Toast | translateY 8px + opacity in; opacity out | 180ms in / 150ms out |
| Tab switch | Opacity cross-fade of content + accent-pill move (transform) | 180ms |
| Press feedback | Scale 0.97, spring 400/30 | 120ms |
| Connect button state change | Morph in place: spinner → checkmark (opacity + scale, same element mounted) | 200ms |
| Chart draw-on | Path `stroke-dashoffset` len→0, first mount only | 800ms easeInOut |
| Count-up numbers | eased interpolation, easeOutCubic | 1200ms max, only on first mount — live values update instantly thereafter (a count-up on every sample is unreadable) |

### 7.2 60fps rules (non-negotiable)

- **Animate `transform` and `opacity` only.** `stroke-dashoffset` is allowed for the single selected link and the chart draw-on (one element each). Never animate `width`, `height`, `top`, `left`, `margin`, `box-shadow`, or `filter` per frame. Shadows appear via opacity on a pre-rendered pseudo-element; expanding sections use the grid `0fr→1fr` trick, not height animation.
- **Batch layout reads before writes** (no read-write interleaving in the sampler loop); the 2 s live sampler writes state, React renders, and no `getBoundingClientRect` runs inside a render.
- **`will-change: transform` just-in-time** on travelling dots and the sheet, removed when idle. Never blanket-applied.
- **One event subscription for the whole app** (`useDaemonEvents` from `AppShell`), as on desktop. Pages never subscribe. The 2 s sampler feeds the session ring; nothing polls at 1 s.
- **Verification:** Phase 4 profiles the Home map on a real phone (DevTools Performance: no per-frame Layout/Paint bands during flow animation) and confirms 60fps with 4 peers. A mid-range device is the bar, not a flagship.

### 7.3 Reduced motion (see also §1.6)

Tier 1 removed, Tier 2 softened to ≤150ms fades, Tier 3 kept. The map's Map/List toggle and the Settings → Motion switch are the manual controls; `prefers-reduced-motion` (Android: Settings → Accessibility → Remove animations) is honoured live via a `change` listener, no reload. Both the normal and reduced paths must be screenshotted in Phase 3 review.

---

## 8. What the design needs from the backend (Phase 2 gaps)

These are not design decisions — they are services the UI needs that do not exist yet. Phase 3 cannot build the screens that depend on them until Phase 2 lands them.

| # | Need | Why the UI needs it | Status |
|---|---|---|---|
| B1 | **VPN permission bridge**: a Go service binding exposing `VpnPrepare()` (returns whether the system dialog is needed) and `VpnRequest()` (launches the `VpnService.prepare()` intent), plus a `vpn_permission` state (`granted` / `denied` / `unknown`) in the connection status. | The explainer sheet (§5.2) and the permission-denied state screen (§5.1) cannot be built without it. The frontend must never call Android APIs directly. | Does not exist. |
| B2 | **`ConnectionService.Connect/Disconnect` mapped to VpnService start/stop on Android.** Desktop semantics are pause/resume of the dataplane; on Android Connect must establish the TUN and start the foreground service, Disconnect must stop both. | The Connect button's entire state machine depends on this mapping. | Semantics undefined — Phase 2 decision (plan §3.3/§3.4). |
| B3 | **In-process control API** (plan §3.3, option 1 preferred) so `gui/services` works with no socket on Android. | Every screen reads through it. If option 1 is blocked and the abstract-socket fallback is used, the UI is unaffected — but the decision must be made in Phase 2, not discovered in Phase 3. | Does not exist. |
| B4 | **Android share sheet bridge** (or a verified `navigator.share` in the Wails Android WebView) for Invites → Share and Diagnostics → export bundle. | [Share] is a primary action on Invites. Clipboard copy is the fallback and must always be present; the share sheet must not be promised until the bridge is verified on a real device. | Does not exist / unverified. |
| B5 | **Foreground-service notification content**: connection state + peer count + Disconnect action, updated from the daemon's live state. | The notification is the user's only view of Skein while backgrounded; a stale notification is worse than none. | Specified in plan §3.4, not implemented. |
| B6 | **Service state on Android**: `ServiceService.Status()` must report the in-process daemon + VpnService state (running/stopped/error) with a real `last_error`, so Settings → Service and the daemon-down state screen show truth, not a guess. | Desktop bug §2.2.5 (unverified state after an action) must not be re-introduced on Android. | Semantics undefined — Phase 2 decision. |
| B7 | **Config-dir override** (app private files dir, plan §3.3) so Settings → Node shows the real config path and Reset/purge operates on the right directory. | The Switch-network wizard shows a backup path; a desktop path on Android would be a lie. | Does not exist. |

No other backend gaps were found: peers, routes, activities, invites, security, logs, firewall status, and update check all have existing bindings the mobile screens reuse.

---

## 9. Out of scope for v1 (explicit)

1. **Light theme and high-contrast theme on mobile.** Tokens exist; the switch is deferred to v1.1 (§1, D7). Dark only in v1.
2. **Command palette (⌘K).** Dropped on mobile (§2.1). Returns only if a keyboard/tablet build justifies it.
3. **Per-peer invite revocation (F-01).** Deferred post-M9, same as desktop. No UI for it.
4. **"Remove peer" in any form.** Product rule, not a scope choice. Never designed, never added later without a product decision.
5. **QR-code scanning to join.** v1 generates and displays QR codes; joining is by pasting an invite string. Scanning needs a camera bridge and a new permission — v1.1 candidate.
6. **Per-app VPN / split tunnelling UI** (`addDisallowedApplication`). The VpnService supports it; the UI does not expose it in v1. All apps route through the TUN except the daemon's own sockets (`allowBypass`).
7. **In-app update apply.** About shows the version and can *check* for updates; applying an update (download + install APK) is deferred — sideloaded APK updates need the install-packages flow and are a v1.1 item.
8. **Home-screen widgets, quick tiles, and shortcuts.** No widget, no Quick Settings tile in v1. The foreground notification's Disconnect action is the only out-of-app control.
9. **Tablet / multi-window / landscape-first layouts.** Landscape ≥700dp gets the two-column Home (§3.1); a dedicated tablet layout is not designed here.
10. **Diagnostics bundle export if the share bridge (B4) is not ready.** In that case Settings → Diagnostics shows logs read-only and the export button is omitted (not shown disabled with no explanation) until the bridge lands.
11. **Multiple networks / network switching without leaving.** One network at a time; switching is leave → reset → create/join (§4.4).
12. **iOS, Play Store submission, AAB.** Per the parent plan §1.

---

## 10. Acceptance checks for Phase 3 (what "matches this brief" means)

- [ ] Home shows status sentence + compact map + Connect button in the first viewport on a 360×800dp phone; map fits without scrolling on portrait.
- [ ] Five tabs, all labels visible, all targets ≥48dp; every feature reachable in ≤2 taps from Home.
- [ ] Tapping a peer opens the detail sheet; tapping a link opens link detail; the sheet has 3 detents and a drag handle.
- [ ] Map/List toggle works and List mode carries identical data with identical labels.
- [ ] All five state screens exist with their specified button, and each button does the thing it says.
- [ ] First Connect shows the explainer sheet *before* the system dialog; denying permission lands on the permission-denied screen with [Open VPN settings].
- [ ] No "remove peer" control exists anywhere; no per-peer revocation UI; no raw API endpoint is shown to the user.
- [ ] Reduced-motion path verified by screenshot (normal + reduced); 60fps profiled on a real phone with 4 peers.
- [ ] `npm run build` and `vitest` pass; desktop GUI untouched (no changes under the desktop shell/routes except shared components the mobile variants extend).
- [ ] Owner agrees the home-map treatment (§3) and bottom-nav structure (§2) before Phase 3 builds — this brief is the thing being agreed, not a formality.

---

## 11. Sources and traceability

- Parent plan: `docs/superpowers/plans/2026-11-13-android-mobile.md` (§4 is the brief this document expands; §§3, 5, 6 are the backend/phase context it depends on).
- Desktop vision: `~/workspace/skein-work/gui-redesign-prompt.md` (§§3–4: design language, mesh map, activity, peers, switch-network wizard, no dead ends; §2.2 bugs 3–5 are the acceptance flows §4.4/§5 implement).
- Incumbent implementation: `gui/frontend/src/{App.tsx → app/App.tsx, components/layout/*, routes/*, features/mesh/*, features/activity/*, styles/tokens.css}`, `gui/services/{connection,peer,invite,service,config}.go`.
- Skills applied: brainstorming (design-before-build, this doc as the design artefact); frontend-design (bold direction, rejected generic aesthetics); impeccable v4.5.2 (Operate mode, brief-wins, refinement preserves the incumbent tokens); ui_ux_design (design-system helper consulted, pattern/typography rejected with reason, pre-delivery checklist adopted in §10); diagram-animation (nodes→edges→labels reveal, travelling-dot flow); micro-interaction (100–250ms, press scale 0.96–0.97, AnimatePresence sheets/toasts); 60fps-animation (transform/opacity only, FLIP/grid tricks, will-change just-in-time); accessible-animation (tiered reduced motion, `useReducedMotion` hook, 0.01ms not 0s).
