package daemon

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Utsav-X-bit/skein/internal/activity"
	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/conf"
)

// logRingSize bounds the in-memory log tail served by GET /logs and the
// diagnostics bundle.
const logRingSize = 1024

// logRing is a bounded ring of redacted daemon log entries with a monotonic
// sequence used as the ?since=&next= pagination cursor.
type logRing struct {
	mu    sync.Mutex
	next  uint64
	slots [logRingSize]api.LogEntry
}

func (r *logRing) add(level, message string) {
	entry := api.LogEntry{TimeMS: time.Now().UnixMilli(), Level: level, Message: message}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	r.slots[int((r.next-1)%logRingSize)] = entry
}

// tail returns up to limit entries with sequence after since, oldest first.
// next is the sequence of the last entry returned, ready to feed back as
// since.
func (r *logRing) tail(since uint64, limit int) ([]api.LogEntry, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > logRingSize {
		limit = logRingSize
	}
	retained := r.next
	if retained > logRingSize {
		retained = logRingSize
	}
	first := r.next - retained + 1
	if since >= first {
		first = since + 1
	}
	out := make([]api.LogEntry, 0, limit)
	next := first - 1
	for seq := first; seq <= r.next && len(out) < limit; seq++ {
		out = append(out, r.slots[int((seq-1)%logRingSize)])
		next = seq
	}
	return out, next
}

// joinStringRE matches skein:1:/skein:2: invite strings, which carry public
// keys (and for skein:2: the network secret) and must never appear verbatim.
var joinStringRE = regexp.MustCompile(`skein:[12]:[A-Za-z0-9_\-]+`)

// redactor scrubs known secrets out of log lines before they are stored or
// streamed.
type redactor struct {
	pairs [][2]string
}

func newRedactor(cfg conf.Config) *redactor {
	r := &redactor{}
	for _, b := range [][]byte{cfg.Node.NetworkSecret, cfg.API.Token} {
		for _, enc := range []string{
			base64.RawURLEncoding.EncodeToString(b),
			base64.StdEncoding.EncodeToString(b),
		} {
			if len(b) > 0 && enc != "" {
				r.pairs = append(r.pairs, [2]string{enc, "<secret>"})
			}
		}
	}
	return r
}

func (r *redactor) apply(s string) string {
	for _, p := range r.pairs {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	return joinStringRE.ReplaceAllString(s, "skein:<redacted>")
}

// list names what the redactor scrubs, for the diagnostics bundle.
func (r *redactor) list() []string {
	out := []string{"join_strings"}
	if len(r.pairs) > 0 {
		out = append(out, "network_secret", "api_token")
	}
	return out
}

// logWriter feeds the standard "skein: " logger into the ring, parsing the
// warning:/error: prefixes into levels and publishing log:entry events.
type logWriter struct {
	ring *logRing
	red  *redactor
	hub  *api.Hub
}

func (w *logWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	level := "info"
	msg := line
	if i := strings.LastIndex(line, " skein: "); i >= 0 {
		rest := line[i+len(" skein: "):]
		switch {
		case strings.HasPrefix(rest, "warning: "):
			level = "warning"
			rest = rest[len("warning: "):]
		case strings.HasPrefix(rest, "error: "):
			level = "error"
			rest = rest[len("error: "):]
		}
		msg = rest
	}
	msg = w.red.apply(msg)
	entry := api.LogEntry{TimeMS: time.Now().UnixMilli(), Level: level, Message: msg}
	w.ring.add(level, msg)
	if w.hub != nil {
		w.hub.Publish("log:entry", entry)
	}
	return len(p), nil
}

// publish is a nil-safe hub write: hand-rolled daemons in tests have no hub.
func (d *Daemon) publish(typ string, data any) {
	if d.hub != nil {
		d.hub.Publish(typ, data)
	}
}

// emit keeps the original event types for existing CLI consumers and moves
// them onto the hub.
func (d *Daemon) emit(ev api.Event) {
	ev.TimeMS = time.Now().UnixMilli()
	d.publish(ev.Type, ev)
}

// emitPeerChanged publishes the full peer table after any peer mutation.
func (d *Daemon) emitPeerChanged() {
	d.publish("peers:changed", d.Peers())
}

// emitRouteChanged publishes the full route table after route-affecting
// changes (session up, peer removed).
func (d *Daemon) emitRouteChanged() {
	d.publish("routes:changed", d.Routes())
}

// emitActivitySample publishes one aggregate link sample.
func (d *Daemon) emitActivitySample(origin uint64, seq uint64, rec activity.Record, expires time.Time) {
	row := api.Activity{
		Origin:      fmtID(origin),
		Destination: fmtID(rec.Destination),
		Seq:         seq,
		RxBytes:     rec.RxBytes,
		TxBytes:     rec.TxBytes,
		RTTMs:       float64(rec.RTT) / float64(time.Millisecond),
		JitterMs:    float64(rec.Jitter) / float64(time.Millisecond),
		Transport:   rec.Transport.String(),
		Direct:      rec.Direct,
		ObservedMS:  rec.ObservedAt.UnixMilli(),
		ExpiresMS:   expires.UnixMilli(),
	}
	for _, id := range rec.Path {
		row.Path = append(row.Path, fmtID(id))
	}
	d.publish("activity:sample", row)
}

// fmtID renders a peer ID in the wire form.
func fmtID(id uint64) string { return fmt.Sprintf("%016x", id) }
