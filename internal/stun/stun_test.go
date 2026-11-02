package stun

import (
	"context"
	"encoding/binary"
	"net/netip"
	"os"
	"testing"
)

func TestParseXorMappedAddress(t *testing.T) {
	var txID [12]byte
	for i := range txID {
		txID[i] = byte(i + 1)
	}
	want := netip.MustParseAddrPort("203.0.113.7:54321")
	msg := EncodeResponse(txID, want, true)
	got, err := parse(msg, txID)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != want.Addr() {
		t.Fatalf("address = %s, want %s", got, want.Addr())
	}
}

func TestParseIPv6AndPlainMapped(t *testing.T) {
	var txID [12]byte
	want := netip.MustParseAddr("2001:db8::5")
	msg := EncodeResponse(txID, netip.AddrPortFrom(want, 11010), true)
	got, err := parse(msg, txID)
	if err != nil || got != want {
		t.Fatalf("v6: got %s err %v", got, err)
	}
	plain := EncodeResponse(txID, netip.MustParseAddrPort("198.51.100.9:11010"), false)
	got, err = parse(plain, txID)
	if err != nil || got.String() != "198.51.100.9" {
		t.Fatalf("plain: got %s err %v", got, err)
	}
}

func TestParseRejectsBadResponses(t *testing.T) {
	var txID [12]byte
	good := EncodeResponse(txID, netip.MustParseAddrPort("203.0.113.7:11010"), true)
	other := txID
	other[0] ^= 0xff

	if _, err := parse(good[:10], txID); err == nil {
		t.Fatal("expected a short response to be rejected")
	}
	if _, err := parse(good, other); err == nil {
		t.Fatal("expected a transaction mismatch to be rejected")
	}
	tampered := append([]byte(nil), good...)
	tampered[0] = 0x00
	if _, err := parse(tampered, txID); err == nil {
		t.Fatal("expected a wrong message type to be rejected")
	}
}

// TestLiveSTUN talks to the real service; run with SKEIN_STUN_LIVE=1.
func TestLiveSTUN6(t *testing.T) {
	if os.Getenv("SKEIN_STUN_LIVE") == "" {
		t.Skip("set SKEIN_STUN_LIVE=1 to query a public STUN server")
	}
	ip, err := PublicIP(context.Background(), "stun.l.google.com:19302", "udp6")
	if err != nil {
		t.Skipf("no IPv6 path: %v", err)
	}
	t.Logf("public IPv6: %s", ip)
	if !ip.IsValid() || !ip.Is6() {
		t.Fatalf("got %s", ip)
	}
}

func TestLiveSTUN(t *testing.T) {
	if os.Getenv("SKEIN_STUN_LIVE") == "" {
		t.Skip("set SKEIN_STUN_LIVE=1 to query a public STUN server")
	}
	ip, err := PublicIP(context.Background(), "stun.l.google.com:19302", "udp4")
	if err != nil {
		t.Fatalf("PublicIP: %v", err)
	}
	t.Logf("public address: %s", ip)
	if !ip.IsValid() {
		t.Fatal("invalid address")
	}
}

func TestParseRejectsAttributeOverrun(t *testing.T) {
	var txID [12]byte
	msg := make([]byte, headerSize+4)
	binary.BigEndian.PutUint16(msg[0:2], bindingResponse)
	binary.BigEndian.PutUint16(msg[2:4], 4)
	binary.BigEndian.PutUint32(msg[4:8], magicCookie)
	copy(msg[8:20], txID[:])
	binary.BigEndian.PutUint16(msg[headerSize:headerSize+2], attrXorMapped)
	binary.BigEndian.PutUint16(msg[headerSize+2:headerSize+4], 0xffff)
	if _, err := parse(msg, txID); err == nil {
		t.Fatal("attribute overrun was accepted")
	}
}
