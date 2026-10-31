// Package tun wraps OS TUN device creation behind a small byte-oriented
// interface. Only device creation comes from the WireGuard project; all packet
// handling stays in this module.
package tun

import (
	"fmt"
	"sync"

	wgtun "golang.zx2c4.com/wireguard/tun"
)

// Device is a byte-oriented TUN device.
type Device interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Name() string
	Close() error
}

type device struct {
	inner wgtun.Device
	name  string

	mu     sync.Mutex
	bufs   [1][]byte
	sizes  [1]int
	closed bool
}

// Open creates or attaches to a TUN device with the given name and MTU.
func Open(name string, mtu int) (Device, error) {
	if mtu <= 0 {
		return nil, fmt.Errorf("tun: mtu must be positive, got %d", mtu)
	}
	inner, err := wgtun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("tun: create %q: %w", name, err)
	}
	actual, err := inner.Name()
	if err != nil {
		inner.Close()
		return nil, fmt.Errorf("tun: read interface name: %w", err)
	}
	return &device{inner: inner, name: actual}, nil
}

// Read reads one IP packet into buf.
func (d *device) Read(buf []byte) (int, error) {
	d.mu.Lock()
	d.bufs[0] = buf
	n, err := d.inner.Read(d.bufs[:], d.sizes[:], 0)
	size := d.sizes[0]
	d.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return size, nil
}

// Write writes one IP packet from buf.
func (d *device) Write(buf []byte) (int, error) {
	d.mu.Lock()
	d.bufs[0] = buf
	n, err := d.inner.Write(d.bufs[:], 0)
	d.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return len(buf), nil
}

// Name reports the OS interface name.
func (d *device) Name() string { return d.name }

// Close releases the device.
func (d *device) Close() error { return d.inner.Close() }
