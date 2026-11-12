//go:build android

package tun

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
)

// Android TUN: the device is a file descriptor supplied by the Java
// SkeinVpnService, which builds it with VpnService.Builder.establish().
// Android does not allow an app to open /dev/net/tun, so tun.Open
// (wgtun.CreateTUN) is never called on Android. Reads and writes are
// raw IP packets on the FD: unlike the Linux device, there is no
// virtio-net header (transportOffset is 0 on this path — this file
// implements Device directly instead of wrapping wgtun.Device).

// androidDevice is a tun.Device over a VpnService file descriptor.
// The device owns the FD: Close closes it.
type androidDevice struct {
	fd   int
	mtu  int
	name string

	readMu  sync.Mutex
	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool
}

func (d *androidDevice) Read(out []byte) (int, error) {
	d.readMu.Lock()
	defer d.readMu.Unlock()
	n, err := syscall.Read(d.fd, out)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (d *androidDevice) Write(pkt []byte) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	// A TUN write is normally all-or-nothing, but loop anyway so a
	// short write can never silently drop the tail of a packet.
	written := 0
	for written < len(pkt) {
		n, err := syscall.Write(d.fd, pkt[written:])
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, errors.New("tun: short write on android TUN fd")
		}
		written += n
	}
	return written, nil
}

// Name reports the interface name the VpnService built the TUN as.
// The kernel interface is named by the system; "skein0" is the name
// the daemon logs and the GUI shows.
func (d *androidDevice) Name() string { return d.name }

// Close releases the FD. It is safe to call more than once.
func (d *androidDevice) Close() error {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return syscall.Close(d.fd)
}

// pendingFd is the handoff for the VpnService FD: the JNI bridge calls
// SetAndroidFd when establish() returns, and the daemon's OpenAndroid
// consumes it when the dataplane starts. Buffered (1) so the Java
// service thread never blocks handing the FD over.
var (
	pendingFdMu sync.Mutex
	pendingFd   = make(chan int, 1)
)

// SetAndroidFd hands the TUN file descriptor from
// VpnService.Builder.establish() to the daemon. The FD must come from
// ParcelFileDescriptor.detachFd(): ownership transfers to Go, and the
// daemon's Close is what eventually closes it — Java must not close
// the raw FD, only the (detached) ParcelFileDescriptor object. A
// previously handed-over FD that was never consumed (e.g. a connect
// that never happened) is closed so it does not leak.
func SetAndroidFd(fd int) error {
	if fd < 0 {
		return fmt.Errorf("tun: android file descriptor must be non-negative, got %d", fd)
	}
	pendingFdMu.Lock()
	defer pendingFdMu.Unlock()
	select {
	case stale := <-pendingFd:
		_ = syscall.Close(stale)
	default:
	}
	pendingFd <- fd
	return nil
}

// OpenAndroidFd wraps an already-owned FD as a Device. The Device
// owns the FD: Close closes it.
func OpenAndroidFd(fd, mtu int) (Device, error) {
	if fd < 0 {
		return nil, fmt.Errorf("tun: android file descriptor must be non-negative, got %d", fd)
	}
	if mtu <= 0 {
		return nil, fmt.Errorf("tun: mtu must be positive, got %d", mtu)
	}
	return &androidDevice{fd: fd, mtu: mtu, name: "skein0"}, nil
}

// androidFdWait bounds how long OpenAndroid waits for the VpnService
// FD. The FD cannot exist before the user grants VPN permission and
// the service establishes the TUN, so the wait is generous — but it
// must end: an unbounded wait would wedge the daemon's connection
// loop in ConnConnecting, where Disconnect refuses with 409.
const androidFdWait = 60 * time.Second

// OpenAndroid waits for SetAndroidFd and wraps the FD as a Device.
// Call it only after the VpnService has established the TUN; a
// timeout means the VPN was not established (permission denied or
// revoked) and the daemon reports ConnError so Connect can be retried.
func OpenAndroid(mtu int) (Device, error) {
	select {
	case fd := <-pendingFd:
		return OpenAndroidFd(fd, mtu)
	case <-time.After(androidFdWait):
		return nil, errors.New("tun: timed out waiting for the Android VpnService file descriptor; grant VPN permission and retry")
	}
}
