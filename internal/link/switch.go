package link

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Switch carries frames over UDP and falls back to TCP for peers that cannot
// receive UDP. TCP is used for an address once a connection to it exists.
type Switch struct {
	udp *UDP
	tcp *TCP

	packets chan received
	closed  chan struct{}
	once    sync.Once
	wg      sync.WaitGroup

	// forced marks addresses the fallback was explicitly dialed for, and
	// udpSeen records when a datagram last arrived from an address. A
	// datagram proves the peer's UDP path works, so it clears forced and
	// keeps UDP preferred over any fallback stream for a while.
	mu      sync.Mutex
	forced  map[netip.Addr]bool
	dialing map[netip.Addr]bool
	udpSeen map[netip.Addr]time.Time
}

// udpPreferenceWindow is how long a received datagram keeps UDP preferred
// over an existing TCP fallback. Every inbound datagram refreshes it, so a
// live UDP path never loses to a stale stream; a peer that really needs TCP
// sends nothing over UDP and reverts once the window lapses.
const udpPreferenceWindow = 30 * time.Second

// udpProven reports whether a datagram arrived from ip recently enough to
// prefer UDP over an existing fallback stream.
func (s *Switch) udpProven(ip netip.Addr) bool {
	s.mu.Lock()
	at, ok := s.udpSeen[ip]
	s.mu.Unlock()
	return ok && time.Since(at) < udpPreferenceWindow
}

// Listen binds UDP and, when possible, TCP on the same port.
func Listen(port uint16) (*Switch, error) {
	udp, err := ListenUDP(port)
	if err != nil {
		return nil, err
	}
	// Bind TCP on the port UDP actually got, so both transports share an
	// address even when the requested port was 0.
	tcp, tcpErr := ListenTCP(udp.Port())
	if tcpErr != nil {
		// TCP is a fallback; keep running on UDP alone.
		tcp = nil
	}
	s := &Switch{
		udp:     udp,
		tcp:     tcp,
		packets: make(chan received, 256),
		closed:  make(chan struct{}),
		forced:  make(map[netip.Addr]bool),
		dialing: make(map[netip.Addr]bool),
		udpSeen: make(map[netip.Addr]time.Time),
	}
	s.wg.Add(1)
	go s.readUDP()
	if tcp != nil {
		s.wg.Add(1)
		go s.readTCP()
	}
	return s, nil
}

// transientLinkError reports a recoverable kernel resource error.
func transientLinkError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no buffer space") ||
		strings.Contains(msg, "cannot allocate memory") ||
		strings.Contains(msg, "temporarily unavailable")
}

func (s *Switch) readUDP() {
	defer s.wg.Done()
	buf := make([]byte, MaxDatagram+1)
	for {
		n, from, err := s.udp.Recv(buf)
		if err != nil {
			if errors.Is(err, ErrTruncated) {
				continue
			}
			if transientLinkError(err) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			select {
			case <-s.closed:
				return
			default:
			}
			return
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		// A datagram from this peer proves its UDP path works, so drop any
		// fallback preference instead of staying on TCP forever.
		s.mu.Lock()
		delete(s.forced, from.Addr())
		s.udpSeen[from.Addr()] = time.Now()
		s.mu.Unlock()
		select {
		case s.packets <- received{data: data, from: from, transport: "udp"}:
		case <-s.closed:
			return
		default:
		}
	}
}

func (s *Switch) readTCP() {
	defer s.wg.Done()
	buf := make([]byte, MaxDatagram+1)
	for {
		n, from, err := s.tcp.Recv(buf)
		if err != nil {
			if errors.Is(err, ErrTruncated) {
				continue
			}
			if transientLinkError(err) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			select {
			case <-s.closed:
				return
			default:
			}
			return
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		select {
		case s.packets <- received{data: data, from: from, transport: "tcp"}:
		case <-s.closed:
			return
		default:
		}
	}
}

// Send prefers UDP when a datagram from the address proved that path works.
// Otherwise an existing TCP connection wins: any live stream to the exact
// address (including an accepted connection, whose key is the peer's
// ephemeral port) is used, and a single stream from the destination IP wins
// when sending to the peer's listen address. That is the M4 F-01 fix: the
// accept side previously fell through to UDP because `forced` is only set
// by an explicit DialTCP on the dial side, so handshake replies to a
// TCP-accepted peer were sent as UDP to a dead ephemeral port and lost
// silently. UDP is used only when no TCP connection exists, or when a
// datagram has since proved the UDP path.
func (s *Switch) Send(addr netip.AddrPort, payload []byte) error {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	proven := s.udpProven(addr.Addr())
	if s.tcp != nil && !proven {
		if s.tcp.HasConn(addr) {
			return s.tcp.Send(addr, payload)
		}
		if err := s.tcp.SendToIP(addr, payload); err == nil {
			return nil
		}
	}
	s.mu.Lock()
	forced := s.forced[addr.Addr()]
	s.mu.Unlock()
	if forced && !proven && s.tcp != nil && s.tcp.HasConn(addr) {
		return s.tcp.Send(addr, payload)
	}
	udpErr := s.udp.Send(addr, payload)
	if udpErr == nil {
		return nil
	}
	if s.tcp == nil {
		return udpErr
	}
	// Never block the packet loop on a 3 s TCP dial: start one in the
	// background and drop this frame; later frames use the stream.
	s.mu.Lock()
	if !s.dialing[addr.Addr()] {
		s.dialing[addr.Addr()] = true
		go func() {
			defer func() {
				s.mu.Lock()
				delete(s.dialing, addr.Addr())
				s.mu.Unlock()
			}()
			if err := s.tcp.Dial(addr); err == nil {
				s.mu.Lock()
				s.forced[addr.Addr()] = true
				s.mu.Unlock()
			}
		}()
	}
	s.mu.Unlock()
	return udpErr
}

// DialTCP opens a fallback connection to addr.
func (s *Switch) DialTCP(addr netip.AddrPort) error {
	if s.tcp == nil {
		return errTCPDisabled
	}
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	if err := s.tcp.Dial(addr); err != nil {
		return err
	}
	s.mu.Lock()
	s.forced[addr.Addr()] = true
	s.mu.Unlock()
	return nil
}

// TransportFor reports which transport a connection to addr uses.
func (s *Switch) TransportFor(addr netip.AddrPort) string {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	if s.udpProven(addr.Addr()) {
		return "udp"
	}
	if s.tcp != nil && (s.tcp.HasConn(addr) || s.tcp.HasConnForIP(addr)) {
		return "tcp"
	}
	return "udp"
}

// SendDirect sends one frame over UDP to a specific address, ignoring any
// fallback stream. Path proofs use it: a challenge carried by an existing
// TCP connection proves nothing about the address being tested.
func (s *Switch) SendDirect(addr netip.AddrPort, payload []byte) error {
	return s.udp.Send(netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port()), payload)
}

// HasTCP reports whether the TCP fallback is listening.
func (s *Switch) HasTCP() bool { return s.tcp != nil }

// Recv returns one frame from either transport.
func (s *Switch) Recv(buf []byte) (int, netip.AddrPort, error) {
	n, from, _, err := s.RecvTagged(buf)
	return n, from, err
}

// RecvTagged returns one frame and the transport that carried it.
func (s *Switch) RecvTagged(buf []byte) (int, netip.AddrPort, string, error) {
	select {
	case pkt := <-s.packets:
		if len(pkt.data) > len(buf) {
			return 0, pkt.from, pkt.transport, ErrTruncated
		}
		return copy(buf, pkt.data), pkt.from, pkt.transport, nil
	case <-s.closed:
		return 0, netip.AddrPort{}, "", net.ErrClosed
	}
}

// Observe is unavailable without a rendezvous service.
func (s *Switch) Observe() (netip.AddrPort, error) {
	return netip.AddrPort{}, errors.New("link: observe requires a rendezvous endpoint, which v1 does not provide")
}

// LocalAddrs reports the bound UDP and TCP addresses.
func (s *Switch) LocalAddrs() []netip.AddrPort {
	out := s.udp.LocalAddrs()
	if s.tcp != nil {
		out = append(out, s.tcp.LocalAddrs()...)
	}
	return out
}

// Port reports the bound UDP port.
func (s *Switch) Port() uint16 { return s.udp.Port() }

// Close releases both transports.
func (s *Switch) Close() error {
	var err error
	s.once.Do(func() {
		close(s.closed)
		if s.tcp != nil {
			_ = s.tcp.Close()
		}
		if e := s.udp.Close(); e != nil {
			err = e
		}
		s.wg.Wait()
	})
	return err
}
