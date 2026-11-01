package link

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

// TCP carries the same frames over a length-prefixed stream, for networks that
// block UDP. Connections are keyed by the remote address so a reply to the
// source of an accepted connection reuses it.
type TCP struct {
	ln net.Listener

	mu    sync.Mutex
	conns map[netip.AddrPort]*tcpConn

	packets chan received
	closed  chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

type tcpConn struct {
	conn net.Conn
	addr netip.AddrPort
	// mu serialises frames: the stream has no datagram boundaries, so two
	// concurrent Send calls would interleave their headers and payloads.
	mu sync.Mutex
}

// ListenTCP accepts framed connections on port.
func ListenTCP(port uint16) (*TCP, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	t := &TCP{
		ln:      ln,
		conns:   make(map[netip.AddrPort]*tcpConn),
		packets: make(chan received, 256),
		closed:  make(chan struct{}),
	}
	t.wg.Add(1)
	go t.acceptLoop()
	return t, nil
}

func (t *TCP) acceptLoop() {
	defer t.wg.Done()
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			select {
			case <-t.closed:
				return
			default:
			}
			return
		}
		addr, ok := addrOf(conn.RemoteAddr())
		if !ok {
			conn.Close()
			continue
		}
		t.track(addr, conn)
	}
}

func addrOf(a net.Addr) (netip.AddrPort, bool) {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return netip.AddrPort{}, false
	}
	addr, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(addr.Unmap(), uint16(tcp.Port)), true
}

func (t *TCP) track(addr netip.AddrPort, conn net.Conn) {
	t.mu.Lock()
	if old, ok := t.conns[addr]; ok {
		old.conn.Close()
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		// Nagle would hold the payload until the header is acknowledged,
		// turning every frame into one RTT.
		_ = tc.SetNoDelay(true)
	}
	t.conns[addr] = &tcpConn{conn: conn, addr: addr}
	t.mu.Unlock()

	t.wg.Add(1)
	go t.readLoop(addr, conn)
}

// Dial connects to addr if no connection exists yet.
func (t *TCP) Dial(addr netip.AddrPort) error {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	t.mu.Lock()
	_, exists := t.conns[addr]
	t.mu.Unlock()
	if exists {
		return nil
	}
	conn, err := net.DialTimeout("tcp", addr.String(), 3*time.Second)
	if err != nil {
		return err
	}
	t.track(addr, conn)
	return nil
}

// HasConn reports whether a live connection to addr exists.
func (t *TCP) HasConn(addr netip.AddrPort) bool {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.conns[addr]
	return ok
}

// Send writes one framed payload to addr.
func (t *TCP) Send(addr netip.AddrPort, payload []byte) error {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	if len(payload) > MaxDatagram {
		return fmt.Errorf("link: datagram of %d bytes exceeds %d", len(payload), MaxDatagram)
	}
	t.mu.Lock()
	c, ok := t.conns[addr]
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("link: no TCP connection to %s", addr)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))

	// One writev for header and payload: two writes let Nagle hold the
	// payload until the header is acknowledged.
	c.mu.Lock()
	bufs := net.Buffers{hdr[:], payload}
	_, err := bufs.WriteTo(c.conn)
	c.mu.Unlock()
	if err != nil {
		t.drop(addr, c)
		return err
	}
	return nil
}

func (t *TCP) readLoop(addr netip.AddrPort, conn net.Conn) {
	defer t.wg.Done()
	var hdr [4]byte
	buf := make([]byte, MaxDatagram)
	for {
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			t.drop(addr, &tcpConn{conn: conn, addr: addr})
			return
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n < wireHeaderSize || n > MaxDatagram {
			t.drop(addr, &tcpConn{conn: conn, addr: addr})
			return
		}
		if _, err := io.ReadFull(conn, buf[:n]); err != nil {
			t.drop(addr, &tcpConn{conn: conn, addr: addr})
			return
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		select {
		case t.packets <- received{data: data, from: addr}:
		case <-t.closed:
			return
		default:
			// Saturated: drop like a congested network.
		}
	}
}

func (t *TCP) drop(addr netip.AddrPort, c *tcpConn) {
	t.mu.Lock()
	if cur, ok := t.conns[addr]; ok && cur.conn == c.conn {
		delete(t.conns, addr)
	}
	t.mu.Unlock()
	c.conn.Close()
}

// Recv returns one framed payload.
func (t *TCP) Recv(buf []byte) (int, netip.AddrPort, error) {
	select {
	case pkt := <-t.packets:
		if len(pkt.data) > len(buf) {
			return 0, pkt.from, ErrTruncated
		}
		return copy(buf, pkt.data), pkt.from, nil
	case <-t.closed:
		return 0, netip.AddrPort{}, net.ErrClosed
	}
}

// LocalAddrs reports the bound listener address.
func (t *TCP) LocalAddrs() []netip.AddrPort {
	if addr, ok := addrOf(t.ln.Addr()); ok {
		return []netip.AddrPort{addr}
	}
	return nil
}

// Close stops the listener and every connection.
func (t *TCP) Close() error {
	var err error
	t.once.Do(func() {
		close(t.closed)
		err = t.ln.Close()
		t.mu.Lock()
		for addr, c := range t.conns {
			c.conn.Close()
			delete(t.conns, addr)
		}
		t.mu.Unlock()
		t.wg.Wait()
	})
	return err
}

// wireHeaderSize is the smallest plausible frame, used to reject garbage.
const wireHeaderSize = 26

var errTCPDisabled = errors.New("link: TCP transport is not listening")
