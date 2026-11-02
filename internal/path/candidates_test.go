package path

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestEligible(t *testing.T) {
	cases := map[string]bool{
		"192.0.2.1":    true,
		"2001:db8::1":  true,
		"127.0.0.1":    false,
		"::1":          false,
		"0.0.0.0":      false,
		"::":           false,
		"239.1.1.1":    false,
		"ff02::1":      false,
		"fe80::1":      false,
		"fe80::1%eth0": true,
	}
	for in, want := range cases {
		addr := netip.MustParseAddr(in)
		if got := Eligible(addr); got != want {
			t.Fatalf("Eligible(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestFilterNormalizesAndDeduplicates(t *testing.T) {
	in := []netip.AddrPort{
		netip.MustParseAddrPort("192.0.2.1:11010"),
		netip.MustParseAddrPort("[::ffff:192.0.2.1]:11010"),
		netip.MustParseAddrPort("127.0.0.1:11010"),
		netip.MustParseAddrPort("192.0.2.2:0"),
		netip.MustParseAddrPort("[2001:db8::1]:11010"),
	}
	out := Filter(in)
	if len(out) != 2 {
		t.Fatalf("Filter = %v, want 2 endpoints", out)
	}
	if out[0].Addr().Is4In6() || out[1].Addr().Is4In6() {
		t.Fatal("mapped addresses were not unmapped")
	}
}

func TestLocalCandidatesExcludeLoopback(t *testing.T) {
	cands, err := LocalCandidates(11010)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		if c.Addr().IsLoopback() || c.Addr().IsUnspecified() || c.Addr().IsMulticast() {
			t.Fatalf("ineligible candidate %s", c)
		}
		if c.Port() != 11010 {
			t.Fatalf("port = %d", c.Port())
		}
	}
}

func TestProberPicksLowestRTT(t *testing.T) {
	cands := []netip.AddrPort{
		netip.MustParseAddrPort("192.0.2.1:11010"),
		netip.MustParseAddrPort("192.0.2.2:11010"),
		netip.MustParseAddrPort("192.0.2.3:11010"),
	}
	p := &Prober{
		Timeout: time.Second,
		Probe: func(ctx context.Context, addr netip.AddrPort) (time.Duration, error) {
			switch addr.Addr().String() {
			case "192.0.2.1":
				return 40 * time.Millisecond, nil
			case "192.0.2.2":
				return 5 * time.Millisecond, nil
			default:
				return 0, errors.New("timeout")
			}
		},
	}
	got, err := p.Best(context.Background(), cands)
	if err != nil {
		t.Fatal(err)
	}
	if got.Addr.Addr().String() != "192.0.2.2" || got.RTT != 5*time.Millisecond {
		t.Fatalf("winner = %+v", got)
	}
}

func TestProberNoCommonFamily(t *testing.T) {
	p := &Prober{
		Timeout: time.Second,
		Probe: func(ctx context.Context, addr netip.AddrPort) (time.Duration, error) {
			return 0, errors.New("unreachable")
		},
	}
	if _, err := p.Best(context.Background(), []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:11010")}); !errors.Is(err, ErrNoPath) {
		t.Fatalf("err = %v, want ErrNoPath", err)
	}
	if _, err := p.Best(context.Background(), nil); !errors.Is(err, ErrNoPath) {
		t.Fatalf("empty candidate err = %v, want ErrNoPath", err)
	}
}

func TestProberTimeout(t *testing.T) {
	p := &Prober{
		Timeout: 30 * time.Millisecond,
		Probe: func(ctx context.Context, addr netip.AddrPort) (time.Duration, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		},
	}
	start := time.Now()
	if _, err := p.Best(context.Background(), []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:11010")}); err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("probe took %v", elapsed)
	}
}

func TestProberBoundedConcurrency(t *testing.T) {
	cands := make([]netip.AddrPort, 20)
	for i := range cands {
		cands[i] = netip.MustParseAddrPort("192.0.2." + itoa(i+1) + ":11010")
	}
	var active, peak int
	mu := make(chan struct{}, 1)
	p := &Prober{
		Timeout:     time.Second,
		Concurrency: 3,
		Probe: func(ctx context.Context, addr netip.AddrPort) (time.Duration, error) {
			mu <- struct{}{}
			active++
			if active > peak {
				peak = active
			}
			<-mu
			time.Sleep(5 * time.Millisecond)
			mu <- struct{}{}
			active--
			<-mu
			return time.Millisecond, nil
		},
	}
	if _, err := p.Best(context.Background(), cands); err != nil {
		t.Fatal(err)
	}
	if peak > 3 {
		t.Fatalf("peak concurrency = %d, want <= 3", peak)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func TestExcludePrefixesDropsOverlayAddresses(t *testing.T) {
	cands := []netip.AddrPort{
		netip.MustParseAddrPort("10.144.144.1:11010"),
		netip.MustParseAddrPort("192.168.1.5:11010"),
		netip.MustParseAddrPort("[fd00:6d65:7368::2]:11010"),
		netip.MustParseAddrPort("[2001:db8::5]:11010"),
	}
	got := ExcludePrefixes(cands, []netip.Prefix{
		netip.MustParsePrefix("10.144.144.0/24"),
		netip.MustParsePrefix("fd00:6d65:7368::/64"),
	})
	if len(got) != 2 || got[0].Addr().String() != "192.168.1.5" || got[1].Addr().String() != "2001:db8::5" {
		t.Fatalf("ExcludePrefixes = %v", got)
	}
}
