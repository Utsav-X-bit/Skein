// Package wan discovers the public endpoint a node should advertise.
package wan

import (
	"context"
	"net/netip"
	"os"
	"time"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/stun"
)

// cgnat is the carrier-grade NAT range; an address there is not reachable.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Routable reports whether an address can be dialed from the internet.
func Routable(addr netip.Addr) bool {
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() ||
		addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() {
		return false
	}
	return !cgnat.Contains(addr)
}

// Candidates returns the public endpoints to advertise: the configured
// public_addr plus, when enabled, the address a STUN server reports.
func Candidates(ctx context.Context, cfg conf.Config) []netip.AddrPort {
	var out []netip.AddrPort
	if pub, ok := cfg.PublicCandidate(); ok {
		out = append(out, pub)
	}
	if !cfg.Discovery.STUN || os.Getenv("SKEIN_NO_STUN") != "" {
		return out
	}
	timeout := 2 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d < timeout {
			timeout = d
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// One probe per family: a single socket only reports the family it uses,
	// so the public IPv4 needs udp4 and the public IPv6 needs udp6.
	type result struct {
		addr netip.Addr
		err  error
	}
	results := make(chan result, 2)
	for _, network := range []string{"udp4", "udp6"} {
		go func(network string) {
			ip, err := stun.PublicIP(probeCtx, cfg.Discovery.STUNServer, network)
			results <- result{addr: ip, err: err}
		}(network)
	}
	for range 2 {
		r := <-results
		if r.err != nil || !Routable(r.addr) {
			continue
		}
		addr := netip.AddrPortFrom(r.addr, cfg.Node.ListenPort)
		dup := false
		for _, existing := range out {
			if existing == addr {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, addr)
		}
	}
	return out
}

// Merge appends extra to base without duplicates.
func Merge(base, extra []netip.AddrPort) []netip.AddrPort {
	out := append([]netip.AddrPort(nil), base...)
	for _, addr := range extra {
		dup := false
		for _, existing := range out {
			if existing == addr {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, addr)
		}
	}
	return out
}
