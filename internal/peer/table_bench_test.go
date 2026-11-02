package peer

import (
	"net/netip"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/identity"
)

func BenchmarkLookupOverlay(b *testing.B) {
	tbl := NewTable()
	for i := 0; i < 256; i++ {
		id, err := identity.Generate()
		if err != nil {
			b.Fatal(err)
		}
		addr := netip.AddrFrom4([4]byte{10, 0, byte(i / 256), byte(i)})
		if err := tbl.Upsert(Peer{ID: id.ID, PublicKey: id.PublicKey, OverlayV4: addr}); err != nil {
			b.Fatal(err)
		}
	}
	target := netip.MustParseAddr("10.0.0.42")
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := tbl.LookupOverlay(target); !ok {
			b.Fatal("missing")
		}
	}
}
