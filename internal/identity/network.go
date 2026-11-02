package identity

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// Network invite: one string that bootstraps a node. It carries the group
// secret, the inviter's identity and endpoints, the overlay prefixes, and the
// overlay addresses already in use. Treat it as a secret: anyone holding it can
// join the network.
const (
	NetworkInvitePrefix  = "skein:2:"
	networkInviteVersion = 2
	maxUsedAddresses     = 16
	maxNetworkInvite     = 512
)

// NetworkInvite is the bootstrap payload.
type NetworkInvite struct {
	Secret    []byte
	ID        uint64
	PublicKey []byte
	PrefixV4  netip.Prefix
	PrefixV6  netip.Prefix
	UsedV4    []netip.Addr
	Addrs     []netip.AddrPort
}

// EncodeNetworkInvite renders skein:2:<base64url-payload>.
func EncodeNetworkInvite(inv NetworkInvite) (string, error) {
	if len(inv.Secret) != KeySize {
		return "", fmt.Errorf("network invite secret must be %d bytes", KeySize)
	}
	if len(inv.PublicKey) != KeySize {
		return "", fmt.Errorf("network invite public key must be %d bytes", KeySize)
	}
	id, err := PeerID(inv.PublicKey)
	if err != nil {
		return "", err
	}
	if inv.ID != 0 && inv.ID != id {
		return "", fmt.Errorf("network invite peer ID %016x does not match its public key", inv.ID)
	}
	if !inv.PrefixV4.IsValid() || !inv.PrefixV4.Addr().Is4() {
		return "", errors.New("network invite needs a valid IPv4 overlay prefix")
	}
	if !inv.PrefixV6.IsValid() || !inv.PrefixV6.Addr().Is6() {
		return "", errors.New("network invite needs a valid IPv6 overlay prefix")
	}
	if len(inv.UsedV4) > maxUsedAddresses {
		return "", fmt.Errorf("network invite carries %d used addresses, maximum is %d", len(inv.UsedV4), maxUsedAddresses)
	}
	if len(inv.Addrs) > MaxEndpoints {
		return "", fmt.Errorf("network invite carries %d endpoints, maximum is %d", len(inv.Addrs), MaxEndpoints)
	}

	payload := make([]byte, 0, 160)
	payload = append(payload, networkInviteVersion)
	payload = append(payload, inv.Secret...)
	payload = binary.BigEndian.AppendUint64(payload, id)
	payload = append(payload, inv.PublicKey...)

	v4 := inv.PrefixV4.Masked().Addr().As4()
	payload = append(payload, v4[:]...)
	payload = append(payload, byte(inv.PrefixV4.Bits()))
	v6 := inv.PrefixV6.Masked().Addr().As16()
	payload = append(payload, v6[:]...)
	payload = append(payload, byte(inv.PrefixV6.Bits()))

	payload = append(payload, byte(len(inv.UsedV4)))
	for _, addr := range inv.UsedV4 {
		if !addr.Is4() {
			return "", fmt.Errorf("used overlay address %s is not IPv4", addr)
		}
		a := addr.As4()
		payload = append(payload, a[:]...)
	}
	payload = append(payload, byte(len(inv.Addrs)))
	for _, addr := range inv.Addrs {
		encoded, err := appendAddrPort(nil, addr)
		if err != nil {
			return "", err
		}
		payload = append(payload, encoded...)
	}
	if len(payload) > maxNetworkInvite {
		return "", fmt.Errorf("network invite is %d bytes, maximum is %d", len(payload), maxNetworkInvite)
	}
	return NetworkInvitePrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

// DecodeNetworkInvite parses and validates a skein:2: invite.
func DecodeNetworkInvite(s string) (NetworkInvite, error) {
	if len(s) < len(NetworkInvitePrefix) || s[:len(NetworkInvitePrefix)] != NetworkInvitePrefix {
		return NetworkInvite{}, fmt.Errorf("network invite must start with %q", NetworkInvitePrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(s[len(NetworkInvitePrefix):])
	if err != nil {
		return NetworkInvite{}, fmt.Errorf("network invite is not valid base64url: %w", err)
	}
	if len(raw) > maxNetworkInvite {
		return NetworkInvite{}, fmt.Errorf("network invite is %d bytes, maximum is %d", len(raw), maxNetworkInvite)
	}
	const fixed = 1 + KeySize + 8 + KeySize + 5 + 17 + 1
	if len(raw) < fixed {
		return NetworkInvite{}, errors.New("network invite is truncated")
	}
	if raw[0] != networkInviteVersion {
		return NetworkInvite{}, fmt.Errorf("unsupported network invite version %d", raw[0])
	}
	off := 1
	inv := NetworkInvite{Secret: append([]byte(nil), raw[off:off+KeySize]...)}
	off += KeySize
	inv.ID = binary.BigEndian.Uint64(raw[off : off+8])
	off += 8
	inv.PublicKey = append([]byte(nil), raw[off:off+KeySize]...)
	off += KeySize

	inv.PrefixV4 = netip.PrefixFrom(netip.AddrFrom4([4]byte(raw[off:off+4])), int(raw[off+4]))
	off += 5
	inv.PrefixV6 = netip.PrefixFrom(netip.AddrFrom16([16]byte(raw[off:off+16])), int(raw[off+16]))
	off += 17

	derived, err := PeerID(inv.PublicKey)
	if err != nil {
		return NetworkInvite{}, err
	}
	if derived != inv.ID {
		return NetworkInvite{}, errors.New("network invite peer ID does not match its public key")
	}
	if inv.PrefixV4 != inv.PrefixV4.Masked() || inv.PrefixV4.Bits() == 0 {
		return NetworkInvite{}, errors.New("network invite has an invalid IPv4 prefix")
	}
	if inv.PrefixV6 != inv.PrefixV6.Masked() || inv.PrefixV6.Bits() == 0 {
		return NetworkInvite{}, errors.New("network invite has an invalid IPv6 prefix")
	}

	usedCount := int(raw[off])
	off++
	if usedCount > maxUsedAddresses {
		return NetworkInvite{}, fmt.Errorf("network invite carries %d used addresses, maximum is %d", usedCount, maxUsedAddresses)
	}
	if off+4*usedCount > len(raw) {
		return NetworkInvite{}, errors.New("network invite is truncated")
	}
	for range usedCount {
		inv.UsedV4 = append(inv.UsedV4, netip.AddrFrom4([4]byte(raw[off:off+4])))
		off += 4
	}

	if off >= len(raw) {
		return NetworkInvite{}, errors.New("network invite is truncated")
	}
	addrCount := int(raw[off])
	off++
	if addrCount > MaxEndpoints {
		return NetworkInvite{}, fmt.Errorf("network invite carries %d endpoints, maximum is %d", addrCount, MaxEndpoints)
	}
	for range addrCount {
		addr, n, err := parseAddrPort(raw[off:])
		if err != nil {
			return NetworkInvite{}, err
		}
		off += n
		inv.Addrs = append(inv.Addrs, addr)
	}
	if off != len(raw) {
		return NetworkInvite{}, errors.New("network invite has trailing bytes")
	}
	return inv, nil
}
