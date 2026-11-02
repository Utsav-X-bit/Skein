// Package stun implements the small part of STUN needed to learn a node's
// public address: one binding request and an XOR-MAPPED-ADDRESS reply.
package stun

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

const (
	bindingRequest  = 0x0001
	bindingResponse = 0x0101
	magicCookie     = 0x2112A442
	attrMapped      = 0x0001
	attrXorMapped   = 0x0020
	headerSize      = 20
)

// PublicIP asks server (host:port) which address it sees for this node,
// using network "udp4" or "udp6" so each address family is probed separately.
func PublicIP(ctx context.Context, server, network string) (netip.Addr, error) {
	addr, err := net.ResolveUDPAddr(network, server)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("stun: resolve %s: %w", server, err)
	}
	conn, err := net.DialUDP(network, nil, addr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("stun: dial %s: %w", server, err)
	}
	defer conn.Close()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Second)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return netip.Addr{}, err
	}

	var txID [12]byte
	if _, err := rand.Read(txID[:]); err != nil {
		return netip.Addr{}, err
	}
	req := make([]byte, headerSize)
	binary.BigEndian.PutUint16(req[0:2], bindingRequest)
	binary.BigEndian.PutUint16(req[2:4], 0)
	binary.BigEndian.PutUint32(req[4:8], magicCookie)
	copy(req[8:20], txID[:])
	if _, err := conn.Write(req); err != nil {
		return netip.Addr{}, err
	}

	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("stun: read: %w", err)
	}
	return parse(buf[:n], txID)
}

func parse(msg []byte, txID [12]byte) (netip.Addr, error) {
	if len(msg) < headerSize {
		return netip.Addr{}, errors.New("stun: short response")
	}
	if binary.BigEndian.Uint16(msg[0:2]) != bindingResponse {
		return netip.Addr{}, fmt.Errorf("stun: unexpected message type %#x", binary.BigEndian.Uint16(msg[0:2]))
	}
	if binary.BigEndian.Uint32(msg[4:8]) != magicCookie {
		return netip.Addr{}, errors.New("stun: bad magic cookie")
	}
	if string(msg[8:20]) != string(txID[:]) {
		return netip.Addr{}, errors.New("stun: transaction ID mismatch")
	}
	length := int(binary.BigEndian.Uint16(msg[2:4]))
	if headerSize+length > len(msg) {
		return netip.Addr{}, errors.New("stun: truncated attributes")
	}

	end := headerSize + length
	off := headerSize
	for off+4 <= end {
		attrType := binary.BigEndian.Uint16(msg[off : off+2])
		attrLen := int(binary.BigEndian.Uint16(msg[off+2 : off+4]))
		if off+4+attrLen > end || off+4+attrLen > len(msg) {
			return netip.Addr{}, errors.New("stun: attribute overruns message")
		}
		value := msg[off+4 : off+4+attrLen]
		switch attrType {
		case attrXorMapped:
			return decodeAddr(value, txID, true)
		case attrMapped:
			return decodeAddr(value, txID, false)
		}
		off += 4 + attrLen
		off += (4 - attrLen%4) % 4
	}
	return netip.Addr{}, errors.New("stun: no mapped address in response")
}

func decodeAddr(value []byte, txID [12]byte, xor bool) (netip.Addr, error) {
	if len(value) < 4 {
		return netip.Addr{}, errors.New("stun: short mapped address")
	}
	family := value[1]
	port := binary.BigEndian.Uint16(value[2:4])
	if xor {
		port ^= uint16(magicCookie >> 16)
	}
	switch family {
	case 0x01:
		if len(value) < 8 {
			return netip.Addr{}, errors.New("stun: short IPv4 address")
		}
		var raw [4]byte
		copy(raw[:], value[4:8])
		if xor {
			var cookie [4]byte
			binary.BigEndian.PutUint32(cookie[:], magicCookie)
			for i := range raw {
				raw[i] ^= cookie[i]
			}
		}
		return netip.AddrPortFrom(netip.AddrFrom4(raw), port).Addr(), nil
	case 0x02:
		if len(value) < 20 {
			return netip.Addr{}, errors.New("stun: short IPv6 address")
		}
		var raw [16]byte
		copy(raw[:], value[4:20])
		if xor {
			var mask [16]byte
			binary.BigEndian.PutUint32(mask[0:4], magicCookie)
			copy(mask[4:], txID[:])
			for i := range raw {
				raw[i] ^= mask[i]
			}
		}
		return netip.AddrFrom16(raw), nil
	default:
		return netip.Addr{}, fmt.Errorf("stun: unknown address family %d", family)
	}
}

// EncodeResponse builds a binding response for tests.
func EncodeResponse(txID [12]byte, addr netip.AddrPort, xor bool) []byte {
	value := make([]byte, 0, 20)
	value = append(value, 0)
	if addr.Addr().Is4() {
		value = append(value, 0x01)
	} else {
		value = append(value, 0x02)
	}
	port := addr.Port()
	if xor {
		port ^= uint16(magicCookie >> 16)
	}
	value = binary.BigEndian.AppendUint16(value, port)
	raw := addr.Addr().AsSlice()
	if xor {
		if addr.Addr().Is4() {
			var cookie [4]byte
			binary.BigEndian.PutUint32(cookie[:], magicCookie)
			for i := range raw {
				raw[i] ^= cookie[i]
			}
		} else {
			var mask [16]byte
			binary.BigEndian.PutUint32(mask[0:4], magicCookie)
			copy(mask[4:], txID[:])
			for i := range raw {
				raw[i] ^= mask[i]
			}
		}
	}
	value = append(value, raw...)

	attrType := uint16(attrMapped)
	if xor {
		attrType = attrXorMapped
	}
	attr := make([]byte, 4, 4+len(value)+3)
	binary.BigEndian.PutUint16(attr[0:2], attrType)
	binary.BigEndian.PutUint16(attr[2:4], uint16(len(value)))
	attr = append(attr, value...)
	for len(attr)%4 != 0 {
		attr = append(attr, 0)
	}

	msg := make([]byte, headerSize, headerSize+len(attr))
	binary.BigEndian.PutUint16(msg[0:2], bindingResponse)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(attr)))
	binary.BigEndian.PutUint32(msg[4:8], magicCookie)
	copy(msg[8:20], txID[:])
	return append(msg, attr...)
}
