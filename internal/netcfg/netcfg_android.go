//go:build android

package netcfg

import "net/netip"

// Android has no `ip` or `netsh`: addresses, routes, and MTU for the
// overlay live in the VpnService.Builder configuration owned by
// SkeinVpnService, which the system applies when establish() runs.
// Every operation here is therefore a no-op that succeeds; the daemon
// keeps calling the same netcfg entry points as on desktop.
//
// One behavioural difference: SetMTU cannot raise the interface MTU
// after establish() — the MTU is fixed when the Builder runs — so a
// negotiated MTU raise only lifts the engine's accept limit, not the
// interface. The interface keeps the configured MTU from establish().

func configureSteps(string, int, []netip.Prefix, []netip.Prefix) ([]step, error) {
	return nil, nil
}

func removalSteps(string, []netip.Prefix, []netip.Prefix) [][][]string { return nil }

func listRoutes() ([]routeEntry, error) { return nil, nil }

func deleteRouteArgs(netip.Prefix, string) [][]string { return nil }

func setMTU(string, int) error { return nil }

// IPv6Available reports whether this host can carry an IPv6 overlay
// address. Android devices carry IPv6; the overlay address is
// verified by the VpnService when the TUN is established.
func IPv6Available() bool { return true }

// OverlayIPv6Usable reports whether addr answers on this host.
func OverlayIPv6Usable(_ string, addr netip.Addr) bool { return addr.IsValid() }
