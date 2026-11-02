package cli

import (
	"fmt"
	"strings"
)

func padPanel(lines []string, r dashboardRect) []string {
	out := make([]string, r.h)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], r.w)
		} else {
			out[i] = strings.Repeat(" ", r.w)
		}
	}
	return out
}

func (d *dashboard) card(title string, body []string, r dashboardRect) []string {
	if r.w < 4 || r.h < 3 {
		return padPanel(body, r)
	}
	out := make([]string, 0, r.h)
	out = append(out, d.paint(ansiBorder, "╭")+d.paint(ansiBrand, fit(" "+title+" ", r.w-2))+d.paint(ansiBorder, "╮"))
	for i := 0; i < r.h-2; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		out = append(out, d.paint(ansiBorder, "│")+fit(line, r.w-2)+d.paint(ansiBorder, "│"))
	}
	out = append(out, d.paint(ansiBorder, "╰")+strings.Repeat("─", r.w-2)+d.paint(ansiBorder, "╯"))
	return out
}

func (d *dashboard) renderMetricStrip(v view, r dashboardRect) []string {
	up, down := 0, 0
	var rtt, jitter float64
	for _, p := range v.peers {
		if p.State == "up" {
			up++
		} else {
			down++
		}
	}
	if p, ok := selectedPeer(v); ok {
		m := v.metrics[p.ID]
		rtt, jitter = m.rtt, m.jitter
	}
	body := []string{
		" MESH " + d.paint(ansiHealthy, fmt.Sprintf("%d up", up)) + " / " + d.paint(ansiError, fmt.Sprintf("%d down", down)) + "   " + d.paint(ansiTraffic, fmt.Sprintf("↓ %s  ↑ %s", humanRate(totalRate(v.rates)), humanRate(totalRate(v.rates)))),
		" RTT " + d.paint(ansiBrand, fmtRTT(rtt)) + fmt.Sprintf("   JITTER %.1f ms", jitter),
	}
	if v.err != nil {
		recovery := ""
		if v.recoverErr != nil {
			recovery = " · " + strings.Split(v.recoverErr.Error(), "\n")[0]
		}
		body[1] = d.paint(ansiError, " ! disconnected from daemon · "+age(v.lastFetch)+recovery)
	}
	return d.card("LIVE MESH", body, r)
}

func fmtRTT(rtt float64) string {
	if rtt <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f ms", rtt)
}

func (d *dashboard) renderPeerCard(v view, r dashboardRect) []string {
	body := d.renderPeers(v, max(1, r.w-2), max(1, r.h-2))
	return d.card("PEERS", body, r)
}

func (d *dashboard) renderDetailCard(v view, r dashboardRect) []string {
	body := d.renderInspector(v, max(1, r.w-2))
	return d.card("SELECTED LINK", body, r)
}
