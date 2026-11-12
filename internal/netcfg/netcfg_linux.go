//go:build linux

package netcfg

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

func configureSteps(name string, mtu int, addresses, routes []netip.Prefix) ([]step, error) {
	steps := []step{
		{run: [][]string{{"ip", "link", "set", "dev", name, "mtu", strconv.Itoa(mtu)}}},
		{
			run:  [][]string{{"ip", "link", "set", "dev", name, "up"}},
			undo: [][]string{{"ip", "link", "set", "dev", name, "down"}},
		},
	}
	for _, addr := range addresses {
		base, err := ipArgs(addr.Addr())
		if err != nil {
			return nil, err
		}
		steps = append(steps, step{
			run:  [][]string{append(base, "addr", "add", addr.String(), "dev", name)},
			undo: [][]string{append(base, "addr", "del", addr.String(), "dev", name)},
		})
	}
	for _, route := range routes {
		if redundantRoute(route, addresses) {
			// Adding the address already created this connected route.
			continue
		}
		base, err := ipArgs(route.Addr())
		if err != nil {
			return nil, err
		}
		steps = append(steps, step{
			run:  [][]string{append(base, "route", "add", route.String(), "dev", name)},
			undo: [][]string{append(base, "route", "del", route.String(), "dev", name)},
		})
	}
	return steps, nil
}

func removalSteps(name string, addresses, routes []netip.Prefix) [][][]string {
	var out [][][]string
	for _, route := range routes {
		if redundantRoute(route, addresses) {
			// Removing the address removes the connected route.
			continue
		}
		base, err := ipArgs(route.Addr())
		if err != nil {
			continue
		}
		out = append(out, [][]string{append(base, "route", "del", route.String(), "dev", name)})
	}
	for _, addr := range addresses {
		base, err := ipArgs(addr.Addr())
		if err != nil {
			continue
		}
		out = append(out, [][]string{append(base, "addr", "del", addr.String(), "dev", name)})
	}
	return out
}

func ipArgs(addr netip.Addr) ([]string, error) {
	switch {
	case addr.Is4():
		return []string{"ip"}, nil
	case addr.Is6():
		return []string{"ip", "-6"}, nil
	default:
		return nil, fmt.Errorf("netcfg: unsupported address family for %s", addr)
	}
}

func listRoutes() ([]routeEntry, error) {
	var entries []routeEntry
	for _, family := range []struct {
		args     []string
		fallback netip.Prefix
	}{
		{[]string{"ip", "route", "show"}, netip.MustParsePrefix("0.0.0.0/0")},
		{[]string{"ip", "-6", "route", "show"}, netip.MustParsePrefix("::/0")},
	} {
		out, err := runner(family.args[0], family.args[1:]...)
		if err != nil {
			// A host without IPv6 has no IPv6 route table.
			if len(family.args) > 1 && family.args[1] == "-6" {
				continue
			}
			return nil, err
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			prefix := family.fallback
			if fields[0] != "default" {
				p, err := netip.ParsePrefix(fields[0])
				if err != nil {
					continue
				}
				prefix = p
			}
			var device string
			for i := 0; i+1 < len(fields); i++ {
				if fields[i] == "dev" {
					device = fields[i+1]
					break
				}
			}
			if device == "" {
				continue
			}
			entries = append(entries, routeEntry{prefix: prefix, device: device})
		}
	}
	return entries, nil
}

func deleteRouteArgs(prefix netip.Prefix, name string) [][]string {
	base, err := ipArgs(prefix.Addr())
	if err != nil {
		return nil
	}
	return [][]string{append(base, "route", "del", prefix.String(), "dev", name)}
}

// setMTU applies a new interface MTU in place.
func setMTU(name string, mtu int) error {
	_, err := runner("ip", "link", "set", "dev", name, "mtu", strconv.Itoa(mtu))
	return err
}

// IPv6Available reports whether this host can carry an IPv6 overlay address.
// A disabled IPv6 stack is not an error: the overlay stays IPv4-only instead
// of failing to bring the interface up.
func IPv6Available() bool {
	for _, path := range []string{
		"/proc/sys/net/ipv6/conf/all/disable_ipv6",
		"/proc/sys/net/ipv6/conf/default/disable_ipv6",
	} {
		raw, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(raw)) != "0" {
			return false
		}
	}
	return true
}
