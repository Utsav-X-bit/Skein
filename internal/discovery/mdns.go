package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/utsavgupta5467/skein/internal/identity"
)

// ServiceName is the mDNS service used for LAN discovery.
const ServiceName = "_skein._udp"

// Fingerprint derives the network tag published in mDNS from the network
// secret. It is not the secret itself.
func Fingerprint(psk []byte) string {
	sum := sha256.Sum256(psk)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

// MDNS is a running advertisement and browse loop.
type MDNS struct {
	server *zeroconf.Server
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// StartMDNS advertises inv on the local link and reports validated peers.
func StartMDNS(ctx context.Context, inv identity.Invite, fingerprint string, port uint16, onPeer func(identity.Invite)) (*MDNS, error) {
	if onPeer == nil {
		return nil, errors.New("discovery: peer callback is required")
	}
	txt, err := EncodeTXT(inv, fingerprint)
	if err != nil {
		return nil, err
	}
	instance := fmt.Sprintf("skein-%016x", inv.ID)
	server, err := zeroconf.Register(instance, ServiceName, "local.", int(port), txt, nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: register mDNS: %w", err)
	}

	browseCtx, cancel := context.WithCancel(ctx)
	m := &MDNS{server: server, cancel: cancel}

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		server.Shutdown()
		cancel()
		return nil, fmt.Errorf("discovery: create mDNS resolver: %w", err)
	}
	entries := make(chan *zeroconf.ServiceEntry, 16)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for entry := range entries {
			peerInv, err := DecodeEntry(entry, fingerprint)
			if err != nil || peerInv.ID == inv.ID {
				continue
			}
			onPeer(peerInv)
		}
	}()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(entries)
		_ = resolver.Browse(browseCtx, ServiceName, "local.", entries)
	}()
	return m, nil
}

// Close stops advertising and browsing.
func (m *MDNS) Close() {
	if m == nil {
		return
	}
	m.cancel()
	m.server.Shutdown()
	m.wg.Wait()
}

// EncodeTXT renders the invite as mDNS TXT records.
func EncodeTXT(inv identity.Invite, fingerprint string) ([]string, error) {
	if len(inv.PublicKey) != identity.KeySize {
		return nil, errors.New("discovery: invite public key has the wrong size")
	}
	if len(inv.Addrs) > identity.MaxEndpoints {
		return nil, errors.New("discovery: too many invite endpoints")
	}
	txt := []string{
		"id=" + fmt.Sprintf("%016x", inv.ID),
		"pk=" + base64.StdEncoding.EncodeToString(inv.PublicKey),
		"net=" + fingerprint,
	}
	for _, addr := range inv.Addrs {
		encoded := addr.String()
		if len(encoded) > 250 {
			continue
		}
		txt = append(txt, "a="+encoded)
	}
	return txt, nil
}

// DecodeTXT parses TXT records into an invite.
func DecodeTXT(txt []string, fingerprint string) (identity.Invite, error) {
	var (
		inv    identity.Invite
		netTag string
		addrs  []netip.AddrPort
	)
	for _, record := range txt {
		key, value, ok := strings.Cut(record, "=")
		if !ok {
			continue
		}
		switch key {
		case "id":
			id, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				return identity.Invite{}, fmt.Errorf("discovery: invalid mDNS id %q", value)
			}
			inv.ID = id
		case "pk":
			pub, err := base64.StdEncoding.DecodeString(value)
			if err != nil || len(pub) != identity.KeySize {
				return identity.Invite{}, errors.New("discovery: invalid mDNS public key")
			}
			inv.PublicKey = pub
		case "net":
			netTag = value
		case "a":
			addr, err := netip.ParseAddrPort(value)
			if err != nil {
				return identity.Invite{}, fmt.Errorf("discovery: invalid mDNS endpoint %q", value)
			}
			addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
			if addr.Port() == 0 || addr.Addr().IsUnspecified() || addr.Addr().IsMulticast() {
				return identity.Invite{}, fmt.Errorf("discovery: unusable mDNS endpoint %s", addr)
			}
			addrs = append(addrs, addr)
		}
	}
	if netTag != fingerprint {
		return identity.Invite{}, errors.New("discovery: mDNS network tag mismatch")
	}
	if inv.ID == 0 || len(inv.PublicKey) != identity.KeySize {
		return identity.Invite{}, errors.New("discovery: incomplete mDNS invite")
	}
	derived, err := identity.PeerID(inv.PublicKey)
	if err != nil || derived != inv.ID {
		return identity.Invite{}, errors.New("discovery: mDNS peer ID does not match its key")
	}
	inv.Addrs = addrs
	return inv, nil
}

// DecodeEntry validates one mDNS service entry.
func DecodeEntry(entry *zeroconf.ServiceEntry, fingerprint string) (identity.Invite, error) {
	if entry == nil {
		return identity.Invite{}, errors.New("discovery: nil mDNS entry")
	}
	inv, err := DecodeTXT(entry.Text, fingerprint)
	if err != nil {
		return identity.Invite{}, err
	}
	if len(inv.Addrs) == 0 && entry.Port > 0 && entry.Port <= 65535 {
		for _, ip := range append(entry.AddrIPv4, entry.AddrIPv6...) {
			addr, ok := netip.AddrFromSlice(ip)
			if !ok {
				continue
			}
			addr = addr.Unmap()
			if addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() {
				continue
			}
			inv.Addrs = append(inv.Addrs, netip.AddrPortFrom(addr, uint16(entry.Port)))
			if len(inv.Addrs) >= identity.MaxEndpoints {
				break
			}
		}
	}
	if len(inv.Addrs) == 0 {
		return identity.Invite{}, errors.New("discovery: mDNS entry has no usable endpoints")
	}
	return inv, nil
}

// TTLFor bounds how long a state advertisement stays valid.
const TTLFor = 90 * time.Second
