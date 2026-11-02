package activity

import (
	"testing"
	"time"
)

func TestCacheKeepsFreshestAndExpires(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewCache(2)
	c.Put(1, 7, Record{Destination: 2, RxBytes: 50, ObservedAt: now, Path: []uint64{2}}, now.Add(time.Second))
	c.Put(1, 6, Record{Destination: 2, RxBytes: 1, ObservedAt: now, Path: []uint64{2}}, now.Add(time.Second))
	got := c.Snapshot(now)
	if len(got) != 1 || got[0].RxBytes != 50 || got[0].Seq != 7 {
		t.Fatalf("snapshot = %+v", got)
	}
	if got := c.Snapshot(now.Add(time.Second)); len(got) != 0 {
		t.Fatalf("expired = %+v", got)
	}
}

func TestCacheBoundsEntries(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewCache(2)
	for _, d := range []uint64{2, 3, 4} {
		c.Put(1, d, Record{Destination: d, ObservedAt: now, Path: []uint64{d}}, now.Add(time.Duration(d)*time.Second))
	}
	got := c.Snapshot(now)
	if len(got) != 2 || got[0].Destination != 3 || got[1].Destination != 4 {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestResetOriginAcceptsARestartedPeer(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewCache(4)
	c.Put(1, 9, Record{Destination: 2, ObservedAt: now, Path: []uint64{2}}, now.Add(time.Minute))
	c.ResetOrigin(1)
	if got := c.Snapshot(now); len(got) != 0 {
		t.Fatalf("samples survived the reset: %+v", got)
	}
	c.Put(1, 1, Record{Destination: 2, ObservedAt: now, Path: []uint64{2}}, now.Add(time.Minute))
	if got := c.Snapshot(now); len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("restarted peer was treated as stale: %+v", got)
	}
}
