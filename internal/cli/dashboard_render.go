package cli

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// build is retained for snapshot callers; interactive drawing supplies height.
func (d *dashboard) build(v view, width int) []string { return d.buildSized(v, width, 999) }

func (d *dashboard) buildSized(v view, width, height int) []string {
	if width < 1 {
		return nil
	}
	l := resolveLayout(width, height)
	if l.mode == modeShort {
		return d.renderShort(v, width, height)
	}
	lines := []string{d.bar("SKEIN // MISSION CONTROL", width)}
	up, down := 0, 0
	for _, p := range v.peers {
		if p.State == "up" {
			up++
		} else {
			down++
		}
	}
	node := dash(v.st.ID)
	overlay := dash(v.st.OverlayIPv4)
	lines = append(lines, d.line(fmt.Sprintf(" %s  overlay %s  %d up / %d down  traffic %s", node, overlay, up, down, humanRate(totalRate(v.rates))), width))
	if v.err != nil {
		lines = append(lines, d.line(" ! disconnected from daemon · last updated "+age(v.lastFetch), width))
	}
	if v.recoverErr != nil {
		for _, msg := range strings.Split(v.recoverErr.Error(), "\n") {
			lines = append(lines, d.line(" ! "+msg, width))
		}
	}
	if v.ui.help {
		return append(lines, d.renderHelp(width)...)
	}
	switch l.mode {
	case modeCompact:
		lines = append(lines, d.line(" [Overview] [Mesh] [Peers] [Routes] [Events]  TAB switch · ? help", width))
		lines = append(lines, d.renderFocused(v, width, max(4, height-len(lines)-2))...)
	default:
		lines = append(lines, d.bar("MESH ROUTE MAP", width))
		lines = append(lines, d.renderRouteMap(v, width, min(11, max(6, height/3)))...)
		lines = append(lines, d.bar("PEERS", width))
		lines = append(lines, d.renderPeers(v, width, min(5, max(2, height/6)))...)
		lines = append(lines, d.bar("INSPECTOR", width))
		lines = append(lines, d.renderInspector(v, width)...)
		lines = append(lines, d.bar("ROUTES", width))
		lines = append(lines, d.renderRoutes(v, width, min(4, max(1, height/8)))...)
		lines = append(lines, d.bar("ACTIVITY", width))
		lines = append(lines, d.renderActivity(v, width, min(3, max(1, height/9)))...)
	}
	motion := "live"
	if v.ui.paused {
		motion = "paused"
	}
	lines = append(lines, d.line(fmt.Sprintf(" %s  focus %s  SPACE %s motion · ? help · q quit", sparkline(v.spark), focusName(v.ui.focus), motion), width))
	if len(lines) > height && height > 0 {
		return lines[:height]
	}
	return lines
}

func (d *dashboard) renderShort(v view, width, height int) []string {
	lines := []string{d.line(" SKEIN // MISSION CONTROL", width), d.line(" "+dash(v.st.OverlayIPv4)+" · "+dash(v.st.ID), width)}
	if v.err != nil {
		lines = append(lines, d.line(" ! daemon disconnected · "+age(v.lastFetch), width))
	} else {
		lines = append(lines, d.line(fmt.Sprintf(" %d peers · %s", len(v.peers), humanRate(totalRate(v.rates))), width))
	}
	lines = append(lines, d.line(" TAB views · j/k peers · ? help · q quit", width))
	if len(lines) > height {
		return lines[:height]
	}
	return lines
}

func (d *dashboard) renderFocused(v view, width, maxLines int) []string {
	switch v.ui.focus {
	case focusMesh:
		return append([]string{d.bar("MESH", width)}, d.renderRouteMap(v, width, maxLines-1)...)
	case focusPeers:
		return append([]string{d.bar("PEERS", width)}, d.renderPeers(v, width, maxLines-1)...)
	case focusRoutes:
		return append([]string{d.bar("ROUTES", width)}, d.renderRoutes(v, width, maxLines-1)...)
	case focusEvents:
		return append([]string{d.bar("ACTIVITY", width)}, d.renderActivity(v, width, maxLines-1)...)
	default:
		return append([]string{d.bar("OVERVIEW", width)}, d.renderInspector(v, width)...)
	}
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
	lines := make([]string, 0, min(limit, len(peers)))
	for i, p := range peers {
		if i >= limit {
			break
		}
		mark := " "
		if p.ID == v.ui.selected || (v.ui.selected == "" && i == 0) {
			mark = ">"
		}
		m := v.metrics[p.ID]
		rtt := "—"
		if m.rtt > 0 {
			rtt = fmt.Sprintf("%.1fms", m.rtt)
		}
		name := p.Name
		if name == "" {
			name = shortID(p.ID)
		}
		overlay := dash(p.OverlayIPv4)
		lines = append(lines, d.line(fmt.Sprintf("%s %-13s %-15s %-9s %5s ↓%-8s ↑%-8s", mark, trunc(name, 13), overlay, p.State, rtt, humanRate(m.rx), humanRate(m.tx)), width))
	}
	return lines
}

func (d *dashboard) renderInspector(v view, width int) []string {
	p, ok := selectedPeer(v)
	if !ok {
		return []string{d.line(" select a peer to inspect speed, latency, and path health", width)}
	}
	m := v.metrics[p.ID]
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
	latency := "unavailable"
	if m.rtt > 0 {
		latency = fmt.Sprintf("%.1fms (%.1f–%.1f, jitter %.1f)", m.rtt, m.minRTT, m.maxRTT, m.jitter)
	}
	return []string{
		d.line(fmt.Sprintf(" %s · %s · %s · %s", name, state, transport, dash(p.Endpoint)), width),
		d.line(fmt.Sprintf(" RX %s  TX %s  peak %s  RTT %s", humanRate(m.rx), humanRate(m.tx), humanRate(m.peak), latency), width),
		d.line(" last seen "+lastSeen(p.LastSeenMS)+" · ENTER select · ESC return", width),
	}
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
	lines := make([]string, 0, min(limit, len(v.routes)))
	for i, r := range v.routes {
		if i >= limit {
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
	now := time.Now().UnixMilli()
	lines := make([]string, 0, limit)
	for _, a := range v.activities {
		if len(lines) >= limit {
			break
		}
		if a.ExpiresMS <= now {
			continue
		}
		lines = append(lines, d.line(fmt.Sprintf(" %s → %s  ↓%s ↑%s  %.1fms", shortID(a.Origin), shortID(a.Destination), humanRate(float64(a.RxBytes)), humanRate(float64(a.TxBytes)), a.RTTMs), width))
	}
	if len(lines) == 0 {
		lines = append(lines, d.line(" quiet — no fresh aggregate mesh activity", width))
	}
	return lines
}

// renderRouteMap draws only edges substantiated by route paths.
func (d *dashboard) renderRouteMap(v view, width, height int) []string {
	c := newCanvas(width, height)
	cx, cy := width/2, height/2
	type point struct {
		x, y int
		id   string
		name string
	}
	points := map[string]point{"me": {cx, cy, "me", "me"}}
	peers := append([]peerDTO(nil), v.peers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	rx, ry := max(8, min(width/2-10, 32)), max(2, min(height/2-1, 4))
	for i, p := range peers {
		a := -math.Pi/2 + 2*math.Pi*float64(i)/float64(max(1, len(peers)))
		name := p.Name
		if name == "" {
			name = shortID(p.ID)
		}
		points[p.ID] = point{cx + int(math.Round(float64(rx)*math.Cos(a))), cy + int(math.Round(float64(ry)*math.Sin(a))), p.ID, trunc(name, 10)}
	}
	type edge struct{ from, to string }
	edges := map[edge]bool{}
	for _, r := range v.routes {
		path := r.Path
		if len(path) == 0 {
			path = []string{r.NextHop}
			if r.NextHop != r.Destination {
				path = append(path, r.Destination)
			}
		}
		prev := "me"
		for _, next := range path {
			if _, ok := points[next]; ok && prev != next {
				edges[edge{prev, next}] = true
				prev = next
			}
		}
	}
	nowMS := time.Now().UnixMilli()
	for _, a := range v.activities {
		if a.ExpiresMS <= nowMS {
			continue
		}
		path := append([]string{a.Origin}, a.Path...)
		if len(path) == 1 || path[len(path)-1] != a.Destination {
			path = append(path, a.Destination)
		}
		for i := 1; i < len(path); i++ {
			if _, ok := points[path[i-1]]; ok {
				if _, ok := points[path[i]]; ok {
					edges[edge{path[i-1], path[i]}] = true
				}
			}
		}
	}
	for e := range edges {
		a, b := points[e.from], points[e.to]
		c.line(a.x, a.y, b.x, b.y, '·', ansiDim)
		if d.edgeActive(v, e.from, e.to) {
			d.drawPacket(c, a.x, a.y, b.x, b.y, e.from+e.to, v)
		}
	}
	for id, p := range points {
		glyph, col := "●", ansiGreen
		if id == "me" {
			glyph, col = "◆", ansiCyan
		}
		c.text(p.x, p.y, glyph, col)
		labelX := p.x + 2
		if labelX+len([]rune(p.name)) > width {
			labelX = p.x - len([]rune(p.name)) - 1
		}
		c.text(max(0, labelX), p.y, p.name, ansiWhite)
	}
	if len(edges) == 0 {
		c.text(max(0, cx-10), cy+1, "waiting for routes", ansiDim)
	}
	return c.lines(d.color)
}

func (d *dashboard) edgeActive(v view, from, to string) bool {
	if !v.motion || v.ui.paused {
		return false
	}
	if from == "me" {
		r := v.rates[to]
		if r.rx+r.tx > 1 {
			return true
		}
	}
	now := time.Now().UnixMilli()
	for _, a := range v.activities {
		if a.ExpiresMS <= now || a.RxBytes+a.TxBytes == 0 {
			continue
		}
		path := append([]string{a.Origin}, a.Path...)
		if len(path) == 1 || path[len(path)-1] != a.Destination {
			path = append(path, a.Destination)
		}
		for i := 1; i < len(path); i++ {
			if (path[i-1] == from && path[i] == to) || (path[i-1] == to && path[i] == from) {
				return true
			}
		}
	}
	return false
}
func (d *dashboard) drawPacket(c *canvas, x0, y0, x1, y1 int, id string, v view) {
	p := math.Mod(v.phase*(1+float64(hashID(id)%3)/2)+float64(hashID(id)%100)/100, 1)
	x, y := lerp(x0, x1, p), lerp(y0, y1, p)
	c.set(x, y, '●', ansiCyan)
}
