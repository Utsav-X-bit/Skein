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
