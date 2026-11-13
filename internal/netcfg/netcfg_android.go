//go:build android

package netcfg

import (
	"fmt"
	"net/netip"
)

// Android has no `ip` or `netsh`: addresses, routes, and MTU for the
// overlay live in the VpnService.Builder configuration owned by
// SkeinVpnService, which the system applies when establish() runs.
// The address and route operations are therefore no-ops that succeed;
// the daemon keeps calling the same netcfg entry points as on desktop.
//
// SetMTU is the exception: the interface MTU is fixed when the Builder
// runs, so it reports ErrUnsupported instead of pretending to have
// applied a negotiated MTU (see setMTU).

func configureSteps(string, int, []netip.Prefix, []netip.Prefix) ([]step, error) {
	return nil, nil
}

func removalSteps(string, []netip.Prefix, []netip.Prefix) [][][]string { return nil }

func listRoutes() ([]routeEntry, error) { return nil, nil }

func deleteRouteArgs(netip.Prefix, string) [][]string { return nil }

// setMTU reports that Android cannot change the interface MTU: VpnService
// .Builder fixes it when establish() runs and nothing can raise it afterwards.
// Returning success would be a lie the daemon acts on — it raises its own
// accept limit to the negotiated value and then the kernel drops every packet
// that big on the way into the TUN (EMSGSIZE), which looks like random loss on
// large transfers.
func setMTU(string, int) error {
	return fmt.Errorf("netcfg: %w: the Android TUN MTU is fixed when VpnService.Builder establishes it", ErrUnsupported)
}

// IPv6Available reports whether this host can carry an IPv6 overlay
// address. Android devices carry IPv6; the overlay address is
// verified by the VpnService when the TUN is established.
func IPv6Available() bool { return true }

// OverlayIPv6Usable reports whether addr answers on this host.
func OverlayIPv6Usable(_ string, addr netip.Addr) bool { return addr.IsValid() }
