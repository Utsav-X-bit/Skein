package discovery

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/identity"
)

func BenchmarkEncodeDecode(b *testing.B) {
	local, err := identity.Generate()
	if err != nil {
		b.Fatal(err)
	}
	state := State{
		Origin:  local.ID,
		Seq:     1,
		Expires: time.Now().Add(time.Minute),
		Peers: []PeerRecord{{
			ID: local.ID, PublicKey: local.PublicKey, OverlayV4: netip.MustParseAddr("10.144.144.1"),
			Candidates: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:11010")},
		}},
	}
	payload, err := Encode(state)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Encode(state); err != nil {
			b.Fatal(err)
		}
		if _, err := Decode(payload); err != nil {
			b.Fatal(err)
		}
	}
}
