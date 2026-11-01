package control

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// punchRequest is either a request to a relay (HasAddr false) or the relay's
// delivery to one side (HasAddr true, Addr is the other side's observed
// endpoint).
type punchRequest struct {
	Target  uint64
	Nonce   [16]byte
	Addr    netip.AddrPort
	HasAddr bool
}

const maxPunchPayload = 64

func encodePunch(p punchRequest) []byte {
	buf := make([]byte, 0, maxPunchPayload)
	buf = binary.BigEndian.AppendUint64(buf, p.Target)
	buf = append(buf, p.Nonce[:]...)
	if !p.HasAddr {
		return append(buf, 0)
	}
	addr := netip.AddrPortFrom(p.Addr.Addr().Unmap(), p.Addr.Port())
	buf = append(buf, 1)
	if addr.Addr().Is4() {
		v4 := addr.Addr().As4()
		buf = append(buf, 4)
		buf = append(buf, v4[:]...)
	} else {
		v6 := addr.Addr().As16()
		buf = append(buf, 6)
		buf = append(buf, v6[:]...)
	}
	return binary.BigEndian.AppendUint16(buf, addr.Port())
}

func decodePunch(payload []byte) (punchRequest, error) {
	if len(payload) < 25 {
		return punchRequest{}, errors.New("control: punch payload is truncated")
	}
	req := punchRequest{Target: binary.BigEndian.Uint64(payload[:8])}
	copy(req.Nonce[:], payload[8:24])
	switch payload[24] {
	case 0:
		if len(payload) != 25 {
			return punchRequest{}, errors.New("control: punch request has trailing bytes")
		}
		return req, nil
	case 1:
		rest := payload[25:]
		if len(rest) < 1 {
			return punchRequest{}, errors.New("control: punch delivery is truncated")
		}
		req.HasAddr = true
		switch rest[0] {
		case 4:
			if len(rest) != 7 {
				return punchRequest{}, errors.New("control: punch IPv4 delivery has the wrong length")
			}
			req.Addr = netip.AddrPortFrom(netip.AddrFrom4([4]byte(rest[1:5])), binary.BigEndian.Uint16(rest[5:7]))
		case 6:
			if len(rest) != 19 {
				return punchRequest{}, errors.New("control: punch IPv6 delivery has the wrong length")
			}
			req.Addr = netip.AddrPortFrom(netip.AddrFrom16([16]byte(rest[1:17])), binary.BigEndian.Uint16(rest[17:19]))
		default:
			return punchRequest{}, fmt.Errorf("control: unknown punch address family %d", rest[0])
		}
		req.Addr = netip.AddrPortFrom(req.Addr.Addr().Unmap(), req.Addr.Port())
		if req.Addr.Port() == 0 || req.Addr.Addr().IsUnspecified() || req.Addr.Addr().IsMulticast() {
			return punchRequest{}, fmt.Errorf("control: invalid punch endpoint %s", req.Addr)
		}
		return req, nil
	default:
		return punchRequest{}, fmt.Errorf("control: unknown punch flags %d", payload[24])
	}
}
