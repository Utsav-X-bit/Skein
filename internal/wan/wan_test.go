package wan

import (
	"net/netip"
	"testing"
)

func TestRoutable(t *testing.T) {
	cases := map[string]bool{
		"203.0.113.7":   true,
		"2001:db8::5":   true,
		"10.0.0.1":      false,
		"192.168.1.6":   false,
		"172.16.0.1":    false,
		"100.64.0.1":    false,
		"100.127.255.1": false,
		"127.0.0.1":     false,
		"fe80::1":       false,
		"fc00::1":       false,
	}
	for in, want := range cases {
		if got := Routable(netip.MustParseAddr(in)); got != want {
			t.Fatalf("Routable(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestMergeDeduplicates(t *testing.T) {
	a := []netip.AddrPort{netip.MustParseAddrPort("203.0.113.7:11010")}
	b := []netip.AddrPort{netip.MustParseAddrPort("203.0.113.7:11010"), netip.MustParseAddrPort("198.51.100.9:11010")}
	got := Merge(a, b)
	if len(got) != 2 {
		t.Fatalf("Merge = %v", got)
	}
}
