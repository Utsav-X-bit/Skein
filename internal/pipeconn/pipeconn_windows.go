// Package pipeconn opens a Windows named pipe as a net.Conn, using direct
// Win32 handles so the caller keeps the handle for identity checks.
package pipeconn

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// Dial opens the named pipe as a client instance.
func Dial(name string) (net.Conn, error) {
	switch {
	case strings.HasPrefix(name, `\\.\pipe\`):
		// already a full pipe path
	case strings.HasPrefix(name, `\\`):
		name = `\\.\pipe\` + strings.TrimPrefix(name, `\\`)
	default:
		name = `\\.\pipe\` + name
	}
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		name16,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, // no sharing: a single writer/reader pair per instance
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("pipeconn: open %s: %w", name, err)
	}
	c := &conn{h: h}
	c.in, err = windows.CreateEvent(nil, 1, 0, nil) // manual reset
	if err != nil {
		c.Close()
		return nil, err
	}
	c.out, err = windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// conn is a net.Conn over one named pipe instance with overlapped I/O.
type conn struct {
	h   windows.Handle
	in  windows.Handle // read-complete event
	out windows.Handle // write-complete event

	mu       sync.Mutex
	deadline time.Time
}

var _ = net.Conn(&conn{})

func (c *conn) Read(b []byte) (int, error) {
	log.Printf("pipeconn: read enter")
	ov := &windows.Overlapped{}
	ov.HEvent = c.in
	windows.ResetEvent(c.in)
	var n uint32
	err := windows.ReadFile(c.h, b, &n, ov)
	if err == nil {
		log.Printf("pipeconn: read sync n=%d", n)
		return int(n), nil
	}
	if err != windows.ERROR_IO_PENDING {
		return 0, err
	}
	if err := c.wait(c.in, ov); err != nil {
		return 0, err
	}
	if err := windows.GetOverlappedResult(c.h, ov, &n, false); err != nil {
		return 0, err
	}
	log.Printf("pipeconn: read async n=%d", n)
	return int(n), nil
}

func (c *conn) Write(b []byte) (int, error) {
	total := 0
	for total < len(b) {
		ov := &windows.Overlapped{}
		ov.HEvent = c.out
		windows.ResetEvent(c.out)
		var n uint32
		err := windows.WriteFile(c.h, b[total:], &n, ov)
		if err == nil {
			log.Printf("pipeconn: write sync n=%d", n)
			total += int(n)
			continue
		}
		if err != windows.ERROR_IO_PENDING {
			return total, err
		}
		if err := c.wait(c.out, ov); err != nil {
			return total, err
		}
		if err := windows.GetOverlappedResult(c.h, ov, &n, false); err != nil {
			return total, err
		}
		log.Printf("pipeconn: write async n=%d", n)
		total += int(n)
	}
	return total, nil
}

// wait blocks until an overlapped operation completes or the shared
// deadline expires.
func (c *conn) wait(event windows.Handle, ov *windows.Overlapped) error {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	wait := uint32(0xFFFFFFFF) // INFINITE
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return osDeadline{}
		}
		ms := uint32(d / time.Millisecond)
		if ms == 0 {
			ms = 1
		}
		wait = ms
	}
	ev, err := windows.WaitForSingleObject(event, wait)
	if err != nil {
		return err
	}
	switch {
	case ev == windows.WAIT_OBJECT_0:
		return nil
	case ev == 0x102: // WAIT_TIMEOUT
		windows.CancelIoEx(c.h, ov)
		return osDeadline{}
	default:
		return errors.New("pipeconn: wait failed")
	}
}

func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	if c.in != 0 {
		errs = append(errs, windows.CloseHandle(c.in))
		c.in = 0
	}
	if c.out != 0 {
		errs = append(errs, windows.CloseHandle(c.out))
		c.out = 0
	}
	if c.h != 0 {
		errs = append(errs, windows.CloseHandle(c.h))
		c.h = 0
	}
	return errors.Join(errs...)
}

func (c *conn) LocalAddr() net.Addr  { return addr{} }
func (c *conn) RemoteAddr() net.Addr { return addr{} }

// SetDeadline bounds both reads and writes; the helper and CLI never need
// separate read and write deadlines on a local pipe.
func (c *conn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	return nil
}

func (c *conn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

// osDeadline is a net.Error whose deadline flag is true.
type osDeadline struct{}

func (osDeadline) Error() string   { return "pipeconn: i/o timeout" }
func (osDeadline) Timeout() bool   { return true }
func (osDeadline) Temporary() bool { return true }

type addr struct{}

func (addr) Network() string { return "namedpipe" }
func (addr) String() string  { return "namedpipe" }
