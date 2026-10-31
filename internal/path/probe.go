package path

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// ErrNoPath reports that no candidate answered.
var ErrNoPath = errors.New("path: no candidate answered")

// ProbeFunc measures one candidate path. It must return only after the peer
// proved possession of the session keys over that path.
type ProbeFunc func(ctx context.Context, addr netip.AddrPort) (time.Duration, error)

// Result is the winning candidate.
type Result struct {
	Addr netip.AddrPort
	RTT  time.Duration
}

// Prober races candidate endpoints and keeps the lowest-RTT validated path.
type Prober struct {
	Probe       ProbeFunc
	Timeout     time.Duration
	Concurrency int
}

// Best probes candidates with bounded concurrency and returns the fastest.
func (p *Prober) Best(ctx context.Context, candidates []netip.AddrPort) (Result, error) {
	candidates = Filter(candidates)
	if len(candidates) == 0 {
		return Result{}, ErrNoPath
	}
	if p.Probe == nil {
		return Result{}, errors.New("path: prober has no probe function")
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	limit := p.Concurrency
	if limit <= 0 {
		limit = 4
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		addr netip.AddrPort
		rtt  time.Duration
		err  error
	}
	results := make(chan outcome, len(candidates))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, cand := range candidates {
		wg.Add(1)
		go func(addr netip.AddrPort) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results <- outcome{addr: addr, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			rtt, err := p.Probe(ctx, addr)
			results <- outcome{addr: addr, rtt: rtt, err: err}
		}(cand)
	}
	wg.Wait()
	close(results)

	var best Result
	found := false
	for r := range results {
		if r.err != nil {
			continue
		}
		if !found || r.rtt < best.RTT || (r.rtt == best.RTT && r.addr.String() < best.Addr.String()) {
			best = Result{Addr: r.addr, RTT: r.rtt}
			found = true
		}
	}
	if !found {
		return Result{}, ErrNoPath
	}
	return best, nil
}

// SortCandidates orders endpoints deterministically by family then address.
func SortCandidates(cands []netip.AddrPort) {
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Addr().Is4() != cands[j].Addr().Is4() {
			return cands[i].Addr().Is4()
		}
		return cands[i].String() < cands[j].String()
	})
}
