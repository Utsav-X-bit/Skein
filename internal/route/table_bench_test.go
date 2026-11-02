package route

import (
	"testing"
	"time"
)

func BenchmarkLookup(b *testing.B) {
	tbl := NewTable(1)
	for i := uint64(2); i < 64; i++ {
		if err := tbl.UpdateDirect(DirectLink{PeerID: i, RTT: time.Millisecond}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := tbl.Lookup(42); !ok {
			b.Fatal("missing")
		}
	}
}
