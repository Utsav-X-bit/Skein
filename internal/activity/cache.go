// Package activity keeps bounded, aggregate mesh link telemetry.
package activity

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Transport classifies the link that produced a sample.
type Transport uint8

const (
	TransportUnknown Transport = iota
	TransportUDP
	TransportTCP
	TransportMixed
)

func (t Transport) String() string {
	switch t {
	case TransportUDP:
		return "udp"
	case TransportTCP:
		return "tcp"
	case TransportMixed:
		return "mixed"
	default:
		return "—"
	}
}

// TransportFromStrings normalizes the directional transport names in a peer
// snapshot. Differing known values mean the path is mixed.
func TransportFromStrings(tx, rx string) Transport {
	tx, rx = strings.ToLower(tx), strings.ToLower(rx)
	if tx == "" {
		tx = rx
	}
	if rx == "" {
		rx = tx
	}
	if tx != "" && rx != "" && tx != rx {
		return TransportMixed
	}
	switch tx {
	case "udp":
		return TransportUDP
	case "tcp":
		return TransportTCP
	default:
		return TransportUnknown
	}
}

// Record is a payload-free aggregate link sample. Origin comes from the
// authenticated STATE envelope, not this record.
type Record struct {
	Destination uint64
	RxBytes     uint64
	TxBytes     uint64
	RTT         time.Duration
	Jitter      time.Duration
	Transport   Transport
	Direct      bool
	ObservedAt  time.Time
	Path        []uint64
}

// Sample is a cached record with the authenticated origin and freshness data.
type Sample struct {
	Origin uint64
	Seq    uint64
	Record
	ExpiresAt time.Time
}

type cacheKey struct{ origin, destination uint64 }

// Cache is a synchronous bounded cache; callers choose when to prune it.
type Cache struct {
	mu       sync.Mutex
	capacity int
	samples  map[cacheKey]Sample
}

func NewCache(capacity int) *Cache {
	if capacity < 1 {
		capacity = 1
	}
	return &Cache{capacity: capacity, samples: make(map[cacheKey]Sample)}
}

// Put accepts only a newer, valid sample for an origin/destination pair.
func (c *Cache) Put(origin, seq uint64, r Record, expires time.Time) {
	if origin == 0 || r.Destination == 0 || r.ObservedAt.IsZero() || !expires.After(r.ObservedAt) {
		return
	}
	k := cacheKey{origin, r.Destination}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.samples[k]; ok && old.Seq >= seq {
		return
	}
	if _, exists := c.samples[k]; !exists && len(c.samples) >= c.capacity {
		var victim cacheKey
		var at time.Time
		for key, sample := range c.samples {
			if at.IsZero() || sample.ExpiresAt.Before(at) || (sample.ExpiresAt.Equal(at) && (key.origin < victim.origin || (key.origin == victim.origin && key.destination < victim.destination))) {
				victim, at = key, sample.ExpiresAt
			}
		}
		delete(c.samples, victim)
	}
	r.Path = append([]uint64(nil), r.Path...)
	c.samples[k] = Sample{Origin: origin, Seq: seq, Record: r, ExpiresAt: expires}
}

// Snapshot removes expired data and returns stable, defensive copies.
func (c *Cache) Snapshot(now time.Time) []Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Sample, 0, len(c.samples))
	for k, sample := range c.samples {
		if !sample.ExpiresAt.After(now) {
			delete(c.samples, k)
			continue
		}
		sample.Path = append([]uint64(nil), sample.Path...)
		out = append(out, sample)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin < out[j].Origin
		}
		return out[i].Destination < out[j].Destination
	})
	return out
}

func (c *Cache) Prune(now time.Time) { _ = c.Snapshot(now) }
