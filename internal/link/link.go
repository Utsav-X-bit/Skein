// Package link defines the datagram transport abstraction used by the
// dataplane and provides its UDP implementation.
package link

import (
	"errors"
	"net/netip"
)

// MaxDatagram is the largest frame the transport carries.
const MaxDatagram = 1400

// ErrTruncated reports a datagram larger than the receive buffer.
var ErrTruncated = errors.New("link: datagram truncated")

// Link is a datagram transport. Send and Recv are safe for concurrent use.
type Link interface {
	// Send transmits payload to addr.
	Send(addr netip.AddrPort, payload []byte) error
	// Recv reads one datagram into buf and reports its source.
	Recv(buf []byte) (int, netip.AddrPort, error)
	// Observe reports the public endpoint seen by a rendezvous peer. It is
	// unavailable in v1 because there is no rendezvous service.
	Observe() (netip.AddrPort, error)
	// LocalAddrs lists the bound socket addresses.
	LocalAddrs() []netip.AddrPort
	// Close releases the sockets.
	Close() error
}
