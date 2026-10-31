package link

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func listen(t *testing.T) *UDP {
	t.Helper()
	l, err := ListenUDP(0)
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestIPv4Loopback(t *testing.T) {
	a, b := listen(t), listen(t)
	from := netip.MustParseAddrPort("127.0.0.1:" + itoa(b.Port()))
	if err := a.Send(from, []byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	buf := make([]byte, 64)
	n, src, err := b.Recv(buf)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("payload = %q", buf[:n])
	}
	if !src.Addr().Is4() || src.Port() != a.Port() {
		t.Fatalf("source = %s, want 127.0.0.1:%d", src, a.Port())
	}
}

func TestIPv6Loopback(t *testing.T) {
	if !supportsIPv6() {
		t.Skip("host has no IPv6 loopback")
	}
	a, b := listen(t), listen(t)
	from := netip.MustParseAddrPort("[::1]:" + itoa(b.Port()))
	if err := a.Send(from, []byte("v6")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	buf := make([]byte, 64)
	n, src, err := b.Recv(buf)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(buf[:n]) != "v6" {
		t.Fatalf("payload = %q", buf[:n])
	}
	if !src.Addr().Is6() || src.Addr().Is4In6() {
		t.Fatalf("source = %s, want an unmapped IPv6 address", src)
	}
}

func TestIPv4MappedSourceIsNormalized(t *testing.T) {
	a, b := listen(t), listen(t)
	from := netip.MustParseAddrPort("127.0.0.1:" + itoa(b.Port()))
	if err := a.Send(from, []byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	_, src, err := b.Recv(buf)
	if err != nil {
		t.Fatal(err)
	}
	if src.Addr().Is4In6() {
		t.Fatalf("source %s was not unmapped", src)
	}
}

func TestLocalAddrsAndObserve(t *testing.T) {
	l := listen(t)
	if len(l.LocalAddrs()) == 0 {
		t.Fatal("no local addresses reported")
	}
	if _, err := l.Observe(); err == nil {
		t.Fatal("Observe must be unavailable in v1")
	}
}

func TestPortCollisionIsRejected(t *testing.T) {
	first := listen(t)
	port := first.Port()
	if _, err := ListenUDP(port); err == nil {
		t.Fatal("expected a second bind on the same port to fail")
	}
}

func TestClosedLink(t *testing.T) {
	l, err := ListenUDP(0)
	if err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddrPort("127.0.0.1:9")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := l.Send(addr, []byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Send after Close = %v, want net.ErrClosed", err)
	}
	if _, _, err := l.Recv(make([]byte, 8)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Recv after Close = %v, want net.ErrClosed", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTruncationDetected(t *testing.T) {
	a, b := listen(t), listen(t)
	from := netip.MustParseAddrPort("127.0.0.1:" + itoa(b.Port()))
	if err := a.Send(from, bytes.Repeat([]byte{0x41}, 600)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Recv(make([]byte, 32)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("Recv with a small buffer = %v, want ErrTruncated", err)
	}
}

func TestOversizedSendRejected(t *testing.T) {
	l := listen(t)
	if err := l.Send(netip.MustParseAddrPort("127.0.0.1:9"), make([]byte, MaxDatagram+1)); err == nil {
		t.Fatal("expected an oversized datagram to be rejected")
	}
}

func TestRecvDeadlineAfterCloseWhileBlocked(t *testing.T) {
	l := listen(t)
	done := make(chan error, 1)
	go func() {
		_, _, err := l.Recv(make([]byte, 64))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	l.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked Recv returned without an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Recv did not unblock on Close")
	}
}

func supportsIPv6() bool {
	conn, err := net.ListenPacket("udp6", "[::1]:0")
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func itoa(v uint16) string {
	if v == 0 {
		return "0"
	}
	var buf [5]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
