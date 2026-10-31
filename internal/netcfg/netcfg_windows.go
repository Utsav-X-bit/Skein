//go:build windows

package netcfg

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

func configureSteps(name string, mtu int, addresses, routes []netip.Prefix) ([]step, error) {
	steps := []step{
		{run: [][]string{
			{"netsh", "interface", "ipv4", "set", "subinterface", "name=" + name, "mtu=" + strconv.Itoa(mtu), "store=active"},
			{"netsh", "interface", "ipv6", "set", "subinterface", "name=" + name, "mtu=" + strconv.Itoa(mtu), "store=active"},
		}},
	}
	for _, addr := range addresses {
		base, err := netshFamily(addr.Addr())
		if err != nil {
			return nil, err
		}
		steps = append(steps, step{
			run: [][]string{
				{"netsh", "interface", base, "add", "address", "name=" + name, "address=" + addr.String()},
			},
			undo: [][]string{
				{"netsh", "interface", base, "delete", "address", "name=" + name, "address=" + addr.Addr().String()},
			},
		})
	}
	for _, route := range routes {
		if redundantRoute(route, addresses) {
			// Adding the address already created this on-link route.
			continue
		}
		base, err := netshFamily(route.Addr())
		if err != nil {
			return nil, err
		}
		steps = append(steps, step{
			run: [][]string{
				{"netsh", "interface", base, "add", "route", route.String(), "name=" + name},
			},
			undo: [][]string{
				{"netsh", "interface", base, "delete", "route", route.String(), "name=" + name},
			},
		})
	}
	return steps, nil
}

func removalSteps(name string, addresses, routes []netip.Prefix) [][][]string {
	var out [][][]string
	for _, route := range routes {
		if redundantRoute(route, addresses) {
			continue
		}
		base, err := netshFamily(route.Addr())
		if err != nil {
			continue
		}
		out = append(out, [][]string{
			{"netsh", "interface", base, "delete", "route", route.String(), "name=" + name},
		})
	}
	for _, addr := range addresses {
		base, err := netshFamily(addr.Addr())
		if err != nil {
			continue
		}
		out = append(out, [][]string{
			{"netsh", "interface", base, "delete", "address", "name=" + name, "address=" + addr.Addr().String()},
		})
	}
	return out
}

func netshFamily(addr netip.Addr) (string, error) {
	switch {
	case addr.Is4():
		return "ipv4", nil
	case addr.Is6():
		return "ipv6", nil
	default:
		return "", fmt.Errorf("netcfg: unsupported address family for %s", addr)
	}
}

func listRoutes() ([]routeEntry, error) {
	var entries []routeEntry
	for _, family := range []string{"ipv4", "ipv6"} {
		out, err := runner("netsh", "interface", family, "show", "routes")
		if err != nil {
			if family == "ipv6" {
				continue
			}
			return nil, err
		}
		entries = append(entries, parseNetshRoutes(out)...)
	}
	return entries, nil
}

// parseNetshRoutes reads the fixed column layout of `netsh ... show routes`:
//
//	Publish  Type      Met  Prefix                    Idx  Interface Name
//	No       Manual    256  10.144.144.0/24             12  Skein
func parseNetshRoutes(out string) []routeEntry {
	var entries []routeEntry
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		prefix, err := netip.ParsePrefix(fields[3])
		if err != nil {
			continue
		}
		device := strings.Join(fields[5:], " ")
		entries = append(entries, routeEntry{prefix: prefix, device: device})
	}
	return entries
}

func deleteRouteArgs(prefix netip.Prefix, name string) [][]string {
	base, err := netshFamily(prefix.Addr())
	if err != nil {
		return nil
	}
	return [][]string{{"netsh", "interface", base, "delete", "route", prefix.String(), "name=" + name}}
}
