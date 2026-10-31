package wire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestRoundTripAllTypes(t *testing.T) {
	types := []MessageType{Handshake, Data, Ping, Pong, State, Challenge, ChallengeReply}
	for _, typ := range types {
		payload := []byte{0xde, 0xad, 0xbe, 0xef}
		if typ.Encrypted() {
			payload = AppendNonce(nil, 7, 99)
			payload = append(payload, bytes.Repeat([]byte{0xaa}, TagSize+4)...)
		}
		h := Header{Type: typ, ToNext: 0x0102030405060708, ToFinal: 0x1112131415161718, From: 0x2122232425262728, TTL: 3}
		frame, err := Marshal(h, payload)
		if err != nil {
			t.Fatalf("%v: Marshal: %v", typ, err)
		}
		got, gotPayload, err := Parse(frame)
		if err != nil {
			t.Fatalf("%v: Parse: %v", typ, err)
		}
		if got != h {
			t.Fatalf("%v: header = %+v, want %+v", typ, got, h)
		}
		if !bytes.Equal(gotPayload, payload) {
			t.Fatalf("%v: payload mismatch", typ)
		}
	}
}

func TestBigEndianLayout(t *testing.T) {
	frame, err := Marshal(Header{Type: Handshake, ToNext: 0x0102030405060708, ToFinal: 0x1112131415161718, From: 0x2122232425262728, TTL: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != HeaderSize {
		t.Fatalf("length = %d, want %d", len(frame), HeaderSize)
	}
	if frame[0] != byte(Handshake) || frame[25] != 4 {
		t.Fatalf("type/ttl layout wrong: %v", frame)
	}
	if got := binary.BigEndian.Uint64(frame[1:9]); got != 0x0102030405060708 {
		t.Fatalf("to_next = %016x", got)
	}
	if got := binary.BigEndian.Uint64(frame[9:17]); got != 0x1112131415161718 {
		t.Fatalf("to_final = %016x", got)
	}
	if got := binary.BigEndian.Uint64(frame[17:25]); got != 0x2122232425262728 {
		t.Fatalf("from = %016x", got)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	valid, err := Marshal(Header{Type: Data, ToNext: 1, ToFinal: 2, From: 3, TTL: 4}, append(AppendNonce(nil, 1, 2), make([]byte, TagSize)...))
	if err != nil {
		t.Fatal(err)
	}
	short := valid[:HeaderSize-1]
	oversized := make([]byte, MaxFrameSize+1)

	badType := append([]byte(nil), valid...)
	badType[0] = 200
	zeroID := append([]byte(nil), valid...)
	binary.BigEndian.PutUint64(zeroID[1:9], 0)
	badTTL := append([]byte(nil), valid...)
	badTTL[25] = 0
	tooHighTTL := append([]byte(nil), valid...)
	tooHighTTL[25] = MaxTTL + 1
	truncatedEncrypted := append([]byte(nil), valid[:HeaderSize+NonceSize+TagSize-1]...)

	cases := map[string][]byte{
		"short":               short,
		"oversized":           oversized,
		"unknown type":        badType,
		"zero to_next":        zeroID,
		"zero ttl":            badTTL,
		"ttl above maximum":   tooHighTTL,
		"encrypted too short": truncatedEncrypted,
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Parse(frame); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

func TestMarshalRejectsOversizedPayload(t *testing.T) {
	payload := append(AppendNonce(nil, 1, 1), make([]byte, maxPayloadBody+1+TagSize)...)
	if _, err := Marshal(Header{Type: Data, ToNext: 1, ToFinal: 2, From: 3, TTL: 4}, payload); err == nil {
		t.Fatal("expected oversized payload to be rejected")
	}
	if _, err := Marshal(Header{Type: Handshake, ToNext: 1, ToFinal: 2, From: 3, TTL: 4}, make([]byte, MaxPayload+1)); err == nil {
		t.Fatal("expected oversized handshake payload to be rejected")
	}
}

func TestNonceRoundTrip(t *testing.T) {
	payload := AppendNonce(nil, 0xdeadbeef, 0x0102030405060708)
	payload = append(payload, make([]byte, TagSize)...)
	epoch, counter, ct, err := ParseNonce(payload)
	if err != nil {
		t.Fatal(err)
	}
	if epoch != 0xdeadbeef || counter != 0x0102030405060708 || len(ct) != TagSize {
		t.Fatalf("nonce = %x/%d ct=%d", epoch, counter, len(ct))
	}
}

func TestRelayMutableFields(t *testing.T) {
	frame, err := Marshal(Header{Type: Data, ToNext: 1, ToFinal: 2, From: 3, TTL: 4}, append(AppendNonce(nil, 1, 1), make([]byte, TagSize)...))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetToNext(frame, 9); err != nil {
		t.Fatal(err)
	}
	if err := SetTTL(frame, 3); err != nil {
		t.Fatal(err)
	}
	h, _, err := Parse(frame)
	if err != nil {
		t.Fatal(err)
	}
	if h.ToNext != 9 || h.TTL != 3 || h.ToFinal != 2 || h.From != 3 {
		t.Fatalf("header = %+v", h)
	}
	if err := SetToNext(frame, 0); err == nil {
		t.Fatal("expected zero next hop to be rejected")
	}
	if err := SetTTL(frame, 0); err == nil {
		t.Fatal("expected zero ttl to be rejected")
	}
}
