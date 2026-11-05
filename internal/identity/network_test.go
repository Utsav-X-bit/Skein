package identity

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"
)

func TestNetworkInviteRoundTrip(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	inv := NetworkInvite{
		Secret:    bytes.Repeat([]byte{0x5a}, KeySize),
		ID:        id.ID,
		PublicKey: id.PublicKey,
		PrefixV4:  netip.MustParsePrefix("10.144.144.0/24"),
		PrefixV6:  netip.MustParsePrefix("fd00:6d65:7368::/64"),
		UsedV4: []netip.Addr{
			netip.MustParseAddr("10.144.144.1"),
			netip.MustParseAddr("10.144.144.2"),
		},
		Addrs: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.10:11010")},
	}
	s, err := EncodeNetworkInvite(inv)
	if err != nil {
		t.Fatalf("EncodeNetworkInvite: %v", err)
	}
	if !strings.HasPrefix(s, "skein:2:") {
		t.Fatalf("missing prefix: %q", s)
	}
	got, err := DecodeNetworkInvite(s)
	if err != nil {
		t.Fatalf("DecodeNetworkInvite: %v", err)
	}
	if !bytes.Equal(got.Secret, inv.Secret) || got.ID != inv.ID {
		t.Fatal("identity or secret mismatch")
	}
	if got.PrefixV4 != inv.PrefixV4 || got.PrefixV6 != inv.PrefixV6 {
		t.Fatalf("prefixes = %v %v", got.PrefixV4, got.PrefixV6)
	}
	if len(got.UsedV4) != 2 || got.UsedV4[1] != inv.UsedV4[1] {
		t.Fatalf("used = %v", got.UsedV4)
	}
	if len(got.Addrs) != 1 || got.Addrs[0] != inv.Addrs[0] {
		t.Fatalf("addrs = %v", got.Addrs)
	}
}

// TestNetworkInviteAllowsSixteenEndpoints is the regression test for the
// 2026-11-05 live failure: a multi-homed laptop (two LANs, Tailscale, a
// ppp VPN, STUN public IPs, plus link-local IPv6s) gathers 9 endpoints,
// but MaxEndpoints was 8, so `invite --network` refused with
// "network invite carries 9 endpoints, maximum is 8" and no node could
// join the fresh network. The cap is 16: a 16-endpoint invite still fits
// the 512-byte payload cap even worst-case (16 IPv6 endpoints + 16 used
// addresses = 466 bytes), and every MaxEndpoints consumer (config peer
// validation, mDNS, peer candidate table) reads the same constant.
// Zones are dropped on the wire (appendAddrPort encodes only the IP),
// so the round-trip asserts counts, not exact zoned equality.
func TestNetworkInviteAllowsSixteenEndpoints(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	addrs := []string{
		"192.168.31.37:11010",
		"192.168.0.113:11010",
		"100.93.83.78:11010",
		"10.95.0.15:11010",
		"[2406:b400:72:f292:2ec:93f1:cc8b:be2e]:11010",
		"[fd7a:115c:a1e0::e501:53fd]:11010",
		"[fe80::4201:627f:3f79:3e97%eno2]:11010",
		"[fe80::dc94:68f1:70b:49c1%wlan0]:11010",
		"[fe80::42ca:2bd7:fd2a:8f5d%tailscale0]:11010",
		"192.168.31.38:11010",
		"192.168.0.114:11010",
		"100.93.83.79:11010",
		"10.95.0.16:11010",
		"[2406:b400:72:f292:2ec:93f1:cc8b:be2f]:11010",
		"[fd7a:115c:a1e0::e501:53fe]:11010",
		"[fe80::4201:627f:3f79:3e98%eno2]:11010",
		"192.168.31.39:11010",
	}
	mk := func(n int) NetworkInvite {
		out := make([]netip.AddrPort, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, netip.MustParseAddrPort(addrs[i]))
		}
		return NetworkInvite{
			Secret:    bytes.Repeat([]byte{0x5a}, KeySize),
			ID:        id.ID,
			PublicKey: id.PublicKey,
			PrefixV4:  netip.MustParsePrefix("10.144.144.0/24"),
			PrefixV6:  netip.MustParsePrefix("fd00:6d65:7368::/64"),
			UsedV4:    []netip.Addr{netip.MustParseAddr("10.144.144.1")},
			Addrs:     out,
		}
	}
	for _, n := range []int{9, 16} {
		s, err := EncodeNetworkInvite(mk(n))
		if err != nil {
			t.Fatalf("EncodeNetworkInvite(%d endpoints): %v", n, err)
		}
		got, err := DecodeNetworkInvite(s)
		if err != nil {
			t.Fatalf("DecodeNetworkInvite(%d endpoints): %v", n, err)
		}
		if len(got.Addrs) != n {
			t.Fatalf("decoded %d endpoints, want %d", len(got.Addrs), n)
		}
	}
	if _, err := EncodeNetworkInvite(mk(17)); err == nil {
		t.Fatal("expected 17 endpoints to be rejected")
	}
}

func TestNetworkInviteRejectsBadInput(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	good, err := EncodeNetworkInvite(NetworkInvite{
		Secret: bytes.Repeat([]byte{1}, KeySize), ID: id.ID, PublicKey: id.PublicKey,
		PrefixV4: netip.MustParsePrefix("10.144.144.0/24"),
		PrefixV6: netip.MustParsePrefix("fd00:6d65:7368::/64"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"v1 prefix":  strings.Replace(good, "skein:2:", "skein:1:", 1),
		"bad base64": "skein:2:!!!!",
		"truncated":  "skein:2:AAAA",
		"trailing":   good + "AAAA",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeNetworkInvite(in); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if _, err := EncodeNetworkInvite(NetworkInvite{Secret: []byte{1}, PublicKey: id.PublicKey}); err == nil {
		t.Fatal("expected a short secret to be rejected")
	}
	if _, err := EncodeNetworkInvite(NetworkInvite{
		Secret: bytes.Repeat([]byte{1}, KeySize), ID: id.ID + 1, PublicKey: id.PublicKey,
		PrefixV4: netip.MustParsePrefix("10.144.144.0/24"),
		PrefixV6: netip.MustParsePrefix("fd00:6d65:7368::/64"),
	}); err == nil {
		t.Fatal("expected an ID mismatch to be rejected")
	}
}
