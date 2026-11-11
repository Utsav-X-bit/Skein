// Package wire implements the fixed Skein outer frame format.
//
// Every frame is:
//
//	[type:1][to_next:8][to_final:8][from:8][ttl:1] payload...
//
// The 26-byte header is authenticated as AEAD associated data (except the
// relay-mutable to_next and ttl fields). Encrypted frames carry
// [key_epoch:4][counter:8] before the ciphertext and a 16-byte AEAD tag.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MessageType identifies a frame kind.
type MessageType uint8

const (
	// Handshake carries a Noise handshake message in plaintext.
	Handshake MessageType = 1
	// Data carries an encrypted overlay IP packet.
	Data MessageType = 2
	// Ping is an encrypted keepalive probe.
	Ping MessageType = 3
	// Pong answers a Ping.
	Pong MessageType = 4
	// State carries an encrypted peer/route advertisement.
	State MessageType = 5
	// Challenge proves reachability of a candidate endpoint.
	Challenge MessageType = 6
	// ChallengeReply answers a Challenge over the probed path.
	ChallengeReply MessageType = 7
	// Confirm completes a handshake by proving the new session is live.
	Confirm MessageType = 8
	// Punch asks a relay to introduce two peers so they can dial each other.
	Punch MessageType = 9
	// MTUProbe carries a padded path-MTU probe; the peer acknowledges it.
	MTUProbe MessageType = 10
	// MTUProbeAck answers an MTUProbe with the probe identifier.
	MTUProbeAck MessageType = 11
)

// Frame layout constants.
const (
	HeaderSize     = 26
	MaxFrameSize   = 1400
	MaxPayload     = MaxFrameSize - HeaderSize
	EpochSize      = 4
	CounterSize    = 8
	NonceSize      = EpochSize + CounterSize
	TagSize        = 16
	Overhead       = HeaderSize + NonceSize + TagSize
	MaxTTL         = 4
	maxPayloadBody = MaxPayload - NonceSize - TagSize
)

// Header is the plaintext routing header of every frame.
type Header struct {
	Type    MessageType
	ToNext  uint64
	ToFinal uint64
	From    uint64
	TTL     uint8
}

// Encrypted reports whether the frame body is a Noise ciphertext envelope.
func (t MessageType) Encrypted() bool {
	switch t {
	case Data, Ping, Pong, State, Challenge, ChallengeReply, Confirm, Punch, MTUProbe, MTUProbeAck:
		return true
	case Handshake:
		return false
	default:
		return false
	}
}

// Valid reports whether t is a known message type.
func (t MessageType) Valid() bool {
	return t >= Handshake && t <= MTUProbeAck
}

// AppendFrame appends a complete frame to dst and returns the extended slice.
func AppendFrame(dst []byte, h Header, payload []byte) ([]byte, error) {
	if err := validate(h, len(payload)); err != nil {
		return nil, err
	}
	start := len(dst)
	dst = append(dst, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	frame := dst[start:]
	frame[0] = byte(h.Type)
	binary.BigEndian.PutUint64(frame[1:9], h.ToNext)
	binary.BigEndian.PutUint64(frame[9:17], h.ToFinal)
	binary.BigEndian.PutUint64(frame[17:25], h.From)
	frame[25] = h.TTL
	return append(dst, payload...), nil
}

// Marshal returns a newly allocated frame.
func Marshal(h Header, payload []byte) ([]byte, error) {
	return AppendFrame(make([]byte, 0, HeaderSize+len(payload)), h, payload)
}

func validate(h Header, payloadLen int) error {
	if !h.Type.Valid() {
		return fmt.Errorf("unknown message type %d", h.Type)
	}
	if h.ToNext == 0 || h.ToFinal == 0 || h.From == 0 {
		return errors.New("frame peer IDs must not be zero")
	}
	if h.TTL < 1 || h.TTL > MaxTTL {
		return fmt.Errorf("ttl %d outside 1..%d", h.TTL, MaxTTL)
	}
	if payloadLen > MaxPayload {
		return fmt.Errorf("payload %d bytes exceeds maximum %d", payloadLen, MaxPayload)
	}
	if h.Type.Encrypted() {
		if payloadLen < NonceSize+TagSize {
			return fmt.Errorf("%d-byte payload is too short for an encrypted frame", payloadLen)
		}
		if payloadLen > NonceSize+TagSize+maxPayloadBody {
			return fmt.Errorf("encrypted payload %d bytes exceeds maximum %d", payloadLen, NonceSize+TagSize+maxPayloadBody)
		}
	}
	return nil
}

// Parse validates b and returns its header and payload. The payload aliases b
// and is never copied.
func Parse(b []byte) (Header, []byte, error) {
	if len(b) < HeaderSize {
		return Header{}, nil, fmt.Errorf("frame is %d bytes, shorter than the %d-byte header", len(b), HeaderSize)
	}
	if len(b) > MaxFrameSize {
		return Header{}, nil, fmt.Errorf("frame is %d bytes, maximum is %d", len(b), MaxFrameSize)
	}
	h := Header{
		Type:    MessageType(b[0]),
		ToNext:  binary.BigEndian.Uint64(b[1:9]),
		ToFinal: binary.BigEndian.Uint64(b[9:17]),
		From:    binary.BigEndian.Uint64(b[17:25]),
		TTL:     b[25],
	}
	payload := b[HeaderSize:]
	if err := validate(h, len(payload)); err != nil {
		return Header{}, nil, err
	}
	return h, payload, nil
}

// SetToNext rewrites the relay-mutable next-hop field in place.
func SetToNext(frame []byte, id uint64) error {
	if len(frame) < HeaderSize {
		return errors.New("frame is too short")
	}
	if id == 0 {
		return errors.New("next hop must not be zero")
	}
	binary.BigEndian.PutUint64(frame[1:9], id)
	return nil
}

// SetTTL rewrites the relay-mutable TTL field in place.
func SetTTL(frame []byte, ttl uint8) error {
	if len(frame) < HeaderSize {
		return errors.New("frame is too short")
	}
	if ttl < 1 || ttl > MaxTTL {
		return fmt.Errorf("ttl %d outside 1..%d", ttl, MaxTTL)
	}
	frame[25] = ttl
	return nil
}

// AppendNonce appends the explicit [epoch:4][counter:8] nonce prefix.
func AppendNonce(dst []byte, epoch uint32, counter uint64) []byte {
	dst = binary.BigEndian.AppendUint32(dst, epoch)
	return binary.BigEndian.AppendUint64(dst, counter)
}

// ParseNonce splits an encrypted payload into its nonce prefix and ciphertext.
func ParseNonce(payload []byte) (epoch uint32, counter uint64, ciphertext []byte, err error) {
	if len(payload) < NonceSize+TagSize {
		return 0, 0, nil, errors.New("payload is too short for a nonce and tag")
	}
	epoch = binary.BigEndian.Uint32(payload[0:4])
	counter = binary.BigEndian.Uint64(payload[4:12])
	return epoch, counter, payload[NonceSize:], nil
}
