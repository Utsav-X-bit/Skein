package link

import (
	"bytes"
	"net/netip"
	"sync"
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
	n, from, transport, err := b.RecvTagged(buf)
	if err != nil || n != len(payload) {
		t.Fatalf("udp recv: n=%d err=%v", n, err)
	}
	if transport != "udp" {
		t.Fatalf("transport = %q, want udp", transport)
	}
	if !from.Addr().Is4() {
		t.Fatalf("unexpected source %s", from)
	}

	// After an explicit TCP dial the same address uses the stream.
	if err := a.DialTCP(addr); err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	a.mu.Lock()
	forced := a.forced[addr.Addr()]
	a.mu.Unlock()
	if !forced {
		t.Fatal("DialTCP did not mark the fallback")
	}
	if err := a.Send(addr, payload); err != nil {
		t.Fatalf("tcp send: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		n, _, transport, err := b.RecvTagged(buf)
		if err != nil {
			t.Fatalf("tcp recv: %v", err)
		}
		if n == len(payload) {
			if transport != "tcp" {
				t.Fatalf("transport = %q, want tcp", transport)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no TCP frame")
		}
	}

	// A UDP frame from the peer proves the datagram path works, so the
	// fallback preference must be dropped again.
	aAddr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), a.Port())
	if err := b.Send(aAddr, payload); err != nil {
		t.Fatalf("udp reply: %v", err)
	}
	if _, _, err := a.Recv(buf); err != nil {
		t.Fatalf("udp reply recv: %v", err)
	}
	a.mu.Lock()
	forced = a.forced[addr.Addr()]
	a.mu.Unlock()
	if forced {
		t.Fatal("a UDP frame from the peer must clear the TCP fallback")
	}
}

// TestTCPConcurrentSendsStayFramed guards the per-connection write lock: the
// dataplane and the control loop send on the same stream.
func TestTCPConcurrentSendsStayFramed(t *testing.T) {
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
	addr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), b.LocalAddrs()[0].Port())
	if err := a.Dial(addr); err != nil {
		t.Fatal(err)
	}

	const senders, frames = 2, 200

	// Receive while sending: the link drops frames when its queue is full,
	// and the stream must still never interleave two frames.
	received := make(chan []byte, senders*frames)
	go func() {
		buf := make([]byte, 256)
		for {
			n, _, err := b.Recv(buf)
			if err != nil {
				close(received)
				return
			}
			received <- append([]byte(nil), buf[:n]...)
		}
	}()

	var wg sync.WaitGroup
	for g := 0; g < senders; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < frames; i++ {
				payload := bytes.Repeat([]byte{byte(g)}, 64+i%32)
				if err := a.Send(addr, payload); err != nil {
					t.Errorf("send: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	deadline := time.After(5 * time.Second)
	for i := 0; i < senders*frames; i++ {
		select {
		case frame, ok := <-received:
			if !ok {
				t.Fatalf("stream closed after %d frames", i)
			}
			for j := range frame {
				if frame[j] != frame[0] {
					t.Fatalf("frame %d interleaved: %x", i, frame)
				}
			}
		case <-deadline:
			t.Fatalf("only %d of %d frames arrived", i, senders*frames)
		}
	}
}
