package link

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
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
	// fallback preference must be dropped again. The reply must go out
	// as a raw datagram: Switch.Send routes by peer address and would
	// prefer the live TCP connection (SendToIP), so it can never
	// exercise the UDP-clearing path it is meant to test.
	aAddr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), a.Port())
	if err := b.udp.Send(aAddr, payload); err != nil {
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
	// Clearing the flag is not enough: Send must actually stop preferring
	// the live stream, or the link stays on TCP forever.
	if err := a.Send(addr, payload); err != nil {
		t.Fatalf("send after proof: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		n, _, transport, err := b.RecvTagged(buf)
		if err != nil {
			t.Fatalf("recv after proof: %v", err)
		}
		if n == len(payload) {
			if transport != "udp" {
				t.Fatalf("transport = %q after UDP proof, want udp", transport)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no frame after UDP proof")
		}
	}
	if got := a.TransportFor(addr); got != "udp" {
		t.Fatalf("TransportFor = %q after UDP proof, want udp", got)
	}
}

// TestTCPConcurrentSendsStayFramed guards the per-connection write lock: the
// dataplane and the control loop send on the same stream. It reads the raw
// stream instead of the drop-on-saturation queue, so delivery is deterministic.
func TestTCPConcurrentSendsStayFramed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer client.Close()
	defer server.Close()

	addr := netip.MustParseAddrPort("127.0.0.1:9")
	link := &TCP{conns: make(map[netip.AddrPort]*tcpConn), closed: make(chan struct{})}
	link.track(addr, client)

	const senders, frames = 2, 100
	var wg sync.WaitGroup
	for g := 0; g < senders; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < frames; i++ {
				payload := bytes.Repeat([]byte{byte('a' + g)}, 64+i%32)
				if err := link.Send(addr, payload); err != nil {
					t.Errorf("send: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < senders*frames; i++ {
		var hdr [4]byte
		if _, err := io.ReadFull(server, hdr[:]); err != nil {
			t.Fatalf("frame %d header: %v", i, err)
		}
		payload := make([]byte, int(binary.BigEndian.Uint32(hdr[:])))
		if _, err := io.ReadFull(server, payload); err != nil {
			t.Fatalf("frame %d body: %v", i, err)
		}
		for j := range payload {
			if payload[j] != payload[0] {
				t.Fatalf("frame %d interleaved: %x", i, payload)
			}
		}
	}
}

// TestSwitchAcceptSideReplyUsesTCP is the M4 P1 (F-01) regression test: a
// reply to the source of an accepted TCP connection must travel over that
// connection, not as a UDP datagram to the peer's ephemeral TCP port (where
// no UDP socket exists and the frame is silently lost). On the old code the
// accept side's Switch.Send fell through to UDP because `forced` is only set
// by an explicit DialTCP on the dial side.
func TestSwitchAcceptSideReplyUsesTCP(t *testing.T) {
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
	bAddr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), b.Port())
	if err := a.DialTCP(bAddr); err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	payload := frameBytes(40)
	if err := a.Send(bAddr, payload); err != nil {
		t.Fatalf("tcp send: %v", err)
	}
	buf := make([]byte, 64)
	n, from, transport, err := b.RecvTagged(buf)
	if err != nil || n != len(payload) || transport != "tcp" {
		t.Fatalf("tcp recv: n=%d transport=%q err=%v", n, transport, err)
	}
	// Reply to the exact source of the accepted connection (ephemeral port).
	reply := frameBytes(30)
	if err := b.Send(from, reply); err != nil {
		t.Fatalf("accept-side reply Send: %v", err)
	}
	got := make(chan struct {
		n         int
		transport string
		err       error
	}, 1)
	go func() {
		n, _, transport, err := a.RecvTagged(buf)
		got <- struct {
			n         int
			transport string
			err       error
		}{n, transport, err}
	}()
	select {
	case r := <-got:
		if r.err != nil || r.n != len(reply) || r.transport != "tcp" {
			t.Fatalf("reply recv: n=%d transport=%q err=%v", r.n, r.transport, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("accept-side reply never arrived over TCP (old code sent it as UDP to an ephemeral port)")
	}
}

// TestSwitchSendToListenAddrUsesAcceptedTCP covers TCP relaying: a node that
// only holds an accepted connection from a peer (keyed by the peer's ephemeral
// port) must still reach that peer over TCP when sending to the peer's listen
// address, because no UDP path may exist. The fallback applies only when
// exactly one connection exists for the destination IP, so it never guesses
// between two peers behind one NAT address.
func TestSwitchSendToListenAddrUsesAcceptedTCP(t *testing.T) {
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
	bAddr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), b.Port())
	if err := a.DialTCP(bAddr); err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	payload := frameBytes(40)
	if err := a.Send(bAddr, payload); err != nil {
		t.Fatalf("tcp send: %v", err)
	}
	buf := make([]byte, 64)
	if _, _, transport, err := b.RecvTagged(buf); err != nil || transport != "tcp" {
		t.Fatalf("tcp recv: transport=%q err=%v", transport, err)
	}
	// b sends to a's listen address, not to the ephemeral dial source.
	aAddr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), a.Port())
	if err := b.Send(aAddr, payload); err != nil {
		t.Fatalf("send to listen addr: %v", err)
	}
	got := make(chan struct {
		n         int
		transport string
		err       error
	}, 1)
	go func() {
		n, _, transport, err := a.RecvTagged(buf)
		got <- struct {
			n         int
			transport string
			err       error
		}{n, transport, err}
	}()
	select {
	case r := <-got:
		if r.err != nil || r.n != len(payload) || r.transport != "tcp" {
			t.Fatalf("recv: n=%d transport=%q err=%v", r.n, r.transport, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("frame to listen address never arrived over the accepted TCP connection")
	}
}

func TestSwitchSurvivesTruncatedDatagram(t *testing.T) {
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

	addr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), b.Port())
	raw, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Write(make([]byte, 2000)); err != nil {
		t.Fatal(err)
	}

	payload := frameBytes(40)
	if err := a.Send(addr, payload); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var got int
	var recvErr error
	go func() {
		buf := make([]byte, 64)
		got, _, _, recvErr = b.RecvTagged(buf)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("receive loop stopped after a truncated datagram")
	}
	if recvErr != nil || got != len(payload) {
		t.Fatalf("recv = %d, %v; want %d, nil", got, recvErr, len(payload))
	}
}
