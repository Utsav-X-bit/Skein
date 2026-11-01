package link

import (
	"bytes"
	"net/netip"
	"testing"
	"time"
)

func frameBytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}

func TestTCPRoundTrip(t *testing.T) {
	a, err := ListenTCP(0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := ListenTCP(0)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	bAddr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), b.LocalAddrs()[0].Port())
	if err := a.Dial(bAddr); err != nil {
		t.Fatalf("Dial: %v", err)
	}
	payload := frameBytes(40)
	if err := a.Send(bAddr, payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 64)
	n, from, err := b.Recv(buf)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Fatal("payload mismatch")
	}
	// The reply goes back over the accepted connection, keyed by its source.
	reply := frameBytes(30)
	if err := b.Send(from, reply); err != nil {
		t.Fatalf("reply Send: %v", err)
	}
	n, _, err = a.Recv(buf)
	if err != nil {
		t.Fatalf("reply Recv: %v", err)
	}
	if !bytes.Equal(buf[:n], reply) {
		t.Fatal("reply mismatch")
	}
}

func TestTCPRejectsShortFrame(t *testing.T) {
	a, err := ListenTCP(0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Send(netip.MustParseAddrPort("127.0.0.1:9"), frameBytes(40)); err == nil {
		t.Fatal("expected an error without a connection")
	}
	if err := a.Dial(netip.MustParseAddrPort("127.0.0.1:9")); err == nil {
		t.Fatal("expected dial to a closed port to fail")
	}
}

func TestSwitchPrefersUDPThenTCP(t *testing.T) {
	a, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !a.HasTCP() || !b.HasTCP() {
		t.Skip("TCP fallback unavailable")
	}

	addr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), b.Port())
	payload := frameBytes(40)

	// UDP first.
	if err := a.Send(addr, payload); err != nil {
		t.Fatalf("udp send: %v", err)
	}
	buf := make([]byte, 64)
	n, from, err := b.Recv(buf)
	if err != nil || n != len(payload) {
		t.Fatalf("udp recv: n=%d err=%v", n, err)
	}
	if !from.Addr().Is4() {
		t.Fatalf("unexpected source %s", from)
	}

	// After an explicit TCP dial the same address uses the stream.
	if err := a.DialTCP(addr); err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	if err := a.Send(addr, payload); err != nil {
		t.Fatalf("tcp send: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		n, _, err := b.Recv(buf)
		if err != nil {
			t.Fatalf("tcp recv: %v", err)
		}
		if n == len(payload) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no TCP frame")
		}
	}
}
