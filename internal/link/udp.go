package link

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"github.com/Utsav-X-bit/skein/internal/wire"
)

// UDP is the dual-stack or paired-socket UDP Link implementation.
type UDP struct {
	conns []*net.UDPConn

	// packets carries datagrams when more than one socket is in use.
	packets chan received
	closed  chan struct{}

	closeOnce sync.Once
	closeErr  error
	readers   sync.WaitGroup
}

type received struct {
	data      []byte
	from      netip.AddrPort
	transport string
	err       error
}

// ProtectSocket, when set (Android only), exempts a socket FD from
// the VPN so its traffic bypasses the TUN. The GUI registers the
// JNI implementation at startup; on other platforms it stays nil
// and ListenUDP binds normally. A socket that is not protected has
// its packets routed into the TUN, so a peer on the LAN or the
// public internet is never reached.
var ProtectSocket func(fd int) bool

// ListenUDP binds the underlay socket. It prefers one dual-stack socket and
// falls back to paired IPv4/IPv6 sockets on the same port.
func ListenUDP(port uint16) (*UDP, error) {
	cfg := udpListenConfig()
	wildcard := net.JoinHostPort("", fmt.Sprintf("%d", port))
	if pc, err := cfg.ListenPacket(context.Background(), "udp", wildcard); err == nil {
		uc := pc.(*net.UDPConn)
		if local, ok := uc.LocalAddr().(*net.UDPAddr); ok && local.IP.To4() == nil {
			// IPv6 wildcard: Go binds this dual-stack when the OS allows it.
			return newUDP([]*net.UDPConn{uc}), nil
		}
		// The wildcard resolved to IPv4; add an IPv6 socket on the same port.
		bound := uc.LocalAddr().(*net.UDPAddr).Port
		if v6, err := cfg.ListenPacket(context.Background(), "udp6", fmt.Sprintf("[::]:%d", bound)); err == nil {
			return newUDP([]*net.UDPConn{uc, v6.(*net.UDPConn)}), nil
		}
		return newUDP([]*net.UDPConn{uc}), nil
	}

	pc4, err4 := cfg.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err4 != nil {
		return nil, fmt.Errorf("bind UDP port %d: %w", port, err4)
	}
	return newUDP([]*net.UDPConn{pc4.(*net.UDPConn)}), nil
}

// socketBuffer is the requested send/receive buffer per UDP socket. On a
// long-RTT path the bandwidth-delay product easily exceeds the OS default
// (a few hundred kilobytes), and every overflow is a dropped datagram that
// TCP has to retransmit.
const socketBuffer = 4 << 20

func newUDP(conns []*net.UDPConn) *UDP {
	for _, c := range conns {
		// The kernel caps these at net.core.rmem_max/wmem_max; failures are
		// harmless because the default still works.
		_ = c.SetReadBuffer(socketBuffer)
		_ = c.SetWriteBuffer(socketBuffer)
	}
	u := &UDP{conns: conns, closed: make(chan struct{})}
	if len(conns) > 1 {
		u.packets = make(chan received, 256)
		for _, c := range conns {
			u.readers.Add(1)
			go u.readLoop(c)
		}
	}
	return u
}

func (u *UDP) readLoop(conn *net.UDPConn) {
	defer u.readers.Done()
	buf := make([]byte, wire.MaxFrameSize+1)
	oob := make([]byte, 0)
	for {
		n, _, _, from, err := conn.ReadMsgUDPAddrPort(buf, oob)
		if err != nil {
			if isTruncationError(err) {
				// A datagram larger than the buffer is dropped, not
				// fatal: the loop keeps serving later datagrams.
				continue
			}
			select {
			case <-u.closed:
				return
			default:
			}
			select {
			case u.packets <- received{transport: "udp", err: err}:
			case <-u.closed:
			}
			return
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		select {
		case u.packets <- received{data: data, from: normalize(from), transport: "udp"}:
		case <-u.closed:
			return
		default:
			// The socket pair is saturated; drop like the network would.
		}
	}
}

// Send transmits payload to addr, choosing the socket that matches its family.
func (u *UDP) Send(addr netip.AddrPort, payload []byte) error {
	select {
	case <-u.closed:
		return net.ErrClosed
	default:
	}
	if len(payload) > MaxDatagram {
		return fmt.Errorf("link: datagram of %d bytes exceeds %d", len(payload), MaxDatagram)
	}
	addr = normalize(addr)
	conn := u.conns[0]
	if len(u.conns) > 1 {
		want4 := addr.Addr().Is4()
		for _, c := range u.conns {
			local, ok := c.LocalAddr().(*net.UDPAddr)
			if !ok {
				continue
			}
			if (local.IP.To4() != nil) == want4 {
				conn = c
				break
			}
		}
	}
	_, err := conn.WriteToUDPAddrPort(payload, addr)
	return err
}

// Recv reads one datagram into buf.
func (u *UDP) Recv(buf []byte) (int, netip.AddrPort, error) {
	if u.packets != nil {
		select {
		case pkt := <-u.packets:
			if pkt.err != nil {
				return 0, netip.AddrPort{}, pkt.err
			}
			if len(pkt.data) > len(buf) {
				return 0, pkt.from, ErrTruncated
			}
			return copy(buf, pkt.data), pkt.from, nil
		case <-u.closed:
			return 0, netip.AddrPort{}, net.ErrClosed
		}
	}

	oob := make([]byte, 0)
	n, _, flags, from, err := u.conns[0].ReadMsgUDPAddrPort(buf, oob)
	if err != nil {
		if isTruncationError(err) {
			return 0, netip.AddrPort{}, ErrTruncated
		}
		return 0, netip.AddrPort{}, err
	}
	if flags&msgTrunc != 0 {
		return 0, normalize(from), ErrTruncated
	}
	if n > MaxDatagram {
		return 0, normalize(from), ErrTruncated
	}
	return n, normalize(from), nil
}

// Observe is unavailable without a rendezvous service.
func (u *UDP) Observe() (netip.AddrPort, error) {
	return netip.AddrPort{}, errors.New("link: observe requires a rendezvous endpoint, which v1 does not provide")
}

// LocalAddrs reports the bound socket addresses.
func (u *UDP) LocalAddrs() []netip.AddrPort {
	out := make([]netip.AddrPort, 0, len(u.conns))
	for _, c := range u.conns {
		if addr, ok := c.LocalAddr().(*net.UDPAddr); ok {
			out = append(out, normalize(addr.AddrPort()))
		}
	}
	return out
}

// Port reports the bound UDP port.
func (u *UDP) Port() uint16 {
	if len(u.conns) == 0 {
		return 0
	}
	if addr, ok := u.conns[0].LocalAddr().(*net.UDPAddr); ok {
		return uint16(addr.Port)
	}
	return 0
}

// Close releases every socket.
func (u *UDP) Close() error {
	u.closeOnce.Do(func() {
		close(u.closed)
		for _, c := range u.conns {
			if err := c.Close(); err != nil && u.closeErr == nil {
				u.closeErr = err
			}
		}
		u.readers.Wait()
	})
	return u.closeErr
}

func normalize(addr netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
}
