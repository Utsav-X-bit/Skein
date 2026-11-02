package link

import (
	"errors"
	"net"
	"net/netip"
	"sync"
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

	// forced marks addresses the fallback was explicitly dialed for. UDP
	// frames from an address clear it: a working UDP path always wins.
	mu      sync.Mutex
	forced  map[netip.Addr]bool
	dialing map[netip.Addr]bool
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
	}
	s.wg.Add(1)
	go s.readUDP()
	if tcp != nil {
		s.wg.Add(1)
		go s.readTCP()
	}
	return s, nil
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

// Send prefers UDP, uses TCP only for an address whose fallback was dialed
// and which has not been heard from over UDP, and dials TCP when UDP fails.
func (s *Switch) Send(addr netip.AddrPort, payload []byte) error {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	s.mu.Lock()
	forced := s.forced[addr.Addr()]
	s.mu.Unlock()
	if forced && s.tcp != nil && s.tcp.HasConn(addr) {
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
	s.mu.Lock()
	forced := s.forced[addr.Addr()]
	s.mu.Unlock()
	if forced && s.tcp != nil && s.tcp.HasConn(addr) {
		return "tcp"
	}
	return "udp"
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
