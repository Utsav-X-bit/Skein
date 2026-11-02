package cli

import (
	"math"
	"sort"
	"time"
)

type dashboardMode uint8

const (
	modeShort dashboardMode = iota
	modeCompact
	modeStandard
	modeWide
)

type dashboardFocus uint8

const (
	focusOverview dashboardFocus = iota
	focusMap
	focusPeers
	focusRoutes
	focusActivity
	focusHelp
)

type dashboardState struct {
	focus       dashboardFocus
	prevFocus   dashboardFocus
	selected    string // peer ID
	routeSel    string // route destination
	activitySel string // origin|destination|path
	inspector   bool
	paused      bool
	help        bool
	esc, csi    bool
}

type metricSample struct {
	at          time.Time
	rx, tx, rtt float64
}
type linkMetrics struct {
	rx, tx, peak, rtt, minRTT, maxRTT, jitter float64
	samples                                   []metricSample
}

func (d *dashboard) observeMetric(id string, r rate, rtt float64, now time.Time) {
	m := d.metrics[id]
	if m == nil {
		m = &linkMetrics{}
		d.metrics[id] = m
	}
	m.rx, m.tx, m.rtt = r.rx, r.tx, rtt
	if r.rx+r.tx > m.peak {
		m.peak = r.rx + r.tx
	}
	if rtt > 0 {
		if m.minRTT == 0 || rtt < m.minRTT {
			m.minRTT = rtt
		}
		if rtt > m.maxRTT {
			m.maxRTT = rtt
		}
		if len(m.samples) > 0 && m.samples[len(m.samples)-1].rtt > 0 {
			m.jitter = math.Abs(rtt - m.samples[len(m.samples)-1].rtt)
		}
	}
	m.samples = append(m.samples, metricSample{now, r.rx, r.tx, rtt})
	cut := now.Add(-30 * time.Second)
	for len(m.samples) > 0 && m.samples[0].at.Before(cut) {
		m.samples = m.samples[1:]
	}
}

func cloneMetrics(src map[string]*linkMetrics) map[string]linkMetrics {
	out := make(map[string]linkMetrics, len(src))
	for id, m := range src {
		c := *m
		c.samples = append([]metricSample(nil), m.samples...)
		out[id] = c
	}
	return out
}

func (d *dashboard) handleKey(key byte) (quit bool) {
	if d.ui.esc {
		if key == '[' {
			d.ui.csi, d.ui.esc = true, false
			return false
		}
		d.ui.esc = false
		d.closeOverlay()
		return false
	}
	if d.ui.csi {
		d.ui.csi = false
		switch key {
		case 'A':
			d.moveSelection(-1)
		case 'B':
			d.moveSelection(1)
		case 'C':
			d.nextFocus(1)
		case 'D':
			d.nextFocus(-1)
		}
		return false
	}
	switch key {
	case 'q', 3:
		return true
	case 27:
		d.ui.esc = true
	case '\t', 'l':
		d.nextFocus(1)
	case 'h':
		d.nextFocus(-1)
	case 'j':
		d.moveSelection(1)
	case 'k':
		d.moveSelection(-1)
	case '\r', '\n':
		d.openInspector()
	case ' ':
		d.ui.paused = !d.ui.paused
	case '?':
		d.ui.help = !d.ui.help
		d.ui.inspector = false
	}
	return false
}

// currentMode resolves the layout mode from the last drawn terminal size.
func (d *dashboard) currentMode() dashboardMode {
	width, height := d.width, d.height
	if width <= 0 || height <= 0 {
		width, height = 100, 34
	}
	return resolveLayout(width, height).mode
}

// focusOrder lists the panels reachable with TAB in the current mode.
func (d *dashboard) focusOrder() []dashboardFocus {
	all := []dashboardFocus{focusOverview, focusMap, focusPeers, focusRoutes, focusActivity}
	if d.currentMode() == modeStandard {
		// Standard shows the map, peers, and inspector; routes and activity
		// live inside the wide rail.
		return all[:3]
	}
	return all
}

func (d *dashboard) nextFocus(delta int) {
	d.ui.help, d.ui.inspector = false, false
	order := d.focusOrder()
	index := 0
	for i, f := range order {
		if f == d.ui.focus {
			index = i
			break
		}
	}
	d.ui.focus = order[(index+delta+len(order))%len(order)]
}

func (d *dashboard) closeOverlay() {
	if !d.ui.inspector && !d.ui.help {
		return
	}
	d.ui.inspector, d.ui.help = false, false
	for _, f := range d.focusOrder() {
		if f == d.ui.prevFocus {
			d.ui.focus = f
			return
		}
	}
}

// openInspector opens the selected peer's inspector from any peer-oriented
// panel and remembers where to return with ESC.
func (d *dashboard) openInspector() {
	if d.ui.focus == focusRoutes {
		if r, ok := selectedRouteFrom(d.routes, d.ui.routeSel); ok {
			d.ui.selected = r.Destination
		}
	}
	if d.ui.focus == focusActivity {
		if a, ok := selectedActivityFrom(freshActivitiesFrom(d.activities, time.Now().UnixMilli()), d.ui.activitySel); ok {
			if a.Origin != d.st.ID {
				d.ui.selected = a.Origin
			} else {
				d.ui.selected = a.Destination
			}
		}
	}
	if d.ui.selected == "" {
		return
	}
	if d.ui.focus != focusOverview {
		d.ui.prevFocus = d.ui.focus
	}
	d.ui.focus = focusOverview
	d.ui.inspector = true
}

func (d *dashboard) moveSelection(delta int) {
	switch d.ui.focus {
	case focusRoutes:
		d.moveRoute(delta)
	case focusActivity:
		d.moveActivity(delta)
	default:
		d.movePeer(delta)
	}
}

func (d *dashboard) movePeer(delta int) {
	peers := append([]peerDTO(nil), d.peers...)
	if len(peers) == 0 {
		d.ui.selected = ""
		return
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	i := 0
	found := false
	for n, p := range peers {
		if p.ID == d.ui.selected {
			i, found = n, true
			break
		}
	}
	if !found && delta > 0 {
		i = -1
	}
	d.ui.selected = peers[(i+delta+len(peers))%len(peers)].ID
}

func (d *dashboard) moveRoute(delta int) {
	routes := append([]routeDTO(nil), d.routes...)
	if len(routes) == 0 {
		d.ui.routeSel = ""
		return
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Destination < routes[j].Destination })
	i := 0
	found := false
	for n, r := range routes {
		if r.Destination == d.ui.routeSel {
			i, found = n, true
			break
		}
	}
	if !found && delta > 0 {
		i = -1
	}
	d.ui.routeSel = routes[(i+delta+len(routes))%len(routes)].Destination
}

func (d *dashboard) moveActivity(delta int) {
	items := freshActivitiesFrom(d.activities, time.Now().UnixMilli())
	if len(items) == 0 {
		d.ui.activitySel = ""
		return
	}
	i := 0
	found := false
	for n, a := range items {
		if activityKey(a) == d.ui.activitySel {
			i, found = n, true
			break
		}
	}
	if !found && delta > 0 {
		i = -1
	}
	d.ui.activitySel = activityKey(items[(i+delta+len(items))%len(items)])
}

type dashboardRect struct{ x, y, w, h int }

func (r dashboardRect) right() int  { return r.x + r.w }
func (r dashboardRect) bottom() int { return r.y + r.h }

type dashboardLayout struct {
	mode                       dashboardMode
	brand, metrics, tabs, body dashboardRect
	footer                     dashboardRect
	rects                      map[dashboardFocus]dashboardRect
}

func resolveLayout(width, height int) dashboardLayout {
	l := dashboardLayout{rects: map[dashboardFocus]dashboardRect{}}
	switch {
	case width < 64 || height < 16:
		l.mode = modeShort
		l.brand = dashboardRect{w: width, h: min(1, height)}
		l.footer = dashboardRect{y: max(0, height-1), w: width, h: 1}
		l.body = dashboardRect{y: l.brand.h, w: width, h: max(0, height-l.brand.h-l.footer.h)}
	case width < 96 || height < 20:
		l.mode = modeCompact
		l.brand = dashboardRect{w: width, h: min(1, height)}
		l.tabs = dashboardRect{y: l.brand.h, w: width, h: min(1, max(0, height-l.brand.h))}
		l.footer = dashboardRect{y: max(0, height-1), w: width, h: 1}
		bodyY := l.brand.h + l.tabs.h
		l.body = dashboardRect{y: bodyY, w: width, h: max(0, height-bodyY-l.footer.h)}
		for _, f := range []dashboardFocus{focusOverview, focusMap, focusPeers, focusRoutes, focusActivity} {
			l.rects[f] = l.body
		}
	case width < 144 || height < 24:
		l.mode = modeStandard
		l.brand = dashboardRect{w: width, h: min(3, height)}
		l.metrics = dashboardRect{y: l.brand.h, w: width, h: min(4, max(0, height-l.brand.h-1))}
		l.footer = dashboardRect{y: max(0, height-1), w: width, h: 1}
		contentY := l.brand.h + l.metrics.h
		contentH := max(0, height-contentY-l.footer.h)
		mapH := max(6, contentH*2/3)
		if mapH > contentH {
			mapH = contentH
		}
		l.rects[focusMap] = dashboardRect{x: 0, y: contentY, w: width, h: mapH}
		lowerH := contentH - mapH
		left := width/2 - 1
		l.rects[focusPeers] = dashboardRect{x: 0, y: contentY + mapH, w: left, h: lowerH}
		l.rects[focusOverview] = dashboardRect{x: left + 2, y: contentY + mapH, w: width - left - 2, h: lowerH}
	default:
		l.mode = modeWide
		l.brand = dashboardRect{w: width, h: min(3, height)}
		l.metrics = dashboardRect{y: l.brand.h, w: width, h: min(4, max(0, height-l.brand.h-1))}
		l.footer = dashboardRect{y: max(0, height-1), w: width, h: 1}
		contentY := l.brand.h + l.metrics.h
		contentH := max(0, height-contentY-l.footer.h)
		left, right, gap := 30, 36, 2
		center := width - left - right - 2*gap
		l.rects[focusPeers] = dashboardRect{x: 0, y: contentY, w: left, h: contentH}
		l.rects[focusMap] = dashboardRect{x: left + gap, y: contentY, w: center, h: contentH}
		l.rects[focusOverview] = dashboardRect{x: left + gap + center + gap, y: contentY, w: right, h: contentH}
	}
	return l
}
