//go:build !windows

package tun

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// fdDevice is a tun.Device over a file descriptor that another part of the
// process owns — on Android, the TUN that VpnService.Builder.establish()
// created. Reads and writes are raw IP packets with no virtio-net header: this
// type never looks at transportOffset, which is 10 on Android because GOOS
// android also satisfies the linux build tag (offset_linux.go).
//
// The descriptor is put in non-blocking mode and registered with the runtime
// poller, so Close wakes a Read that is already blocked in the kernel. That is
// not cosmetic: on Linux close(2) does not interrupt a read(2) blocked in
// another thread. A reader left behind holds the old file description open and
// keeps reading the same descriptor *number*, which the next connect hands to a
// fresh TUN — the stale reader then steals that TUN's packets and feeds them to
// a dead engine. Going through *os.File makes Close wait for the reader before
// the descriptor is released.
type fdDevice struct {
	f    *os.File
	name string

	// Read blocks inside the kernel while holding readMu, so writes must not
	// share it (same contract as device in device.go).
	readMu  sync.Mutex
	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool
}

// newFdDevice takes ownership of fd: Close (or a setup failure) closes it. mtu
// is the MTU the interface was built with; it is validated but not enforced —
// the kernel is the authority on what fits (EMSGSIZE), and the daemon can ask
// for a TUN at a different MTU than the one it later reports here.
func newFdDevice(fd, mtu int, name string) (Device, error) {
	if fd < 0 {
		return nil, fmt.Errorf("tun: file descriptor must be non-negative, got %d", fd)
	}
	if mtu <= 0 {
		return nil, fmt.Errorf("tun: mtu must be positive, got %d", mtu)
	}
	// os.NewFile only returns a pollable File — one whose Close wakes a blocked
	// Read — when the descriptor is already non-blocking.
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("tun: set fd %d non-blocking: %w", fd, err)
	}
	f := os.NewFile(uintptr(fd), name)
	if f == nil {
		return nil, fmt.Errorf("tun: fd %d is not a valid descriptor", fd)
	}
	// A descriptor the poller cannot watch cannot be closed out from under a
	// blocked Read, which is the packet-stealing case described above. Refuse
	// it rather than run with a close that silently leaks the reader.
	if err := f.SetReadDeadline(time.Time{}); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("tun: fd %d cannot be polled (%w): the daemon could not close it while a read is blocked", fd, err)
	}
	return &fdDevice{f: f, name: name}, nil
}

// Read returns one IP packet. After Close it returns an error immediately, so
// the dataplane's read loop exits instead of holding the descriptor.
func (d *fdDevice) Read(out []byte) (int, error) {
	d.readMu.Lock()
	defer d.readMu.Unlock()
	// os.File retries EINTR and parks the goroutine on the poller for EAGAIN.
	return d.f.Read(out)
}

// Write sends one IP packet. A packet write is all-or-nothing, so a short write
// is an error rather than a second write: the tail of a packet is not a packet.
// An oversize packet is refused by the kernel (EMSGSIZE); the device does not
// second-guess the interface MTU, which it cannot read back.
func (d *fdDevice) Write(pkt []byte) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	n, err := d.f.Write(pkt)
	if err != nil {
		return n, err
	}
	if n != len(pkt) {
		return n, fmt.Errorf("tun: short write: %d of %d bytes", n, len(pkt))
	}
	return n, nil
}

// Name reports the name the daemon logs and the GUI shows.
func (d *fdDevice) Name() string { return d.name }

// Close releases the descriptor and wakes any blocked Read. It is safe to call
// more than once.
func (d *fdDevice) Close() error {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.f.Close()
}
