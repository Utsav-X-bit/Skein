package identity

import (
	"bytes"
	"crypto/ecdh"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateProducesMatchingKeypair(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(id.PrivateKey) != KeySize || len(id.PublicKey) != KeySize {
		t.Fatalf("key sizes = %d/%d, want %d", len(id.PrivateKey), len(id.PublicKey), KeySize)
	}
	priv, err := ecdh.X25519().NewPrivateKey(id.PrivateKey)
	if err != nil {
		t.Fatalf("private key rejected: %v", err)
	}
	if !bytes.Equal(priv.PublicKey().Bytes(), id.PublicKey) {
		t.Fatal("public key does not match private key")
	}
	want, err := PeerID(id.PublicKey)
	if err != nil {
		t.Fatalf("PeerID: %v", err)
	}
	if id.ID != want {
		t.Fatalf("ID = %016x, want %016x", id.ID, want)
	}
}

func TestPeerIDIsDeterministicAndRejectsWrongSize(t *testing.T) {
	pub := bytes.Repeat([]byte{0xab}, KeySize)
	a, err := PeerID(pub)
	if err != nil {
		t.Fatalf("PeerID: %v", err)
	}
	b, _ := PeerID(pub)
	if a != b {
		t.Fatal("PeerID is not deterministic")
	}
	if _, err := PeerID(pub[:31]); err == nil {
		t.Fatal("expected error for short public key")
	}
}

func TestLoadRejectsWrongLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id.key")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for short identity file")
	}
}

func TestSaveIsExclusiveAndRoundTrips(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity.key")
	if err := Save(path, id); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want 600", perm)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != id.ID {
		t.Fatalf("loaded ID = %016x, want %016x", loaded.ID, id.ID)
	}
	if err := Save(path, id); err == nil {
		t.Fatal("expected Save to refuse overwriting an existing identity")
	}
}

func TestInviteRoundTrip(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	inv := Invite{
		ID:        id.ID,
		PublicKey: id.PublicKey,
		Addrs: []netip.AddrPort{
			netip.MustParseAddrPort("203.0.113.5:11010"),
			netip.MustParseAddrPort("[2406:b400:72:4f1e::1]:11010"),
		},
	}
	s, err := EncodeInvite(inv)
	if err != nil {
		t.Fatalf("EncodeInvite: %v", err)
	}
	if !strings.HasPrefix(s, "skein:1:") {
		t.Fatalf("invite %q lacks skein:1: prefix", s)
	}
	got, err := DecodeInvite(s)
	if err != nil {
		t.Fatalf("DecodeInvite: %v", err)
	}
	if got.ID != inv.ID || !bytes.Equal(got.PublicKey, inv.PublicKey) {
		t.Fatal("decoded identity mismatch")
	}
	if len(got.Addrs) != 2 || got.Addrs[0] != inv.Addrs[0] || got.Addrs[1] != inv.Addrs[1] {
		t.Fatalf("decoded addrs = %v, want %v", got.Addrs, inv.Addrs)
	}
}

func TestInviteRejectsBadInput(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	good, err := EncodeInvite(Invite{ID: id.ID, PublicKey: id.PublicKey})
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"no prefix":      strings.TrimPrefix(good, "skein:1:"),
		"version 2":      "skein:2:" + strings.TrimPrefix(good, "skein:1:"),
		"bad base64":     "skein:1:!!!!",
		"truncated":      "skein:1:AAAA",
		"trailing bytes": good + "AAAA",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeInvite(in); err == nil {
				t.Fatalf("expected error for %q", in)
			}
		})
	}

	if _, err := EncodeInvite(Invite{ID: id.ID + 1, PublicKey: id.PublicKey}); err == nil {
		t.Fatal("expected ID/public-key mismatch to be rejected")
	}
	if _, err := EncodeInvite(Invite{PublicKey: id.PublicKey, Addrs: []netip.AddrPort{
		netip.MustParseAddrPort("0.0.0.0:11010"),
	}}); err == nil {
		t.Fatal("expected unspecified address to be rejected")
	}
	if _, err := EncodeInvite(Invite{PublicKey: id.PublicKey, Addrs: []netip.AddrPort{
		netip.MustParseAddrPort("239.1.1.1:11010"),
	}}); err == nil {
		t.Fatal("expected multicast address to be rejected")
	}
	if _, err := EncodeInvite(Invite{PublicKey: id.PublicKey, Addrs: []netip.AddrPort{
		netip.MustParseAddrPort("203.0.113.5:0"),
	}}); err == nil {
		t.Fatal("expected zero port to be rejected")
	}
	addrs := make([]netip.AddrPort, MaxEndpoints+1)
	for i := range addrs {
		addrs[i] = netip.MustParseAddrPort("203.0.113.5:11010")
	}
	if _, err := EncodeInvite(Invite{PublicKey: id.PublicKey, Addrs: addrs}); err == nil {
		t.Fatal("expected too many endpoints to be rejected")
	}
}
