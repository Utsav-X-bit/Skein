// Package path enumerates usable local endpoints and probes candidate paths.
package path

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
)

// LocalCandidates returns the local interface endpoints a peer could dial on
// port. Loopback, unspecified, multicast, and unscoped link-local addresses are
// excluded.
func LocalCandidates(port uint16) ([]netip.AddrPort, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []netip.AddrPort
	seen := make(map[netip.AddrPort]struct{})
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			addr, ok := netip.AddrFromSlice(ipNet.IP)
			if !ok {
				continue
			}
			addr = addr.Unmap()
			if !Eligible(addr) {
				continue
			}
			ap := netip.AddrPortFrom(addr, port)
			if _, dup := seen[ap]; dup {
				continue
			}
			seen[ap] = struct{}{}
			out = append(out, ap)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// Eligible reports whether addr can serve as a candidate endpoint.
func Eligible(addr netip.Addr) bool {
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() {
		return false
	}
	if addr.IsLinkLocalUnicast() && addr.Zone() == "" {
		return false
	}
	return true
}

// Filter keeps only eligible candidates with a usable port, preserving order
// and removing duplicates.
func Filter(cands []netip.AddrPort) []netip.AddrPort {
	out := make([]netip.AddrPort, 0, len(cands))
	seen := make(map[netip.AddrPort]struct{}, len(cands))
	for _, c := range cands {
		c = netip.AddrPortFrom(c.Addr().Unmap(), c.Port())
		if c.Port() == 0 || !Eligible(c.Addr()) {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// Family reports the underlay family label for an endpoint.
func Family(addr netip.AddrPort) string {
	if addr.Addr().Is4() {
		return "v4"
	}
	return "v6"
}

// Validate normalizes and validates a candidate endpoint.
func Validate(addr netip.AddrPort) (netip.AddrPort, error) {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	if addr.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("path: endpoint %s has port zero", addr)
	}
	if !Eligible(addr.Addr()) {
		return netip.AddrPort{}, fmt.Errorf("path: endpoint %s is not a usable unicast address", addr)
	}
	return addr, nil
}
