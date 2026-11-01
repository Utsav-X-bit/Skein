// Package tun wraps OS TUN device creation behind a small byte-oriented
// interface. Only device creation comes from the WireGuard project; all packet
// handling stays in this module.
//
// On Linux the device is created with IFF_VNET_HDR, so every read and write
// carries a virtio-net header. Reads may also return a GSO-coalesced packet
// that splits into several IP packets, which is why the wrapper uses the
// device's batch API and queues the results.
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
	mtu   int

	// Read blocks inside the kernel while holding readMu, so writes must not
	// share it. The inner device serialises reads and writes independently.
	readMu  sync.Mutex
	writeMu sync.Mutex

	bufs    [][]byte
	sizes   []int
	pending int
	count   int
	write   [][]byte
	scratch []byte
}

// Open creates or attaches to a TUN device with the given name and MTU.
func Open(name string, mtu int) (Device, error) {
	if mtu <= 0 {
		return nil, fmt.Errorf("tun: mtu must be positive, got %d", mtu)
	}
	inner, err := wgtun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("tun: create %q: %w", name, hint(err))
	}
	actual, err := inner.Name()
	if err != nil {
		inner.Close()
		return nil, fmt.Errorf("tun: read interface name: %w", err)
	}

	batch := inner.BatchSize()
	if batch < 1 {
		batch = 1
	}
	d := &device{
		inner: inner,
		name:  actual,
		mtu:   mtu,
		bufs:  make([][]byte, batch),
		sizes: make([]int, batch),
		write: make([][]byte, 1),
	}
	for i := range d.bufs {
		d.bufs[i] = make([]byte, mtu)
	}
	return d, nil
}

// Read returns one IP packet. Packets produced by GSO splitting are queued and
// returned on later calls.
func (d *device) Read(out []byte) (int, error) {
	d.readMu.Lock()
	defer d.readMu.Unlock()

	if d.pending >= d.count {
		n, err := d.inner.Read(d.bufs, d.sizes, 0)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, nil
		}
		d.count = n
		d.pending = 0
	}
	size := d.sizes[d.pending]
	pkt := d.bufs[d.pending][:size]
	d.pending++
	if len(pkt) > len(out) {
		return 0, fmt.Errorf("tun: packet of %d bytes exceeds the %d-byte buffer", len(pkt), len(out))
	}
	return copy(out, pkt), nil
}

// Write sends one IP packet. The Linux device requires virtio-net header
// headroom, so the packet is staged behind that reserved space.
func (d *device) Write(pkt []byte) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if transportOffset == 0 {
		d.write[0] = pkt
		n, err := d.inner.Write(d.write, 0)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, nil
		}
		return len(pkt), nil
	}

	total := transportOffset + len(pkt)
	if cap(d.scratch) < total {
		d.scratch = make([]byte, total)
	}
	buf := d.scratch[:total]
	copy(buf[transportOffset:], pkt)
	d.write[0] = buf
	n, err := d.inner.Write(d.write, transportOffset)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	return len(pkt), nil
}

// Name reports the OS interface name.
func (d *device) Name() string { return d.name }

// Close releases the device.
func (d *device) Close() error { return d.inner.Close() }
