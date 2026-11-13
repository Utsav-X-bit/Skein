//go:build !windows

package tun

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// fdPair returns both ends of a datagram socketpair: one end is the packet
// device, the other stands in for the IP stack on the far side of the TUN.
func fdPair(t *testing.T) (dev Device, peer *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	d, err := newFdDevice(fds[0], 1280, "skein0")
	if err != nil {
		syscall.Close(fds[0])
		syscall.Close(fds[1])
		t.Fatalf("newFdDevice: %v", err)
	}
	peer = os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { peer.Close() })
	t.Cleanup(func() { d.Close() })
	return d, peer
}

// TestFdDeviceCloseUnblocksRead is the regression test for the reconnect bug:
// close(2) does not interrupt a read(2) blocked in another thread, so a device
// that closed the raw descriptor left the dataplane's read goroutine parked on
// that descriptor *number* — which the next connect handed to a fresh TUN, so
// the stale reader stole the new TUN's packets.
func TestFdDeviceCloseUnblocksRead(t *testing.T) {
	dev, _ := fdPair(t)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1500)
		_, err := dev.Read(buf)
		done <- err
	}()

	// Give the reader time to block inside the kernel.
	time.Sleep(50 * time.Millisecond)
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Read returned no error after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not unblock a pending Read; the reader is stranded on the descriptor")
	}
}

// TestFdDevicePacketRoundTrip checks the plain packet semantics: one Read is one
// packet, and a write reaches the far side intact.
func TestFdDevicePacketRoundTrip(t *testing.T) {
	dev, peer := fdPair(t)

	want := []byte{0x45, 0, 0, 20, 0, 0, 0, 0, 64, 1, 0, 0, 10, 0, 0, 1, 10, 0, 0, 2}
	if n, err := peer.Write(want); err != nil || n != len(want) {
		t.Fatalf("peer write: n=%d err=%v", n, err)
	}
	buf := make([]byte, 1500)
	n, err := dev.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != len(want) {
		t.Fatalf("Read %d bytes, want %d", n, len(want))
	}

	if n, err := dev.Write(want); err != nil || n != len(want) {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	got := make([]byte, 1500)
	n, err = peer.Read(got)
	if err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if n != len(want) {
		t.Fatalf("peer read %d bytes, want %d", n, len(want))
	}
}

// TestFdDeviceCloseIsIdempotent covers the double-close path: teardown closes
// the device, then a retry of teardown closes it again.
func TestFdDeviceCloseIsIdempotent(t *testing.T) {
	dev, _ := fdPair(t)
	if err := dev.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := dev.Read(make([]byte, 1500)); err == nil {
		t.Fatal("Read succeeded after Close")
	}
	if _, err := dev.Write([]byte{0x45}); err == nil {
		t.Fatal("Write succeeded after Close")
	}
}

// TestNewFdDeviceRejectsUnpollableFd: a descriptor the runtime poller cannot
// watch cannot be closed out from under a blocked Read, so it is refused instead
// of running with the packet-stealing close described in fddevice.go. The
// refused descriptor is still owned by newFdDevice, so it must come back closed.
func TestNewFdDeviceRejectsUnpollableFd(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-tun")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()

	rc, err := f.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var (
		dup    int
		dupErr error
	)
	if err := rc.Control(func(fd uintptr) { dup, dupErr = syscall.Dup(int(fd)) }); err != nil {
		t.Fatalf("control: %v", err)
	}
	if dupErr != nil {
		t.Fatalf("dup: %v", dupErr)
	}

	dev, err := newFdDevice(dup, 1280, "skein0")
	if err == nil {
		dev.Close()
		t.Fatal("newFdDevice accepted a regular file descriptor, which the poller cannot watch")
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(dup), syscall.F_GETFD, 0); errno != syscall.EBADF {
		t.Fatalf("fd %d is still open after newFdDevice rejected it (errno %v)", dup, errno)
	}
}
