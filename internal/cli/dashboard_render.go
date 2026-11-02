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
	line := fmt.Sprintf(" Skein · %s · %s · %d up", dash(v.st.OverlayIPv4), shortID(v.st.ID), up)
	if down > 0 {
		line += fmt.Sprintf(" · %d down", down)
	}
	line += " · " + humanRate(totalRate(v.rates))
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
	return d.line(" "+strings.Join(parts, "  "), width)
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
	if wide {
		return d.line(fmt.Sprintf(" %s  focus %s · %s  SPACE %s motion · TAB panels · j/k select · ? help · q quit", sparkline(v.spark), focusName(v.ui.focus), pos, motion), width)
	}
	if width < 96 {
		return d.line(fmt.Sprintf(" %s · %s · TAB pages · j/k · SPACE %s · ? · q", focusName(v.ui.focus), pos, motion), width)
	}
	return d.line(fmt.Sprintf(" %s  focus %s · %s  SPACE %s motion · ? help · q quit", sparkline(v.spark), focusName(v.ui.focus), pos, motion), width)
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
		items := freshActivitiesFrom(v.activities, time.Now().UnixMilli())
		if len(items) == 0 {
			return "activity 0/0"
		}
		index := 0
		for n, a := range items {
			if activityKey(a) == v.ui.activitySel {
				index = n
			}
		}
		return fmt.Sprintf("activity %d/%d", index+1, len(items))
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
	return []string{"overview", "mesh", "peers", "routes", "activity", "help"}[f]
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
			fields := compactFields(name, state, transport, dash(p.OverlayIPv4), fmt.Sprintf("↓%s ↑%s · %s", humanRate(m.rx), humanRate(m.tx), rtt), width)
			fields[0] = mark + strings.TrimPrefix(fields[0], " ")
			for _, line := range fields {
				if len(lines) >= limit {
					break
				}
				lines = append(lines, line)
			}
			continue
		}
		overlay := dash(p.OverlayIPv4)
		lines = append(lines, d.line(fmt.Sprintf("%s %-13s %-15s %-9s %5s ↓%-8s ↑%-8s", mark, clip(name, 13), overlay, state, rtt, humanRate(m.rx), humanRate(m.tx)), width))
	}
	return lines
}

func (d *dashboard) renderInspector(v view, width int) []string {
	p, ok := selectedPeer(v)
	if !ok {
		return []string{d.line(" select a peer to inspect speed, latency, and path health", width)}
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
	state := p.State
	if p.Direct {
		state += " direct"
	} else {
		state += " relayed"
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
	if rtt > 0 {
		latency = fmt.Sprintf("%.1f ms (%.1f–%.1f, jitter %.1f)", rtt, minRTT, maxRTT, m.jitter)
	}
	return []string{
		d.line(fmt.Sprintf(" %s · %s · %s · %s", name, state, transport, dash(p.Endpoint)), width),
		d.line(fmt.Sprintf(" RX %s  TX %s  peak %s  RTT %s", humanRate(maxRate(m.rx, r.rx)), humanRate(maxRate(m.tx, r.tx)), humanRate(maxRate(m.peak, r.rx+r.tx)), latency), width),
		d.line(" last seen "+lastSeen(p.LastSeenMS)+" · ENTER select · ESC return", width),
	}
}

func maxRate(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func lastSeen(ms int64) string {
	if ms <= 0 {
		return "now"
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
		path := append([]string{"me"}, r.Path...)
		if len(r.Path) == 0 {
			path = []string{"me", shortID(r.NextHop), shortID(r.Destination)}
		}
		lines = append(lines, d.line(fmt.Sprintf(" %s  %d hop · %.1f ms", strings.Join(shortPath(path), " → "), r.Hops, r.CostMs), width))
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

func (d *dashboard) renderActivity(v view, width, limit int) []string {
	items := freshActivitiesFrom(v.activities, time.Now().UnixMilli())
	selectedIndex := 0
	for i, a := range items {
		if activityKey(a) == v.ui.activitySel {
			selectedIndex = i
		}
	}
	start := 0
	if selectedIndex >= limit {
		start = selectedIndex - limit + 1
	}
	lines := make([]string, 0, limit)
	for i := start; i < len(items); i++ {
		a := items[i]
		if len(lines) >= limit {
			break
		}
		origin := d.displayName(v, a.Origin)
		dest := d.displayName(v, a.Destination)
		arrow := ansiSlate
		switch {
		case origin == "ME":
			arrow = ansiOut
		case dest == "ME":
			arrow = ansiIn
		}
		mark := " "
		if activityKey(a) == v.ui.activitySel || (v.ui.activitySel == "" && len(lines) == 0) {
			mark = d.paint(ansiBrand, ">")
		}
		line := fmt.Sprintf("%s %s %s %s  ↓%s ↑%s  %.1fms", mark, origin, d.paint(arrow, "→"), dest, humanRate(float64(a.RxBytes)), humanRate(float64(a.TxBytes)), a.RTTMs)
		lines = append(lines, d.line(line, width))
	}
	if len(lines) == 0 {
		lines = append(lines, d.line(" quiet — no fresh aggregate mesh activity", width))
	}
	return lines
}

// displayName localizes a node ID for display: this node is ME, known peers
// use their name, and everything else falls back to a short ID.
func (d *dashboard) displayName(v view, id string) string {
	if id == "" {
		return "—"
	}
	if id == v.st.ID {
		return "ME"
	}
	for _, p := range v.peers {
		if p.ID == id {
			if p.Name != "" {
				return p.Name
			}
			return shortID(p.ID)
		}
	}
	return shortID(id)
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
	peers := append([]peerDTO(nil), v.peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
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
	points := map[string]point{"me": {cx, cy, "me", "ME"}}
	rx, ry := max(8, min(width/2-10, 32)), max(2, min(height/2-1, 4))
	for i, p := range peers {
		a := -math.Pi/2 + 2*math.Pi*float64(i)/float64(max(1, len(peers)))
		points[p.ID] = point{cx + int(math.Round(float64(rx)*math.Cos(a))), cy + int(math.Round(float64(ry)*math.Sin(a))), p.ID, trunc(d.displayName(v, p.ID), 10)}
	}

	type edge struct{ from, to string }
	edges := map[edge]bool{}
	routeEdges := map[edge]bool{}
	if v.ui.focus == focusRoutes {
		if r, ok := selectedRoute(v); ok {
			path := d.routePath(v, r)
			for i := 1; i < len(path); i++ {
				routeEdges[edge{path[i-1], path[i]}] = true
			}
		}
	}
	addPath := func(path []string, into map[edge]bool) {
		for i := 1; i < len(path); i++ {
			from, to := path[i-1], path[i]
			if from == to {
				continue
			}
			if _, ok := points[from]; !ok {
				continue
			}
			if _, ok := points[to]; !ok {
				continue
			}
			into[edge{from, to}] = true
		}
	}
	for _, r := range v.routes {
		addPath(d.routePath(v, r), edges)
	}
	for _, a := range freshActivitiesFrom(v.activities, time.Now().UnixMilli()) {
		addPath(d.activityPath(v, a), edges)
	}

	for e := range edges {
		a, b := points[e.from], points[e.to]
		color := ansiDim
		if routeEdges[e] || e.from == v.ui.selected || e.to == v.ui.selected {
			color = ansiBrand
		}
		c.line(a.x, a.y, b.x, b.y, '·', color)
		for _, pulse := range d.edgePulses(v, e.from, e.to) {
			d.drawPulse(c, a.x, a.y, b.x, b.y, e.from+e.to+pulse.color, pulse.color, pulse.reverse, v.phase)
		}
	}
	for id, p := range points {
		glyph, col := "●", ansiHealthy
		if id == "me" {
			glyph, col = "◆", ansiCyan
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
		if labelX+visibleWidth(p.name) > width {
			labelX = p.x - visibleWidth(p.name) - 1
		}
		c.text(max(0, labelX), p.y, p.name, ansiWhite)
	}
	if len(edges) == 0 {
		c.text(max(0, cx-10), cy+1, "waiting for routes", ansiDim)
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
	c.line(left+3, cy, laneEnd, cy, lineGlyph, lineColor)
	c.text(left, cy, "◆", ansiCyan)
	c.text(left+2, cy, "ME", ansiWhite)
	c.text(labelX, cy, nodeGlyph, stateColor(p.State))
	c.text(labelX+2, cy, name, ansiWhite)

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
			d.drawPulse(c, left+3, cy, laneEnd, cy, p.ID+"tx", ansiOut, false, v.phase)
		}
		if r.rx > 1 {
			d.drawPulse(c, laneEnd, cy, left+3, cy, p.ID+"rx", ansiIn, false, v.phase)
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
	p := math.Mod(phase*(0.8+float64(hashID(id)%3)/3)+float64(hashID(id)%100)/100, 1)
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

func selectedActivityFrom(items []activityDTO, key string) (activityDTO, bool) {
	if len(items) == 0 {
		return activityDTO{}, false
	}
	for _, a := range items {
		if activityKey(a) == key {
			return a, true
		}
	}
	return items[0], true
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

func selectedActivity(v view) (activityDTO, bool) {
	return selectedActivityFrom(freshActivitiesFrom(v.activities, time.Now().UnixMilli()), v.ui.activitySel)
}

func selectedRoute(v view) (routeDTO, bool) {
	return selectedRouteFrom(v.routes, v.ui.routeSel)
}
