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
	focusMesh
	focusPeers
	focusRoutes
	focusEvents
	focusHelp
)

type dashboardState struct {
	focus     dashboardFocus
	selected  string
	inspector bool
	paused    bool
	help      bool
	esc, csi  bool
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
		d.ui.inspector, d.ui.help = false, false
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
		if d.ui.selected != "" {
			d.ui.inspector = true
		}
	case ' ':
		d.ui.paused = !d.ui.paused
	case '?':
		d.ui.help = !d.ui.help
	}
	return false
}

func (d *dashboard) nextFocus(delta int) {
	const count = int(focusEvents) + 1
	d.ui.help = false
	d.ui.focus = dashboardFocus((int(d.ui.focus) + delta + count) % count)
}

func (d *dashboard) moveSelection(delta int) {
	peers := append([]peerDTO(nil), d.peers...)
	if len(peers) == 0 {
		d.ui.selected = ""
		return
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	i := 0
	for n, p := range peers {
		if p.ID == d.ui.selected {
			i = n
			break
		}
	}
	d.ui.selected = peers[(i+delta+len(peers))%len(peers)].ID
}

type dashboardRect struct{ x, y, w, h int }

func (r dashboardRect) right() int  { return r.x + r.w }
func (r dashboardRect) bottom() int { return r.y + r.h }

type dashboardLayout struct {
	mode  dashboardMode
	rects map[dashboardFocus]dashboardRect
}

func resolveLayout(width, height int) dashboardLayout {
	l := dashboardLayout{rects: map[dashboardFocus]dashboardRect{}}
	switch {
	case height < 14 || width < 46:
		l.mode = modeShort
	case width < 96:
		l.mode = modeCompact
	case width < 144:
		l.mode = modeStandard
	default:
		l.mode = modeWide
	}
	return l
}
