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
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/Utsav-X-bit/skein/internal/conf"
)

// Palette. Terminals render these from their own 16-colour scheme, so the
// dashboard inherits the user's theme instead of fighting it.
const (
	ansiReset   = "\x1b[0m"
	ansiDim     = "2"
	ansiGreen   = "32"
	ansiRed     = "31"
	ansiYellow  = "33"
	ansiCyan    = "1;36"
	ansiBlue    = "34"
	ansiWhite   = "1;37"
	ansiBrand   = "38;2;94;234;212"
	ansiTraffic = "38;2;96;165;250"
	ansiOut     = "38;2;34;211;238" // cyan: local outbound traffic
	ansiIn      = "38;2;96;165;250" // blue-violet: inbound traffic
	ansiHealthy = "38;2;74;222;128"
	ansiWarn    = "38;2;251;191;36"
	ansiError   = "38;2;248;113;113"
	ansiSlate   = "38;2;100;116;139"
	ansiBorder  = "38;2;51;65;85"
)

const (
	// frameInterval is 25 fps: a packet crossing a long lane must not jump
	// several cells per frame.
	frameInterval   = 40 * time.Millisecond
	pollInterval    = time.Second
	revealWindow    = 700 * time.Millisecond
	inspectorReveal = 220 * time.Millisecond
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
	client  *client
	out     io.Writer
	color   bool
	motion  bool
	unicode bool

	mu         sync.Mutex
	prev       map[string]sample
	targets    map[string]rate
	disp       map[string]displayPeer
	metrics    map[string]*linkMetrics
	ui         dashboardState
	activities []activityDTO
	events     []string
	spark      []float64

	st        statusDTO
	peers     []peerDTO
	routes    []routeDTO
	lastFetch time.Time
	fetchErr  error

	frame     uint64
	started   time.Time
	phase     float64
	width     int
	height    int
	presenter framePresenter

	configPath  string
	recovering  bool
	lastRecover time.Time
	recoverErr  error
	stopping    bool

	child     *exec.Cmd
	childDone chan error
}

func newDashboard(cfg conf.Config, out io.Writer, color, motion bool) *dashboard {
	uiUnicode = color && os.Getenv("SKEIN_ASCII") == ""
	return &dashboard{
		client: &client{
			base:  "http://" + cfg.API.Listen,
			token: base64.RawURLEncoding.EncodeToString(cfg.API.Token),
			http:  &http.Client{Timeout: 3 * time.Second},
		},
		out:     out,
		color:   color,
		motion:  motion,
		unicode: color && os.Getenv("SKEIN_ASCII") == "",
		prev:    make(map[string]sample),
		targets: make(map[string]rate),
		disp:    make(map[string]displayPeer),
		metrics: make(map[string]*linkMetrics),
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

	keys := make(chan byte, 8)
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
			d.mu.Lock()
			quit := d.handleKey(key)
			d.mu.Unlock()
			if quit {
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
	var activities []activityDTO
	if err := d.client.get(ctx, "/api/v1/activities", &activities); err != nil {
		// The live activity feed enriches the mesh map, but it is not part of
		// the daemon liveness contract. Keep a usable snapshot when attached
		// to a daemon from before this endpoint existed.
		activities = nil
	}

	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.st, d.peers, d.routes = st, peers, routes
	d.activities = activities
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
		d.observeMetric(p.ID, r, p.RTTMs, now)
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
	if width < 1 {
		width = 1
	}

	d.mu.Lock()
	d.width, d.height = width, height
	if d.motion {
		d.ease()
	}
	view := d.viewLocked()
	d.mu.Unlock()

	lines := d.buildSized(view, width, height)
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
	lines = clampLines(lines, height)
	if out := d.presenter.render(lines, width, height); out != "" {
		_, _ = io.WriteString(d.out, out)
	}
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
		activities: append([]activityDTO(nil), d.activities...), metrics: cloneMetrics(d.metrics), ui: d.ui,
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
	activities []activityDTO
	metrics    map[string]linkMetrics
	ui         dashboardState
}

func hashID(id string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619
	}
	return h
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
