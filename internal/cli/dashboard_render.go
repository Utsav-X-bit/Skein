package cli

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// build is retained for snapshot callers; interactive drawing supplies height.
func (d *dashboard) build(v view, width int) []string { return d.buildSized(v, width, 36) }

func (d *dashboard) buildSized(v view, width, height int) []string {
	if width < 1 {
		return nil
	}
	l := resolveLayout(width, height)
	switch l.mode {
	case modeShort:
		return d.renderShortDeck(v, l, width, height)
	case modeCompact:
		return d.renderCompactDeck(v, l, width, height)
	}
	lines := append([]string(nil), d.renderBrand(v, l.brand)...)
	if l.metrics.h > 0 {
		lines = append(lines, d.renderMetricStrip(v, l.metrics)...)
	}
	if v.ui.help {
		lines = append(lines, d.renderHelp(width)...)
		return append(lines, d.footer(v, width, l.mode == modeWide))
	}
	if v.ui.expanded {
		rect := inspectorRect(l, width, height)
		expanded := v
		expanded.ui.focus = v.ui.expandedFocus
		var overlay []string
		if l.mode == modeShort {
			_, overlay = d.page(expanded, width, max(0, rect.h))
		} else {
			title, body := d.page(expanded, max(1, rect.w-2), max(1, rect.h-2))
			overlay = d.focusedCard(title, body, rect, true)
		}
		// The panel expands into place instead of popping open.
		if v.motion && !v.ui.paused && !v.ui.expandedAt.IsZero() {
			if elapsed := time.Since(v.ui.expandedAt); elapsed < inspectorReveal {
				shown := int(easeOut(float64(elapsed)/float64(inspectorReveal)) * float64(len(overlay)))
				if shown < 1 {
					shown = 1
				}
				overlay = overlay[:shown]
			}
		}
		lines = append(lines, overlay...)
		lines = append(lines, d.footer(v, width, l.mode == modeWide))
		return clampLines(lines, height)
	}
	if l.mode == modeWide {
		lines = append(lines, d.renderWide(v, l)...)
		lines = append(lines, d.footer(v, width, true))
		return lines
	}
	mapCard := d.renderMapCard(v, l.rects[focusMap])
	peerCard := d.renderPeerCard(v, l.rects[focusPeers])
	detailCard := d.renderDetailCard(v, l.rects[focusOverview])
	lines = append(lines, mapCard...)
	lines = append(lines, joinColumns([][]string{peerCard, detailCard}, []int{l.rects[focusPeers].w, l.rects[focusOverview].w}, l.rects[focusPeers].h)...)
	lines = append(lines, d.footer(v, width, false))
	return clampLines(lines, height)
}

func clampLines(lines []string, height int) []string {
	if height > 0 && len(lines) > height {
		return lines[:height]
	}
	return lines
}

// renderCompactDeck is one focused card under a status line and tab strip.
func (d *dashboard) renderCompactDeck(v view, l dashboardLayout, width, height int) []string {
	lines := []string{d.statusLine(v, width), d.tabStrip(v.ui.focus, width)}
	title, body := d.page(v, max(1, l.body.w-2), max(1, l.body.h-2))
	if v.ui.help {
		title, body = "HELP", d.renderHelp(max(1, l.body.w-2))
	}
	lines = append(lines, d.focusedCard(title, body, l.body, true)...)
	lines = append(lines, d.footer(v, width, false))
	return clampLines(lines, height)
}

// renderShortDeck keeps only facts and controls; decorative borders go first.
func (d *dashboard) renderShortDeck(v view, l dashboardLayout, width, height int) []string {
	lines := []string{d.statusLine(v, width)}
	_, body := d.page(v, width, max(0, l.body.h))
	if v.ui.help {
		body = d.renderHelp(width)
	}
	lines = append(lines, body...)
	lines = append(lines, d.footer(v, width, false))
	return clampLines(lines, height)
}

func (d *dashboard) statusLine(v view, width int) string {
	if v.err != nil {
		return d.line(d.paint(ansiError, " ! daemon disconnected · "+age(v.lastFetch)), width)
	}
	up, down := 0, 0
	for _, p := range v.peers {
		if p.State == "up" {
			up++
		} else {
			down++
		}
	}
	parts := []string{}
	if name := localNameOf(v.st); name != "ME" {
		parts = append(parts, name)
	}
	parts = append(parts, dash(v.st.OverlayIPv4), fmt.Sprintf("%d up", up))
	if down > 0 {
		parts = append(parts, fmt.Sprintf("%d down", down))
	}
	parts = append(parts, humanRate(totalRate(v.rates)), shortID(v.st.ID))
	line := " Skein"
	for _, part := range parts {
		if visibleWidth(line)+3+visibleWidth(part) > width {
			continue
		}
		line += " · " + part
	}
	return d.line(line, width)
}

func (d *dashboard) tabStrip(focus dashboardFocus, width int) string {
	pages := []struct {
		f    dashboardFocus
		name string
	}{
		{focusOverview, "Overview"}, {focusMap, "Map"}, {focusPeers, "Peers"},
		{focusRoutes, "Routes"}, {focusActivity, "Activity"},
	}
	parts := make([]string, 0, len(pages))
	for _, p := range pages {
		if p.f == focus {
			parts = append(parts, d.paint(ansiBrand, "["+p.name+"]"))
		} else {
			parts = append(parts, p.name)
		}
	}
	line := " " + strings.Join(parts, " ")
	if visibleWidth(line) <= width {
		return fit(line, width)
	}
	// Narrow terminal: keep the selected page named, the footer carries the
	// TAB hint.
	for _, p := range pages {
		if p.f == focus {
			return d.line(" ‹ "+p.name+" ›", width)
		}
	}
	return d.line("", width)
}

// page returns the title and body for the focused panel/page.
func (d *dashboard) page(v view, width, height int) (string, []string) {
	switch v.ui.focus {
	case focusMap:
		return "LIVE ROUTE MAP", d.renderRouteMap(v, width, height)
	case focusPeers:
		return "PEERS", d.renderPeers(v, width, height)
	case focusRoutes:
		return "ROUTES", d.renderRoutes(v, width, height)
	case focusActivity:
		return "ACTIVITY", d.renderActivity(v, width, height)
	default:
		return "OVERVIEW", d.renderInspector(v, width)
	}
}

func (d *dashboard) footer(v view, width int, wide bool) string {
	motion := "live"
	if v.ui.paused {
		motion = "paused"
	}
	pos := d.position(v)
	hint := ""
	if v.ui.expanded {
		hint = " · ESC back"
	} else if width >= 96 {
		hint = " · ENTER expand"
	}
	if wide {
		return d.line(fmt.Sprintf(" %s  focus %s · %s  SPACE %s motion · TAB panels · j/k select%s · ? help · q quit", sparkline(v.spark), focusName(v.ui.focus), pos, motion, hint), width)
	}
	if width < 96 {
		return d.line(fmt.Sprintf(" %s · %s · TAB pages · j/k · SPACE %s · ? · q", focusName(v.ui.focus), pos, motion), width)
	}
	return d.line(fmt.Sprintf(" %s  focus %s · %s  SPACE %s motion%s · ? help · q quit", sparkline(v.spark), focusName(v.ui.focus), pos, motion, hint), width)
}

func (d *dashboard) position(v view) string {
	switch v.ui.focus {
	case focusRoutes:
		if len(v.routes) == 0 {
			return "route 0/0"
		}
		index := 0
		for n, r := range v.routes {
			if r.Destination == v.ui.routeSel {
				index = n
			}
		}
		return fmt.Sprintf("route %d/%d", index+1, len(v.routes))
	case focusActivity:
		rows := activityRows(v.st.ID, localNameOf(v.st), v.peers, v.activities, time.Now().UnixMilli())
		if len(rows) == 0 {
			return "activity 0/0"
		}
		index := 0
		for n, row := range rows {
			if row.key == v.ui.activitySel {
				index = n
			}
		}
		return fmt.Sprintf("activity %d/%d", index+1, len(rows))
	default:
		if len(v.peers) == 0 {
			return "peer 0/0"
		}
		index := 0
		for n, p := range v.peers {
			if p.ID == v.ui.selected {
				index = n
			}
		}
		return fmt.Sprintf("peer %d/%d", index+1, len(v.peers))
	}
}

func (d *dashboard) renderWide(v view, l dashboardLayout) []string {
	left, center, right := l.rects[focusPeers], l.rects[focusMap], l.rects[focusOverview]
	leftLines := d.renderPeerCard(v, left)
	centerLines := d.renderMapCard(v, center)
	detailH := max(5, right.h/2)
	routesH := min(5, max(3, right.h/4))
	activityH := max(3, right.h-detailH-routesH)
	rightLines := d.renderDetailCard(v, dashboardRect{w: right.w, h: detailH})
	rightLines = append(rightLines, d.focusedCard("ROUTES", d.renderRoutes(v, max(1, right.w-2), max(1, routesH-2)), dashboardRect{w: right.w, h: routesH}, v.ui.focus == focusRoutes)...)
	rightLines = append(rightLines, d.focusedCard("ACTIVITY", d.renderActivity(v, max(1, right.w-2), max(1, activityH-2)), dashboardRect{w: right.w, h: activityH}, v.ui.focus == focusActivity)...)
	return joinColumns([][]string{leftLines, centerLines, rightLines}, []int{left.w, center.w, right.w}, left.h)
}

func (d *dashboard) renderMapCard(v view, r dashboardRect) []string {
	body := d.renderRouteMap(v, max(1, r.w-2), max(1, r.h-2))
	return d.focusedCard("LIVE ROUTE MAP", body, r, v.ui.focus == focusMap)
}

func joinColumns(columns [][]string, widths []int, height int) []string {
	lines := make([]string, 0, height)
	for y := 0; y < height; y++ {
		var b strings.Builder
		for x, column := range columns {
			line := ""
			if y < len(column) {
				line = column[y]
			}
			b.WriteString(fit(line, widths[x]))
			if x < len(columns)-1 {
				b.WriteString("  ")
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}

func (d *dashboard) bar(title string, width int) string {
	plain := "─ " + title + " "
	if n := width - len([]rune(plain)); n > 0 {
		plain += strings.Repeat("─", n)
	}
	return d.paint(ansiCyan, fit(plain, width))
}
func (d *dashboard) line(s string, width int) string { return fit(s, width) }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func focusName(f dashboardFocus) string {
	return []string{"overview", "map", "peers", "routes", "activity", "help"}[f]
}
func age(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return humanDuration(time.Since(t)) + " ago"
}

func (d *dashboard) renderHelp(width int) []string {
	return []string{d.line(" TAB/h/l panels   j/k or arrows select peer   ENTER inspector", width), d.line(" SPACE pause motion   ESC back   q quit", width), d.line(" Data is local counters plus authenticated aggregate mesh telemetry.", width)}
}

func selectedPeer(v view) (peerDTO, bool) {
	if v.ui.selected != "" {
		for _, p := range v.peers {
			if p.ID == v.ui.selected {
				return p, true
			}
		}
	}
	if len(v.peers) == 0 {
		return peerDTO{}, false
	}
	peers := append([]peerDTO(nil), v.peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	return peers[0], true
}

// peerActive reports recent traffic with a peer, from local rates or a fresh
// remote activity sample.
func peerActive(v view, id string) bool {
	if r := v.rates[id]; r.rx+r.tx > 32 {
		return true
	}
	now := time.Now().UnixMilli()
	for _, a := range v.activities {
		if a.ExpiresMS <= now || a.RxBytes+a.TxBytes == 0 {
			continue
		}
		if a.Origin == id || a.Destination == id {
			return true
		}
	}
	return false
}

func (d *dashboard) renderPeers(v view, width, limit int) []string {
	if len(v.peers) == 0 {
		return []string{d.line(" no peers yet — run `skein join` with a shared invite", width)}
	}
	peers := append([]peerDTO(nil), v.peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	rows := 1
	if width < 64 {
		rows = 2
	}
	selectedIndex := 0
	for i, p := range peers {
		if p.ID == v.ui.selected {
			selectedIndex = i
		}
	}
	visible := max(1, limit/rows)
	start := 0
	if selectedIndex >= visible {
		start = selectedIndex - visible + 1
	}
	lines := make([]string, 0, min(limit, len(peers)))
	for i := start; i < len(peers); i++ {
		p := peers[i]
		if len(lines) >= limit {
			break
		}
		mark := " "
		if p.ID == v.ui.selected || (v.ui.selected == "" && i == 0) {
			mark = d.paint(ansiBrand, ">")
		}
		active := peerActive(v, p.ID)
		dot := " "
		if active {
			dot = d.paint(ansiOut, "●")
		}
		m := v.metrics[p.ID]
		rtt := "—"
		value := m.rtt
		if value == 0 {
			value = p.RTTMs
		}
		if value > 0 {
			rtt = fmt.Sprintf("%.1f ms", value)
		}
		name := p.Name
		if name == "" {
			name = shortID(p.ID)
		}
		state := d.paint(stateColor(p.State), p.State)
		transport := dash(p.Transport)
		if width < 64 {
			// The overlay identifies the peer; the full endpoint stays in
			// the inspector when the card is too narrow for both.
			nameText := name
			if active {
				nameText = d.paint(ansiOut, name)
			}
			// Reserve the selection marker and activity dot before fitting,
			// so the overlay address is not clipped afterwards.
			fields := compactFields(nameText, state, transport, dash(p.OverlayIPv4), fmt.Sprintf("↓%s ↑%s · %s", humanRate(m.rx), humanRate(m.tx), rtt), width-2)
			fields[0] = mark + dot + strings.TrimPrefix(fields[0], " ")
			for _, line := range fields {
				if len(lines) >= limit {
					break
				}
				lines = append(lines, line)
			}
			continue
		}
		overlay := dash(p.OverlayIPv4)
		line := fmt.Sprintf("%s%s %-13s %-9s %-4s %-15s %7s", mark, dot, clip(name, 13), state, transport, overlay, rtt)
		if endpoint := dash(p.Endpoint); endpoint != "—" && visibleWidth(line)+1+visibleWidth(endpoint) <= width-18 {
			line += " " + endpoint
		}
		line += fmt.Sprintf("  ↓%s ↑%s", humanRate(m.rx), humanRate(m.tx))
		lines = append(lines, d.line(line, width))
	}
	return lines
}

func rttColorFor(ms float64) string {
	switch {
	case ms <= 0:
		return ansiSlate
	case ms < 40:
		return ansiHealthy
	case ms < 120:
		return ansiWarn
	default:
		return ansiError
	}
}

// inspectorFields returns the selected link's facts without width decisions.
func (d *dashboard) inspectorFields(v view) []string {
	p, ok := selectedPeer(v)
	if !ok {
		return []string{"select a peer to inspect speed, latency, and path health"}
	}
	m := v.metrics[p.ID]
	r := v.rates[p.ID]
	name := p.Name
	if name == "" {
		name = shortID(p.ID)
	}
	transport := p.Transport
	if transport == "" {
		transport = "—"
	}
	link := "direct"
	if !p.Direct {
		link = "relayed"
	}
	rtt := m.rtt
	if rtt == 0 {
		rtt = p.RTTMs
	}
	minRTT, maxRTT := m.minRTT, m.maxRTT
	if minRTT == 0 {
		minRTT = rtt
	}
	if maxRTT == 0 {
		maxRTT = rtt
	}
	latency := "unavailable"
	rttPaint := ansiSlate
	if rtt > 0 {
		latency = fmt.Sprintf("%.1f ms (%.1f–%.1f, jitter %.1f)", rtt, minRTT, maxRTT, m.jitter)
		rttPaint = rttColorFor(rtt)
	}
	return []string{
		fmt.Sprintf("%s · %s %s · %s", name, d.paint(stateColor(p.State), p.State), link, d.paint(ansiSlate, transport)),
		"endpoint " + d.paint(ansiWhite, dash(p.Endpoint)),
		fmt.Sprintf("RX %s  TX %s  peak %s", d.paint(ansiIn, humanRate(maxRate(m.rx, r.rx))), d.paint(ansiOut, humanRate(maxRate(m.tx, r.tx))), humanRate(maxRate(m.peak, r.rx+r.tx))),
		"RTT " + d.paint(rttPaint, latency),
		"last seen " + lastSeen(p.LastSeenMS),
	}
}

// renderInspector wraps every field, so a narrow card shows the whole
// endpoint instead of an ellipsis.
func (d *dashboard) renderInspector(v view, width int) []string {
	lines := make([]string, 0, 8)
	for _, field := range d.inspectorFields(v) {
		for _, line := range wrapVisible(field, max(1, width-1)) {
			lines = append(lines, d.line(" "+line, width))
		}
	}
	return lines
}

func maxRate(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func lastSeen(ms int64) string {
	if ms <= 0 {
		// The daemon reports zero for a peer it has never heard from.
		return "never"
	}
	return humanDuration(time.Duration(ms)*time.Millisecond) + " ago"
}

func (d *dashboard) renderRoutes(v view, width, limit int) []string {
	if len(v.routes) == 0 {
		return []string{d.line(" no resolved routes", width)}
	}
	selectedIndex := 0
	for i, r := range v.routes {
		if r.Destination == v.ui.routeSel {
			selectedIndex = i
		}
	}
	start := 0
	if selectedIndex >= limit {
		start = selectedIndex - limit + 1
	}
	lines := make([]string, 0, min(limit, len(v.routes)))
	for i := start; i < len(v.routes); i++ {
		r := v.routes[i]
		if len(lines) >= limit {
			break
		}
		path := append([]string{v.st.ID}, r.Path...)
		if len(r.Path) == 0 {
			path = []string{v.st.ID, r.NextHop, r.Destination}
		}
		if len(path) == 0 || path[len(path)-1] != r.Destination {
			path = append(path, r.Destination)
		}
		labels := make([]string, 0, len(path))
		for _, id := range path {
			label := d.displayName(v, id)
			if id == v.st.ID {
				label = d.paint(ansiBrand, label)
			}
			labels = append(labels, label)
		}
		mark := " "
		if r.Destination == v.ui.routeSel || (v.ui.routeSel == "" && len(lines) == 0) {
			mark = d.paint(ansiBrand, ">")
		}
		line := fmt.Sprintf("%s %s  %s · %s", mark,
			d.paint(ansiWhite, strings.Join(labels, " → ")),
			d.paint(ansiSlate, fmt.Sprintf("%d hop", r.Hops)),
			d.paint(rttColorFor(r.CostMs), fmt.Sprintf("%.1f ms", r.CostMs)))
		lines = append(lines, d.line(line, width))
	}
	return lines
}
func shortPath(path []string) []string {
	out := make([]string, len(path))
	for i, s := range path {
		if s == "me" {
			out[i] = s
		} else {
			out[i] = shortID(s)
		}
	}
	return out
}

// activityRow is one directional row derived from an aggregate sample. A
// remote sample about this node yields two rows: what we sent and what we
// received, so a local ping reads ME → peer.
type activityRow struct {
	key    string
	peerID string
	from   string
	to     string
	rx, tx float64
	rtt    float64
	dir    int // +1 outbound, -1 inbound, 0 remote link
}

func localNameOf(st statusDTO) string {
	if st.Name != "" {
		return st.Name
	}
	return "ME"
}

func nodeLabel(id, localID, localName string, peers []peerDTO) string {
	if id == "" {
		return "—"
	}
	if id == localID {
		return localName
	}
	for _, p := range peers {
		if p.ID == id {
			if p.Name != "" {
				return p.Name
			}
			return shortID(p.ID)
		}
	}
	return shortID(id)
}

// activityRows localizes samples and splits links involving this node into
// their outbound and inbound directions.
func activityRows(localID, localName string, peers []peerDTO, items []activityDTO, nowMS int64) []activityRow {
	out := make([]activityRow, 0, len(items))
	for _, a := range freshActivitiesFrom(items, nowMS) {
		base := activityKey(a)
		switch {
		case a.Destination == localID && a.Origin != localID:
			// The remote peer reported its link to us: RxBytes is what it
			// received from us (our outbound), TxBytes what it sent.
			if a.RxBytes > 0 {
				out = append(out, activityRow{key: base + "|out", peerID: a.Origin, from: localName, to: nodeLabel(a.Origin, localID, localName, peers), tx: float64(a.RxBytes), rtt: a.RTTMs, dir: 1})
			}
			if a.TxBytes > 0 {
				out = append(out, activityRow{key: base + "|in", peerID: a.Origin, from: nodeLabel(a.Origin, localID, localName, peers), to: localName, rx: float64(a.TxBytes), rtt: a.RTTMs, dir: -1})
			}
		case a.Origin == localID:
			out = append(out, activityRow{key: base + "|out", peerID: a.Destination, from: localName, to: nodeLabel(a.Destination, localID, localName, peers), tx: float64(a.TxBytes), rtt: a.RTTMs, dir: 1})
		default:
			out = append(out, activityRow{key: base, peerID: a.Destination, from: nodeLabel(a.Origin, localID, localName, peers), to: nodeLabel(a.Destination, localID, localName, peers), rx: float64(a.RxBytes), tx: float64(a.TxBytes), rtt: a.RTTMs})
		}
	}
	return out
}

func (d *dashboard) activityRowsFor(v view) []activityRow {
	return activityRows(v.st.ID, localNameOf(v.st), v.peers, v.activities, time.Now().UnixMilli())
}

func selectedActivityRow(rows []activityRow, key string) (activityRow, bool) {
	if len(rows) == 0 {
		return activityRow{}, false
	}
	for _, row := range rows {
		if row.key == key {
			return row, true
		}
	}
	return rows[0], true
}

func (d *dashboard) renderActivity(v view, width, limit int) []string {
	rows := d.activityRowsFor(v)
	selectedIndex := 0
	for i, row := range rows {
		if row.key == v.ui.activitySel {
			selectedIndex = i
		}
	}
	start := 0
	if selectedIndex >= limit {
		start = selectedIndex - limit + 1
	}
	lines := make([]string, 0, limit)
	for i := start; i < len(rows); i++ {
		if len(lines) >= limit {
			break
		}
		row := rows[i]
		mark := " "
		if row.key == v.ui.activitySel || (v.ui.activitySel == "" && len(lines) == 0) {
			mark = d.paint(ansiBrand, ">")
		}
		arrow := ansiSlate
		switch row.dir {
		case 1:
			arrow = ansiOut
		case -1:
			arrow = ansiIn
		}
		rate := "—"
		switch {
		case row.rx > 0 && row.tx > 0:
			rate = "↓" + d.paint(ansiIn, humanRate(row.rx)) + " ↑" + d.paint(ansiOut, humanRate(row.tx))
		case row.rx > 0:
			rate = "↓" + d.paint(ansiIn, humanRate(row.rx))
		case row.tx > 0:
			rate = "↑" + d.paint(ansiOut, humanRate(row.tx))
		}
		from, to := row.from, row.to
		if from == localNameOf(v.st) {
			from = d.paint(ansiBrand, from)
		}
		if to == localNameOf(v.st) {
			to = d.paint(ansiBrand, to)
		}
		line := fmt.Sprintf("%s %s %s %s  %s  %s", mark, from, d.paint(arrow, "→"), to, rate, d.paint(rttColorFor(row.rtt), fmt.Sprintf("%.1f ms", row.rtt)))
		lines = append(lines, d.line(line, width))
	}
	if len(lines) == 0 {
		lines = append(lines, d.line(" quiet — no fresh aggregate mesh activity", width))
	}
	return lines
}

// displayName localizes a node ID for display: this node uses its configured
// name, known peers use theirs, and everything else falls back to a short ID.
func (d *dashboard) displayName(v view, id string) string {
	return nodeLabel(id, v.st.ID, localNameOf(v.st), v.peers)
}

func stateColor(state string) string {
	switch state {
	case "up":
		return ansiHealthy
	case "degraded":
		return ansiWarn
	default:
		return ansiError
	}
}

// renderRouteMap draws only edges substantiated by route paths. A single peer
// gets a horizontal lane instead of a sparse radial diagram.
func (d *dashboard) renderRouteMap(v view, width, height int) []string {
	peers := connectedPeers(v)
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	if len(peers) == 0 {
		c := newCanvas(width, height)
		c.text(max(0, width/2-9), max(0, height/2), "no connected peers", ansiDim)
		if height >= 6 && width >= 34 {
			c.text(1, height-1, "● up  ─ route  · reported", ansiSlate)
		}
		return c.lines(d.color)
	}
	if len(peers) == 1 {
		return d.renderLinkLane(v, peers[0], width, height)
	}

	c := newCanvas(width, height)
	cx, cy := width/2, height/2
	type point struct {
		x, y int
		id   string
		name string
	}
	points := map[string]point{"me": {cx, cy, "me", localNameOf(v.st)}}
	rx, ry := max(8, min(width/2-10, 32)), max(2, min(height/2-1, 4))
	for i, p := range peers {
		a := -math.Pi/2 + 2*math.Pi*float64(i)/float64(max(1, len(peers)))
		points[p.ID] = point{cx + int(math.Round(float64(rx)*math.Cos(a))), cy + int(math.Round(float64(ry)*math.Sin(a))), p.ID, trunc(d.displayName(v, p.ID), 10)}
	}

	routes := d.routeEdges(v)
	reported := d.activityEdges(v)
	selected := map[mapEdge]bool{}
	if v.ui.focus == focusRoutes {
		if r, ok := selectedRoute(v); ok {
			path := d.routePath(v, r)
			for i := 1; i < len(path); i++ {
				selected[mapEdge{path[i-1], path[i]}] = true
			}
		}
	}

	drawEdge := func(e mapEdge) {
		a, b := points[e.from], points[e.to]
		glyph, color := '·', ansiDim // reported: this node cannot use it
		if routes[e] {
			glyph, color = '─', ansiSlate // usable route
		}
		if selected[e] || e.from == v.ui.selected || e.to == v.ui.selected {
			color = ansiBrand
		}
		c.line(a.x, a.y, b.x, b.y, glyph, color)
		for _, pulse := range d.edgePulses(v, e.from, e.to) {
			d.drawPulse(c, a.x, a.y, b.x, b.y, e.from+e.to+pulse.color, pulse.color, pulse.reverse, v.phase)
		}
	}
	for e := range routes {
		drawEdge(e)
	}
	for e := range reported {
		if !routes[e] {
			drawEdge(e)
		}
	}
	states := map[string]string{}
	for _, p := range v.peers {
		states[p.ID] = p.State
	}
	for id, p := range points {
		glyph, col := "●", ansiHealthy
		if id == "me" {
			glyph, col = "◆", ansiCyan
		} else {
			col = stateColor(states[id])
			if peerActive(v, id) {
				col = ansiOut
			}
		}
		if id == v.ui.selected {
			col = ansiBrand
			glyph = "◉"
			if !d.unicode {
				glyph = "O"
			}
		}
		c.text(p.x, p.y, glyph, col)
		labelX := p.x + 2
		if id != "me" && p.x < cx {
			// Left of the hub: keep the label on the outside of the link.
			labelX = p.x - visibleWidth(p.name) - 1
		}
		if labelX+visibleWidth(p.name) > width {
			labelX = p.x - visibleWidth(p.name) - 1
		}
		if labelX < 0 {
			labelX = p.x + 2
		}
		labelColor := ansiWhite
		if id == "me" {
			labelColor = ansiBrand
		}
		c.text(max(0, labelX), p.y, p.name, labelColor)
	}
	if len(routes) == 0 && len(reported) == 0 {
		c.text(max(0, cx-10), cy+1, "waiting for routes", ansiDim)
	}
	if height >= 6 && width >= 34 {
		c.text(1, height-1, "● up  ─ route  · reported", ansiSlate)
	}
	return c.lines(d.color)
}

// renderLinkLane draws a single direct peer as a horizontal link with anchors
// and its measured link facts.
func (d *dashboard) renderLinkLane(v view, p peerDTO, width, height int) []string {
	c := newCanvas(width, height)
	cy := height / 2
	if cy > height-2 {
		cy = height - 2
	}
	if cy < 1 {
		cy = 1
	}
	left := 2
	name := d.displayName(v, p.ID)
	healthy := p.State == "up"
	lineGlyph, lineColor := '·', ansiDim
	if healthy {
		lineGlyph, lineColor = '━', ansiBrand
	}
	nodeGlyph := "●"
	if !healthy {
		nodeGlyph = "○"
	}
	labelX := width - 2 - visibleWidth(name)
	if labelX < left+8 {
		labelX = left + 8
	}
	laneEnd := labelX - 2
	if laneEnd < left+3 {
		laneEnd = left + 3
	}
	local := localNameOf(v.st)
	laneStart := left + 2 + visibleWidth(local) + 1
	if laneStart > laneEnd {
		laneStart = laneEnd
	}
	c.line(laneStart, cy, laneEnd, cy, lineGlyph, lineColor)
	c.text(left, cy, "◆", ansiCyan)
	c.text(left+2, cy, local, ansiBrand)
	nodeColor := stateColor(p.State)
	nameColor := ansiWhite
	if peerActive(v, p.ID) {
		nodeColor = ansiOut
		nameColor = ansiOut
	}
	c.text(labelX, cy, nodeGlyph, nodeColor)
	c.text(labelX+2, cy, name, nameColor)

	rtt := p.RTTMs
	if m, ok := v.metrics[p.ID]; ok && m.rtt > 0 {
		rtt = m.rtt
	}
	transport := p.Transport
	if transport == "" {
		transport = "—"
	}
	hops := 1
	if r, ok := selectedRouteFrom(v.routes, p.ID); ok && r.Hops > 0 {
		hops = int(r.Hops)
	}
	facts := fmt.Sprintf("%s · %s · %d hop · %s", transport, fmtRTT(rtt), hops, p.State)
	if p.Endpoint != "" {
		facts += " · " + p.Endpoint
	}
	facts = clip(facts, max(0, width-4))
	c.text(max(0, (width-visibleWidth(facts))/2), cy+1, facts, ansiSlate)

	if v.motion && !v.ui.paused {
		r := v.rates[p.ID]
		if r.tx > 1 {
			d.drawPulse(c, laneStart, cy, laneEnd, cy, p.ID+"tx", ansiOut, false, v.phase)
		}
		if r.rx > 1 {
			d.drawPulse(c, laneEnd, cy, laneStart, cy, p.ID+"rx", ansiIn, false, v.phase)
		}
	}
	return c.lines(d.color)
}

type edgePulse struct {
	color   string
	reverse bool
}

// edgePulses reports the directional traffic on an edge. Local counters give
// outbound and inbound directions; activities add remote flows.
func (d *dashboard) edgePulses(v view, a, b string) []edgePulse {
	var out []edgePulse
	seen := map[edgePulse]bool{}
	add := func(color string, reverse bool) {
		p := edgePulse{color: color, reverse: reverse}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if a == "me" || b == "me" {
		other := b
		if b == "me" {
			other = a
		}
		r := v.rates[other]
		if r.tx > 1 {
			add(ansiOut, false)
		}
		if r.rx > 1 {
			add(ansiIn, true)
		}
	}
	now := time.Now().UnixMilli()
	for _, act := range v.activities {
		if act.ExpiresMS <= now || act.RxBytes+act.TxBytes == 0 {
			continue
		}
		path := d.activityPath(v, act)
		for i := 1; i < len(path); i++ {
			from, to := path[i-1], path[i]
			if (from != a || to != b) && (from != b || to != a) {
				continue
			}
			color := ansiSlate
			switch {
			case from == "me":
				color = ansiOut
			case to == "me":
				color = ansiIn
			}
			add(color, from != a)
		}
	}
	return out
}

func (d *dashboard) drawPulse(c *canvas, x0, y0, x1, y1 int, id, color string, reverse bool, phase float64) {
	// About two seconds per traverse, so each 40 ms frame advances a couple
	// of cells instead of a visible jump.
	p := math.Mod(phase*(0.4+float64(hashID(id)%3)*0.1)+float64(hashID(id)%100)/100, 1)
	if reverse {
		p = 1 - p
	}
	c.set(lerp(x0, x1, p), lerp(y0, y1, p), '●', color)
}

// routePath returns the route as node keys, with this node as "me".
func (d *dashboard) routePath(v view, r routeDTO) []string {
	path := []string{"me"}
	if len(r.Path) > 0 {
		path = append(path, r.Path...)
	} else {
		path = append(path, r.NextHop)
		if r.NextHop != r.Destination {
			path = append(path, r.Destination)
		}
	}
	for i, id := range path {
		if id == v.st.ID {
			path[i] = "me"
		}
	}
	return path
}

// activityPath returns the activity path with this node as "me".
func (d *dashboard) activityPath(v view, a activityDTO) []string {
	path := append([]string{a.Origin}, a.Path...)
	if path[len(path)-1] != a.Destination {
		path = append(path, a.Destination)
	}
	for i, id := range path {
		if id == v.st.ID {
			path[i] = "me"
		}
	}
	return path
}

// activityKey identifies one aggregate activity row stably across refreshes.
func activityKey(a activityDTO) string {
	return a.Origin + "|" + a.Destination + "|" + strings.Join(a.Path, "/")
}

// freshActivitiesFrom returns the unexpired rows in server order.
func freshActivitiesFrom(items []activityDTO, nowMS int64) []activityDTO {
	out := make([]activityDTO, 0, len(items))
	for _, a := range items {
		if a.ExpiresMS > nowMS {
			out = append(out, a)
		}
	}
	return out
}

func selectedRouteFrom(routes []routeDTO, sel string) (routeDTO, bool) {
	if len(routes) == 0 {
		return routeDTO{}, false
	}
	for _, r := range routes {
		if r.Destination == sel {
			return r, true
		}
	}
	return routes[0], true
}

func selectedRoute(v view) (routeDTO, bool) {
	return selectedRouteFrom(v.routes, v.ui.routeSel)
}

// inspectorRect is the content area the expanded inspector may use.
func inspectorRect(l dashboardLayout, width, height int) dashboardRect {
	switch l.mode {
	case modeShort, modeCompact:
		return l.body
	default:
		y := l.brand.h + l.metrics.h
		return dashboardRect{y: y, w: width, h: max(0, height-y-l.footer.h)}
	}
}

// easeOut is the shared ease-out curve for short reveals.
func easeOut(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return 1 - math.Pow(1-t, 3)
}

// mapEdge is one substantiated link between "me" and a peer, or between two
// peers reported by the mesh.
type mapEdge struct{ from, to string }

// connectedPeers are the peers the map may draw: a session is up (or
// degraded but still present). Configured peers that never connected stay in
// the peers list for diagnostics but do not clutter the topology.
func connectedPeers(v view) []peerDTO {
	out := make([]peerDTO, 0, len(v.peers))
	for _, p := range v.peers {
		if p.State == "up" || p.State == "degraded" {
			out = append(out, p)
		}
	}
	return out
}

// knownNodes is "me" plus every connected peer the dashboard can name.
func knownNodes(v view) map[string]bool {
	known := map[string]bool{"me": true}
	for _, p := range connectedPeers(v) {
		known[p.ID] = true
	}
	return known
}

func addEdgePath(edges map[mapEdge]bool, known map[string]bool, path []string) {
	for i := 1; i < len(path); i++ {
		from, to := path[i-1], path[i]
		if from == to || !known[from] || !known[to] {
			continue
		}
		edges[mapEdge{from, to}] = true
	}
}

// routeEdges are paths this node can actually use (solid on the map).
func (d *dashboard) routeEdges(v view) map[mapEdge]bool {
	known := knownNodes(v)
	edges := map[mapEdge]bool{}
	for _, r := range v.routes {
		addEdgePath(edges, known, d.routePath(v, r))
	}
	return edges
}

// activityEdges are remote links reported by peers (dotted on the map). They
// do not imply this node can reach either endpoint.
func (d *dashboard) activityEdges(v view) map[mapEdge]bool {
	known := knownNodes(v)
	edges := map[mapEdge]bool{}
	for _, a := range freshActivitiesFrom(v.activities, time.Now().UnixMilli()) {
		addEdgePath(edges, known, d.activityPath(v, a))
	}
	return edges
}

// topologyEdges is the union, used where the source distinction does not
// matter.
func (d *dashboard) topologyEdges(v view) map[mapEdge]bool {
	edges := d.routeEdges(v)
	for e := range d.activityEdges(v) {
		edges[e] = true
	}
	return edges
}
