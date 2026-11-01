package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

// Palette. Terminals render these from their own 16-colour scheme, so the
// dashboard inherits the user's theme instead of fighting it.
const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "2"
	ansiGreen  = "32"
	ansiRed    = "31"
	ansiYellow = "33"
	ansiCyan   = "1;36"
	ansiBlue   = "34"
	ansiWhite  = "1;37"
)

const (
	// frameInterval is 12.5 fps: enough for smooth motion in a terminal,
	// cheap enough to leave the CPU alone.
	frameInterval = 80 * time.Millisecond
	pollInterval  = time.Second
	revealWindow  = 700 * time.Millisecond
	meshHeight    = 9
)

type rate struct {
	rx, tx float64
}

type sample struct {
	rx, tx uint64
	at     time.Time
}

type displayPeer struct {
	rtt, rx, tx float64
}

type dashboard struct {
	client *client
	out    io.Writer
	color  bool
	motion bool

	mu      sync.Mutex
	prev    map[string]sample
	targets map[string]rate
	disp    map[string]displayPeer
	events  []string
	spark   []float64

	st        statusDTO
	peers     []peerDTO
	routes    []routeDTO
	lastFetch time.Time
	fetchErr  error

	frame   uint64
	started time.Time
	phase   float64

	configPath  string
	recovering  bool
	lastRecover time.Time
	recoverErr  error
	stopping    bool

	child     *exec.Cmd
	childDone chan error
}

func newDashboard(cfg conf.Config, out io.Writer, color, motion bool) *dashboard {
	return &dashboard{
		client: &client{
			base:  "http://" + cfg.API.Listen,
			token: base64.RawURLEncoding.EncodeToString(cfg.API.Token),
			http:  &http.Client{Timeout: 3 * time.Second},
		},
		out:     out,
		color:   color,
		motion:  motion,
		prev:    make(map[string]sample),
		targets: make(map[string]rate),
		disp:    make(map[string]displayPeer),
		started: time.Now(),
	}
}

func (d *dashboard) paint(code, s string) string {
	if !d.color || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + ansiReset
}

func (d *dashboard) run(ctx context.Context) error {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		d.motion = false
	}
	if !d.motion {
		// Reduced motion: one static snapshot, refreshed on a slow tick.
		enableVT()
		if err := d.poll(ctx); err != nil {
			return err
		}
		for _, line := range d.build(d.view(), 100) {
			fmt.Fprintln(d.out, stripANSI(line))
		}
		return nil
	}

	enableVT()
	fd := int(os.Stdin.Fd())
	var restore func()
	if term.IsTerminal(fd) {
		if state, err := term.MakeRaw(fd); err == nil {
			restore = func() { _ = term.Restore(fd, state) }
			defer restore()
		}
	}
	fmt.Fprint(d.out, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(d.out, "\x1b[?25h\x1b[?1049l")

	_ = d.poll(ctx)
	go d.eventLoop(ctx)

	keys := make(chan byte, 1)
	if restore != nil {
		go func() {
			buf := make([]byte, 1)
			for {
				if _, err := os.Stdin.Read(buf); err != nil {
					return
				}
				keys <- buf[0]
			}
		}()
	}

	frames := time.NewTicker(frameInterval)
	defer frames.Stop()
	polls := time.NewTicker(pollInterval)
	defer polls.Stop()

	for {
		d.draw()
		select {
		case <-ctx.Done():
			return nil
		case key := <-keys:
			if key == 'q' || key == 3 {
				return nil
			}
		case <-polls.C:
			_ = d.poll(ctx)
		case <-frames.C:
			d.frame++
			d.phase += frameInterval.Seconds()
			d.maybeRecover(ctx)
		}
	}
}

func (d *dashboard) eventLoop(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.client.base+"/api/v1/events", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+d.client.token)
	stream := &http.Client{}
	resp, err := stream.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Type   string `json:"type"`
			PeerID string `json:"peer_id"`
			TimeMS int64  `json:"time_ms"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			continue
		}
		at := time.UnixMilli(ev.TimeMS).Format("15:04:05")
		entry := fmt.Sprintf("%s  %-16s %s", at, ev.Type, shortID(ev.PeerID))
		d.mu.Lock()
		d.events = append(d.events, entry)
		if len(d.events) > 6 {
			d.events = d.events[len(d.events)-6:]
		}
		d.mu.Unlock()
	}
}

func shortID(id string) string {
	return trunc(id, 12)
}

func trunc(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

// poll refreshes the snapshot and computes per-peer target rates.
func (d *dashboard) poll(ctx context.Context) error {
	var st statusDTO
	if err := d.client.get(ctx, "/api/v1/status", &st); err != nil {
		d.mu.Lock()
		d.fetchErr = err
		d.mu.Unlock()
		return err
	}
	var peers []peerDTO
	if err := d.client.get(ctx, "/api/v1/peers", &peers); err != nil {
		d.mu.Lock()
		d.fetchErr = err
		d.mu.Unlock()
		return err
	}
	var routes []routeDTO
	if err := d.client.get(ctx, "/api/v1/routes", &routes); err != nil {
		d.mu.Lock()
		d.fetchErr = err
		d.mu.Unlock()
		return err
	}

	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st, d.peers, d.routes = st, peers, routes
	d.lastFetch = now
	d.fetchErr = nil

	var total float64
	next := make(map[string]sample, len(peers))
	for _, p := range peers {
		prev, ok := d.prev[p.ID]
		r := rate{}
		if ok && now.After(prev.at) {
			dt := now.Sub(prev.at).Seconds()
			if dt > 0 {
				r.rx = math.Max(0, float64(p.RxBytes-prev.rx)/dt)
				r.tx = math.Max(0, float64(p.TxBytes-prev.tx)/dt)
			}
		}
		d.targets[p.ID] = r
		total += r.rx + r.tx
		next[p.ID] = sample{rx: p.RxBytes, tx: p.TxBytes, at: now}
	}
	d.prev = next
	d.spark = append(d.spark, total)
	if len(d.spark) > 48 {
		d.spark = d.spark[len(d.spark)-48:]
	}
	return nil
}

// ease moves displayed values toward their targets. A fixed fraction per
// frame gives a quick start and a gentle settle, the same shape as an
// ease-out curve.
func (d *dashboard) ease() {
	const k = 0.22
	for id, target := range d.targets {
		cur := d.disp[id]
		cur.rx += (target.rx - cur.rx) * k
		cur.tx += (target.tx - cur.tx) * k
		d.disp[id] = cur
	}
	for _, p := range d.peers {
		cur := d.disp[p.ID]
		if p.RTTMs > 0 {
			if cur.rtt == 0 {
				cur.rtt = p.RTTMs
			}
			cur.rtt += (p.RTTMs - cur.rtt) * k
		} else {
			cur.rtt = 0
		}
		d.disp[p.ID] = cur
	}
}

// maybeRecover restarts a daemon that went away while the dashboard is open.
func (d *dashboard) maybeRecover(ctx context.Context) {
	d.mu.Lock()
	failed := d.fetchErr != nil
	last := d.lastRecover
	busy := d.recovering
	d.mu.Unlock()
	if !failed || busy || time.Since(last) < 5*time.Second {
		return
	}
	d.mu.Lock()
	d.recovering = true
	d.lastRecover = time.Now()
	path := d.configPath
	d.mu.Unlock()
	go func() {
		err := d.ensureDaemon(ctx, path)
		d.mu.Lock()
		d.recovering = false
		d.recoverErr = err
		d.mu.Unlock()
	}()
}

func (d *dashboard) draw() {
	width, height := 100, 34
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		width, height = w, h
	}
	if width < 64 {
		width = 64
	}
	if width > 140 {
		width = 140
	}

	d.mu.Lock()
	if d.motion {
		d.ease()
	}
	view := d.viewLocked()
	d.mu.Unlock()

	lines := d.build(view, width)
	if d.motion {
		// Entrance: reveal top-to-bottom over ~700 ms with an ease-out curve.
		t := float64(time.Since(d.started)) / float64(revealWindow)
		if t < 1 {
			p := 1 - math.Pow(1-t, 3)
			shown := int(p * float64(len(lines)))
			if shown < 1 {
				shown = 1
			}
			lines = lines[:shown]
		}
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	var b strings.Builder
	b.WriteString("\x1b[H")
	for _, line := range lines {
		b.WriteString(line)
		b.WriteString("\x1b[K\r\n")
	}
	b.WriteString("\x1b[J")
	_, _ = io.WriteString(d.out, b.String())
}

func (d *dashboard) view() view {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.viewLocked()
}

func (d *dashboard) viewLocked() view {
	rates := make(map[string]rate, len(d.disp))
	rtts := make(map[string]float64, len(d.disp))
	for id, v := range d.disp {
		rates[id] = rate{rx: v.rx, tx: v.tx}
		rtts[id] = v.rtt
	}
	return view{
		st: d.st, peers: append([]peerDTO(nil), d.peers...), routes: append([]routeDTO(nil), d.routes...),
		rates: rates, rtts: rtts, spark: append([]float64(nil), d.spark...),
		events: append([]string(nil), d.events...), frame: d.frame, phase: d.phase,
		motion: d.motion, err: d.fetchErr, lastFetch: d.lastFetch,
		recoverErr: d.recoverErr,
	}
}

type view struct {
	st         statusDTO
	peers      []peerDTO
	routes     []routeDTO
	rates      map[string]rate
	rtts       map[string]float64
	spark      []float64
	events     []string
	frame      uint64
	phase      float64
	motion     bool
	err        error
	recoverErr error
	lastFetch  time.Time
}

func (d *dashboard) build(v view, width int) []string {
	inner := width - 4
	lines := make([]string, 0, 40)

	up, down := 0, 0
	for _, p := range v.peers {
		if p.State == "up" {
			up++
		} else {
			down++
		}
	}

	section := func(title string, first bool) string {
		label := "─ " + title + " "
		fill := width - 2 - len([]rune(label))
		if fill < 0 {
			fill = 0
		}
		left, right := "├", "┤"
		if first {
			left, right = "╭", "╮"
		}
		return d.paint(ansiCyan, left+label+strings.Repeat("─", fill)+right)
	}
	bottom := d.paint(ansiCyan, "╰"+strings.Repeat("─", width-2)+"╯")
	row := func(content string) string {
		content = fit(content, inner)
		return d.paint(ansiCyan, "│") + " " + content + " " + d.paint(ansiCyan, "│")
	}

	// Header with a breathing status diamond.
	beat := "◆"
	if v.motion && (v.frame/10)%2 == 0 {
		beat = "◇"
	}
	lines = append(lines, section("SKEIN", true))
	nodeID := v.st.ID
	if nodeID == "" {
		nodeID = "—"
	}
	overlay := v.st.OverlayIPv4
	if overlay == "" {
		overlay = "—"
	} else if v.st.OverlayIPv6 != "" {
		overlay += "  " + v.st.OverlayIPv6
	}
	version := v.st.Version
	if version == "" {
		version = "—"
	}
	listen := "—"
	if v.st.ListenPort != 0 {
		listen = fmt.Sprintf(":%d", v.st.ListenPort)
	}
	lines = append(lines, row(fmt.Sprintf("%s %s   %s %s   %s %s",
		d.paint(ansiCyan, beat), nodeID,
		d.paint(ansiDim, "overlay"), overlay,
		d.paint(ansiDim, "version"), version)))
	lines = append(lines, row(fmt.Sprintf("%s %s %s   %s %s   %s %s   %s %s",
		d.paint(ansiDim, "listen"), listen, d.paint(ansiDim, "udp+tcp"),
		d.paint(ansiDim, "uptime"), humanDuration(time.Duration(v.st.UptimeSeconds)*time.Second),
		d.paint(ansiDim, "peers"), fmt.Sprintf("%s %s", d.paint(ansiGreen, fmt.Sprintf("%d up", up)), d.paint(ansiDim, fmt.Sprintf("%d down", down))),
		d.paint(ansiDim, "traffic"), humanRate(totalRate(v.rates)))))

	if v.err != nil {
		lines = append(lines, row(d.paint(ansiRed, "disconnected from daemon: "+v.err.Error())))
	}
	if v.recoverErr != nil {
		for i, line := range strings.Split(v.recoverErr.Error(), "\n") {
			if i >= 4 {
				break
			}
			lines = append(lines, row(d.paint(ansiYellow, line)))
		}
	}

	// Mesh topology.
	lines = append(lines, section("MESH", false))
	for _, meshLine := range d.renderMesh(v, inner, meshHeight) {
		lines = append(lines, row(meshLine))
	}

	// Peers.
	lines = append(lines, section("PEERS", false))
	if len(v.peers) == 0 {
		lines = append(lines, row(d.paint(ansiDim, "no peers yet — share `skein invite --network` and have the other node run `skein join`")))
	} else {
		peers := append([]peerDTO(nil), v.peers...)
		sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
		header := fmt.Sprintf("%-2s %-14s %-10s %-15s %-6s %-4s %-8s %-9s %-9s %s",
			"", "NAME", "ID", "OVERLAY", "STATE", "NET", "RTT", "DOWN", "UP", "ENDPOINT")
		lines = append(lines, row(d.paint(ansiDim, header)))
		for _, p := range peers {
			r := v.rates[p.ID]
			active := r.rx+r.tx > 1
			lines = append(lines, row(d.peerRow(v, p, r, active)))
		}
	}

	// Routes.
	if len(v.routes) > 0 {
		lines = append(lines, section("ROUTES", false))
		for _, r := range v.routes {
			lines = append(lines, row(fmt.Sprintf("%-16s %s %-16s %s %-2d %s %s",
				shortID(r.Destination), d.paint(ansiDim, "via"), shortID(r.NextHop),
				d.paint(ansiDim, "hops"), r.Hops,
				d.paint(ansiDim, "cost"), fmt.Sprintf("%.1f ms", r.CostMs))))
		}
	}

	// Activity.
	lines = append(lines, section("ACTIVITY", false))
	lines = append(lines, row(fmt.Sprintf("%s  %s",
		d.paint(ansiCyan, sparkline(v.spark)),
		d.paint(ansiDim, fmt.Sprintf("now %s   peak %s", humanRate(totalRate(v.rates)), humanRate(peak(v.spark)))))))

	if len(v.events) > 0 {
		lines = append(lines, section("EVENTS", false))
		for _, ev := range v.events {
			lines = append(lines, row(d.paint(ansiDim, ev)))
		}
	}

	lines = append(lines, bottom)
	return lines
}

func (d *dashboard) peerRow(v view, p peerDTO, r rate, active bool) string {
	dot := d.paint(ansiRed, "○")
	switch {
	case p.State == "up" && active:
		glyph := "●"
		if v.motion && (v.frame/4)%2 == 0 {
			glyph = "◉"
		}
		dot = d.paint(ansiGreen, glyph)
	case p.State == "up":
		dot = d.paint(ansiBlue, "◍")
	case p.State == "degraded":
		dot = d.paint(ansiYellow, "◐")
	}
	state := p.State
	switch p.State {
	case "up":
		state = d.paint(ansiGreen, "up")
	case "down":
		state = d.paint(ansiRed, "down")
	case "degraded":
		state = d.paint(ansiYellow, "degraded")
	}
	net := p.Transport
	if net == "" {
		net = p.TransportRx
	}
	if p.TransportRx != "" && p.TransportRx != p.Transport && p.Transport != "" {
		net = p.Transport + "→" + p.TransportRx
	}
	if net == "" {
		net = "—"
	}
	rtt := "—"
	if v.rtts[p.ID] > 0 {
		rtt = fmt.Sprintf("%.1f ms", v.rtts[p.ID])
	}
	overlay := p.OverlayIPv4
	if overlay == "" {
		overlay = "—"
	}
	name := p.Name
	if name == "" {
		name = "—"
	}
	return fmt.Sprintf("%-2s %-14s %-10s %-15s %-6s %-4s %-8s %-9s %-9s %s",
		dot, fit(name, 14), trunc(p.ID, 10), overlay, state, net, rtt,
		humanRate(r.rx), humanRate(r.tx), dash(p.Endpoint))
}

// renderMesh draws the local node and its peers on a small canvas. Links carry
// a pulse that travels outward; the pulse speeds up with real traffic.
func (d *dashboard) renderMesh(v view, width, height int) []string {
	c := newCanvas(width, height)
	cx, cy := width/2, height/2

	type node struct {
		x, y  int
		peer  peerDTO
		up    bool
		label string
	}
	nodes := make([]node, 0, len(v.peers))
	upPeers := make([]peerDTO, 0, len(v.peers))
	for _, p := range v.peers {
		if p.State == "up" {
			upPeers = append(upPeers, p)
		}
	}
	all := append([]peerDTO(nil), upPeers...)
	for _, p := range v.peers {
		if p.State != "up" {
			all = append(all, p)
		}
	}
	rx := width/2 - 12
	if rx > 34 {
		rx = 34
	}
	ry := height/2 - 1
	if ry > 3 {
		ry = 3
	}
	for i, p := range all {
		angle := -math.Pi/2 + 2*math.Pi*float64(i)/float64(len(all))
		x := cx + int(math.Round(float64(rx)*math.Cos(angle)))
		y := cy + int(math.Round(float64(ry)*math.Sin(angle)))
		label := p.Name
		if label == "" {
			label = shortID(p.ID)[:6]
		}
		if len([]rune(label)) > 12 {
			label = string([]rune(label)[:12])
		}
		if p.OverlayIPv4 != "" {
			if ip := lastOctet(p.OverlayIPv4); ip != "" {
				label += " ·" + ip
			}
		}
		nodes = append(nodes, node{x: x, y: y, peer: p, up: p.State == "up", label: label})
	}

	// Self.
	c.text(cx-2, cy, "◆", ansiCyan)
	c.text(cx, cy, "me", ansiCyan)

	positions := make(map[string]node, len(nodes))
	for _, n := range nodes {
		positions[n.peer.ID] = n
	}
	nextHop := make(map[string]string, len(v.routes))
	for _, r := range v.routes {
		nextHop[r.Destination] = r.NextHop
	}

	for _, n := range nodes {
		dim := ansiDim
		if !n.up {
			dim = ansiDim
		}
		// Draw self -> first hop, then first hop -> peer for relayed paths.
		hop := nextHop[n.peer.ID]
		if hop != "" && hop != n.peer.ID {
			if relay, ok := positions[hop]; ok {
				c.line(cx, cy, relay.x, relay.y, '·', dim)
				c.line(relay.x, relay.y, n.x, n.y, '·', dim)
				if n.up {
					d.pulse(c, relay.x, relay.y, n.x, n.y, n.peer.ID, v)
				}
				continue
			}
		}
		c.line(cx, cy, n.x, n.y, '·', dim)
		if n.up {
			d.pulse(c, cx, cy, n.x, n.y, n.peer.ID, v)
		}
	}

	for _, n := range nodes {
		glyph := "○"
		color := ansiRed
		if n.up {
			glyph = "●"
			color = ansiGreen
			if v.motion && (v.frame/6)%2 == 0 {
				glyph = "◉"
			}
		}
		c.text(n.x, n.y, glyph, color)
		lx := n.x + 2
		if lx+len([]rune(n.label)) > c.w {
			lx = n.x - len([]rune(n.label)) - 1
		}
		if lx < 0 {
			lx = 0
		}
		c.text(lx, n.y, n.label, dimFor(n.up))
	}

	if len(nodes) == 0 {
		msg := "waiting for peers"
		dots := "."
		if v.motion {
			dots = strings.Repeat(".", int(v.phase)%4)
		}
		c.text(cx-len(msg)/2, cy+2, msg+dots, ansiDim)
	}

	return c.lines(d.color)
}

func dimFor(up bool) string {
	if up {
		return ansiWhite
	}
	return ansiDim
}

func (d *dashboard) pulse(c *canvas, x0, y0, x1, y1 int, id string, v view) {
	speed := 0.25
	if r, ok := v.rates[id]; ok && r.rx+r.tx > 1 {
		speed = 1.1
	}
	if !v.motion {
		return
	}
	// Stable per-peer offset so pulses do not march in lockstep.
	offset := float64(hashID(id)%1000) / 1000
	p := math.Mod(v.phase*speed+offset, 1)
	px, py := lerp(x0, x1, p), lerp(y0, y1, p)
	c.set(px, py, '●', ansiCyan)
	for _, trail := range []struct {
		back float64
		col  string
	}{{0.05, ansiBlue}, {0.10, ansiDim}} {
		tp := math.Mod(p-trail.back+1, 1)
		tx, ty := lerp(x0, x1, tp), lerp(y0, y1, tp)
		if c.at(tx, ty) == '·' {
			c.set(tx, ty, '·', trail.col)
		}
	}
}

func hashID(id string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619
	}
	return h
}

func lastOctet(ip string) string {
	if i := strings.LastIndexByte(ip, '.'); i >= 0 {
		return ip[i+1:]
	}
	if i := strings.LastIndexByte(ip, ':'); i >= 0 {
		return ip[i+1:]
	}
	return ""
}

func lerp(a, b int, t float64) int {
	return a + int(math.Round(float64(b-a)*t))
}

// canvas is a small character grid with a colour per cell.
type canvas struct {
	w, h  int
	cells []cell
}

type cell struct {
	r     rune
	color string
}

func newCanvas(w, h int) *canvas {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	c := &canvas{w: w, h: h, cells: make([]cell, w*h)}
	for i := range c.cells {
		c.cells[i] = cell{r: ' '}
	}
	return c
}

func (c *canvas) at(x, y int) rune {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return 0
	}
	return c.cells[y*c.w+x].r
}

func (c *canvas) set(x, y int, r rune, color string) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	c.cells[y*c.w+x] = cell{r: r, color: color}
}

func (c *canvas) text(x, y int, s string, color string) {
	for _, r := range s {
		c.set(x, y, r, color)
		x++
	}
}

func (c *canvas) line(x0, y0, x1, y1 int, r rune, color string) {
	dx := int(math.Abs(float64(x1 - x0)))
	dy := -int(math.Abs(float64(y1 - y0)))
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		if c.at(x0, y0) == ' ' {
			c.set(x0, y0, r, color)
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func (c *canvas) lines(color bool) []string {
	out := make([]string, c.h)
	for y := 0; y < c.h; y++ {
		var b strings.Builder
		cur := ""
		for x := 0; x < c.w; x++ {
			cl := c.cells[y*c.w+x]
			code := ""
			if color {
				code = cl.color
			}
			if code != cur {
				if cur != "" {
					b.WriteString(ansiReset)
				}
				if code != "" {
					b.WriteString("\x1b[" + code + "m")
				}
				cur = code
			}
			b.WriteRune(cl.r)
		}
		if cur != "" {
			b.WriteString(ansiReset)
		}
		out[y] = b.String()
	}
	return out
}

func totalRate(rates map[string]rate) float64 {
	var total float64
	for _, r := range rates {
		total += r.rx + r.tx
	}
	return total
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func fit(s string, n int) string {
	visible := stripANSI(s)
	runes := []rune(visible)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s + strings.Repeat(" ", n-len(runes))
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func humanDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%02dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%02dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm%02ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

func humanRate(v float64) string {
	switch {
	case v <= 0:
		return "—"
	case v < 1024:
		return fmt.Sprintf("%.0f B/s", v)
	case v < 1024*1024:
		return fmt.Sprintf("%.1f kB/s", v/1024)
	default:
		return fmt.Sprintf("%.1f MB/s", v/(1024*1024))
	}
}

func peak(values []float64) float64 {
	var max float64
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	return max
}

func sparkline(values []float64) string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	if len(values) == 0 {
		return "▁"
	}
	max := peak(values)
	if max <= 0 {
		max = 1
	}
	var b strings.Builder
	for _, v := range values {
		idx := int(v / max * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		b.WriteRune(blocks[idx])
	}
	return b.String()
}
