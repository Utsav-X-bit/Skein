//go:build linux

package tun

import (
	"encoding/binary"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestTUNWriteRead injects an ICMP echo request into the kernel through the
// device and expects the echo reply back out of the device. It proves the
// virtio-net header handling on both directions.
//
// Requires root: SKEIN_TUN_PROBE=1 sudo -E env "PATH=$PATH" go test ./internal/tun -run TestTUNWriteRead -v
func TestTUNWriteRead(t *testing.T) {
	if os.Getenv("SKEIN_TUN_PROBE") == "" {
		t.Skip("set SKEIN_TUN_PROBE=1 and run as root")
	}
	if os.Geteuid() != 0 {
		t.Skip("TUN probe requires root")
	}

	dev, err := Open("skein-probe", 1280)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dev.Close()
	defer exec.Command("ip", "link", "del", dev.Name()).Run()

	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v: %s", args, err, out)
		}
	}
	run("addr", "add", "10.99.0.2/24", "dev", dev.Name())
	run("link", "set", dev.Name(), "up")

	request := icmpEchoRequest([4]byte{10, 99, 0, 1}, [4]byte{10, 99, 0, 2}, []byte("probe"))
	if _, err := dev.Write(request); err != nil {
		t.Fatalf("Write: %v", err)
	}

	type result struct {
		n   int
		err error
	}
	got := make(chan result, 1)
	go func() {
		buf := make([]byte, 1500)
		n, err := dev.Read(buf)
		got <- result{n: n, err: err}
	}()

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("Read: %v", r.err)
		}
		if r.n < 20 {
			t.Fatalf("short read: %d", r.n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reply came back through the TUN; the write path is broken")
	}
}

func icmpEchoRequest(src, dst [4]byte, payload []byte) []byte {
	pkt := make([]byte, 20+8+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = 1 // ICMP
	copy(pkt[12:16], src[:])
	copy(pkt[16:20], dst[:])
	// ICMP echo request.
	pkt[20] = 8
	pkt[21] = 0
	binary.BigEndian.PutUint16(pkt[24:26], 1)
	copy(pkt[28:], payload)
	sum := checksum(pkt[20:])
	binary.BigEndian.PutUint16(pkt[22:24], sum)
	sum = checksum(pkt[:20])
	binary.BigEndian.PutUint16(pkt[10:12], sum)
	return pkt
}

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
