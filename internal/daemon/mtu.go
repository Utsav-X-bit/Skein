package daemon

import (
	"log"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/peer"
	"github.com/Utsav-X-bit/skein/internal/wire"
)

// applyNegotiatedMTU collects one measured path per directly connected peer
// and raises the overlay MTU once, when every such peer has reported.
//
// The interface MTU is global, so the usable value is the smallest path among
// the peers we can send to directly. The configured MTU stays the floor and is
// never lowered: existing TCP connections keep the MSS they negotiated, and a
// smaller interface MTU would make their packets oversize. A peer that joins
// later therefore cannot shrink the MTU mid-connection; its own sends are
// bounded by its own measurement.
func (d *Daemon) applyNegotiatedMTU(peerID uint64, frame int) {
	d.mtuMu.Lock()
	if d.mtuDone {
		d.mtuMu.Unlock()
		return
	}
	if d.mtuReport == nil {
		d.mtuReport = make(map[uint64]int)
	}
	d.mtuReport[peerID] = frame

	smallest, pending := 0, false
	for _, p := range d.peers.List() {
		if p.State != peer.StateUp || !p.Direct {
			continue
		}
		f, ok := d.mtuReport[p.ID]
		if !ok {
			pending = true
			continue
		}
		if f <= 0 {
			// One unmeasurable path keeps the configured MTU for this
			// connection: a guess here would be worse than the floor.
			d.mtuDone = true
			d.mtuMu.Unlock()
			return
		}
		if smallest == 0 || f < smallest {
			smallest = f
		}
	}
	if pending || smallest == 0 {
		d.mtuMu.Unlock()
		return
	}
	d.mtuDone = true
	d.mtuMu.Unlock()

	cfg := d.config()
	inner := smallest - wire.Overhead
	if inner > conf.MaxMTU {
		inner = conf.MaxMTU
	}
	if inner <= cfg.Node.MTU {
		return
	}
	// A paused dataplane has no engine to accept the larger packets; leave the
	// interface alone until the next connection negotiates again.
	e := d.engine()
	if e == nil {
		return
	}
	// Raise the accept limit first: the interface MTU is what TCP uses to
	// build segments, so it must never exceed what the engine accepts.
	e.SetMTU(inner)
	if err := d.setMTU(cfg.Node.Interface, inner); err != nil {
		log.Printf("skein: path-MTU negotiation: apply %d to %s: %v", inner, cfg.Node.Interface, err)
		return
	}
	log.Printf("skein: overlay MTU raised to %d (configured %d, measured path frame %d)", inner, cfg.Node.MTU, smallest)
}

// resetMTUNegotiation starts a fresh negotiation for a new connection.
func (d *Daemon) resetMTUNegotiation() {
	d.mtuMu.Lock()
	d.mtuReport = nil
	d.mtuDone = false
	d.mtuMu.Unlock()
}
