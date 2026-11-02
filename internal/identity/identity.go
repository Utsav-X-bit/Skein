// Package identity holds the node's static X25519 keypair, its 64-bit peer ID,
// and the compact invite string used to share that identity out of band.
package identity

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"runtime"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

const (
	// KeySize is the size of an X25519 private or public key in bytes.
	KeySize = 32
	// MaxEndpoints is the maximum number of underlay endpoints carried in an invite.
	MaxEndpoints = 8

	invitePrefix   = "skein:1:"
	maxInviteBytes = 512
	inviteVersion  = 1
)

// Identity is a node's static keypair plus the peer ID derived from it.
type Identity struct {
	PrivateKey []byte
	PublicKey  []byte
	ID         uint64
}

// Invite is the shareable identity of a node: who it is and where it can be reached.
type Invite struct {
	ID        uint64
	PublicKey []byte
	Addrs     []netip.AddrPort
}

// Generate creates a fresh X25519 identity.
func Generate() (Identity, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate X25519 key: %w", err)
	}
	return fromECDH(priv)
}

// FromPrivateKey rebuilds an identity from a 32-byte X25519 private key.
func FromPrivateKey(priv []byte) (Identity, error) {
	if len(priv) != KeySize {
		return Identity{}, fmt.Errorf("private key must be %d bytes, got %d", KeySize, len(priv))
	}
	key, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return Identity{}, fmt.Errorf("invalid X25519 private key: %w", err)
	}
	return fromECDH(key)
}

func fromECDH(priv *ecdh.PrivateKey) (Identity, error) {
	pub := priv.PublicKey().Bytes()
	id, err := PeerID(pub)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		PrivateKey: priv.Bytes(),
		PublicKey:  pub,
		ID:         id,
	}, nil
}

// PeerID returns the first 8 bytes of SHA-256(publicKey) as a big-endian integer.
func PeerID(publicKey []byte) (uint64, error) {
	if len(publicKey) != KeySize {
		return 0, fmt.Errorf("public key must be %d bytes, got %d", KeySize, len(publicKey))
	}
	sum := sha256.Sum256(publicKey)
	id := binary.BigEndian.Uint64(sum[:8])
	if id == 0 {
		return 0, errors.New("derived peer ID is zero")
	}
	return id, nil
}

// Load reads a raw 32-byte private key from path.
func Load(path string) (Identity, error) {
	if info, err := os.Stat(path); err == nil && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return Identity{}, fmt.Errorf("identity file %s is accessible by group or others (mode %o); run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, err
	}
	if len(raw) != KeySize {
		return Identity{}, fmt.Errorf("identity file %s must contain exactly %d bytes, got %d", path, KeySize, len(raw))
	}
	return FromPrivateKey(raw)
}

// Save writes the private key to path, refusing to overwrite an existing file.
// The file is created owner-only: mode 0600 on Unix; on Windows, full control
// for the owner, SYSTEM (the service account), and Administrators.
func Save(path string, id Identity) error {
	if len(id.PrivateKey) != KeySize {
		return fmt.Errorf("private key must be %d bytes, got %d", KeySize, len(id.PrivateKey))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(id.PrivateKey); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	if err := fileperm.Restrict(path); err != nil {
		os.Remove(path)
		return fmt.Errorf("restrict identity file permissions: %w", err)
	}
	return nil
}

// EncodeInvite renders an invite as skein:1:<base64url-payload>.
func EncodeInvite(inv Invite) (string, error) {
	if len(inv.PublicKey) != KeySize {
		return "", fmt.Errorf("invite public key must be %d bytes", KeySize)
	}
	id, err := PeerID(inv.PublicKey)
	if err != nil {
		return "", err
	}
	if inv.ID != 0 && inv.ID != id {
		return "", fmt.Errorf("invite peer ID %016x does not match public key (%016x)", inv.ID, id)
	}
	if len(inv.Addrs) > MaxEndpoints {
		return "", fmt.Errorf("invite carries %d endpoints, maximum is %d", len(inv.Addrs), MaxEndpoints)
	}

	payload := make([]byte, 0, 1+8+KeySize+1+len(inv.Addrs)*19)
	payload = append(payload, inviteVersion)
	payload = binary.BigEndian.AppendUint64(payload, id)
	payload = append(payload, inv.PublicKey...)
	payload = append(payload, byte(len(inv.Addrs)))
	for _, addr := range inv.Addrs {
		encoded, err := appendAddrPort(nil, addr)
		if err != nil {
			return "", err
		}
		payload = append(payload, encoded...)
	}
	if len(payload) > maxInviteBytes {
		return "", fmt.Errorf("invite payload is %d bytes, maximum is %d", len(payload), maxInviteBytes)
	}
	return invitePrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

// DecodeInvite parses and validates a skein:1: invite string.
func DecodeInvite(s string) (Invite, error) {
	if len(s) < len(invitePrefix) || s[:len(invitePrefix)] != invitePrefix {
		return Invite{}, fmt.Errorf("invite must start with %q", invitePrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(s[len(invitePrefix):])
	if err != nil {
		return Invite{}, fmt.Errorf("invite is not valid base64url: %w", err)
	}
	if len(raw) > maxInviteBytes {
		return Invite{}, fmt.Errorf("invite payload is %d bytes, maximum is %d", len(raw), maxInviteBytes)
	}
	if len(raw) < 1+8+KeySize+1 {
		return Invite{}, errors.New("invite payload is truncated")
	}
	if raw[0] != inviteVersion {
		return Invite{}, fmt.Errorf("unsupported invite version %d", raw[0])
	}
	id := binary.BigEndian.Uint64(raw[1:9])
	pub := append([]byte(nil), raw[9:9+KeySize]...)
	derived, err := PeerID(pub)
	if err != nil {
		return Invite{}, err
	}
	if id != derived {
		return Invite{}, errors.New("invite peer ID does not match its public key")
	}
	count := int(raw[41])
	if count > MaxEndpoints {
		return Invite{}, fmt.Errorf("invite carries %d endpoints, maximum is %d", count, MaxEndpoints)
	}
	rest := raw[42:]
	addrs := make([]netip.AddrPort, 0, count)
	for i := range count {
		addr, n, err := parseAddrPort(rest)
		if err != nil {
			return Invite{}, fmt.Errorf("invite endpoint %d: %w", i, err)
		}
		addrs = append(addrs, addr)
		rest = rest[n:]
	}
	if len(rest) != 0 {
		return Invite{}, errors.New("invite has trailing bytes")
	}
	return Invite{ID: id, PublicKey: pub, Addrs: addrs}, nil
}

func appendAddrPort(dst []byte, addr netip.AddrPort) ([]byte, error) {
	addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
	if addr.Port() == 0 {
		return nil, errors.New("endpoint port must not be zero")
	}
	if !addr.Addr().IsValid() || addr.Addr().IsUnspecified() || addr.Addr().IsMulticast() {
		return nil, fmt.Errorf("endpoint %s is not a unicast address", addr)
	}
	if ip := addr.Addr(); ip.Is4() {
		v4 := ip.As4()
		dst = append(dst, 4)
		dst = append(dst, v4[:]...)
	} else {
		v6 := ip.As16()
		dst = append(dst, 6)
		dst = append(dst, v6[:]...)
	}
	return binary.BigEndian.AppendUint16(dst, addr.Port()), nil
}

func parseAddrPort(raw []byte) (netip.AddrPort, int, error) {
	if len(raw) < 1 {
		return netip.AddrPort{}, 0, errors.New("missing address family")
	}
	var (
		addr netip.Addr
		size int
	)
	switch raw[0] {
	case 4:
		size = 1 + 4 + 2
		if len(raw) < size {
			return netip.AddrPort{}, 0, errors.New("truncated IPv4 endpoint")
		}
		addr = netip.AddrFrom4([4]byte(raw[1:5]))
	case 6:
		size = 1 + 16 + 2
		if len(raw) < size {
			return netip.AddrPort{}, 0, errors.New("truncated IPv6 endpoint")
		}
		addr = netip.AddrFrom16([16]byte(raw[1:17]))
	default:
		return netip.AddrPort{}, 0, fmt.Errorf("unknown address family %d", raw[0])
	}
	port := binary.BigEndian.Uint16(raw[size-2 : size])
	if port == 0 {
		return netip.AddrPort{}, 0, errors.New("endpoint port must not be zero")
	}
	if addr.IsUnspecified() || addr.IsMulticast() {
		return netip.AddrPort{}, 0, fmt.Errorf("endpoint %s is not a unicast address", addr)
	}
	return netip.AddrPortFrom(addr.Unmap(), port), size, nil
}
