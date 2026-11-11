//go:build !linux && !windows

package netcfg

import "net/netip"

func configureSteps(string, int, []netip.Prefix, []netip.Prefix) ([]step, error) {
	return nil, ErrUnsupported
}

func removalSteps(string, []netip.Prefix, []netip.Prefix) [][][]string { return nil }

func listRoutes() ([]routeEntry, error) { return nil, ErrUnsupported }

func deleteRouteArgs(netip.Prefix, string) [][]string { return nil }

func setMTU(string, int) error { return ErrUnsupported }
