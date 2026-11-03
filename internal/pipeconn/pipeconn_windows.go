// Package pipeconn opens a Windows named pipe as a net.Conn, using direct
// Win32 handles so the caller keeps the handle for identity checks.
package pipeconn

import (
	"fmt"
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
		0,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("pipeconn: open %s: %w", name, err)
	}
	return &conn{h: h}, nil
}

// conn is a blocking net.Conn over one named pipe instance.
type conn struct {
	h windows.Handle

	mu       sync.Mutex
	deadline time.Time
}

var _ = net.Conn(&conn{})

func (c *conn) Read(b []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(c.h, b, &n, nil)
	return int(n), err
}

func (c *conn) Write(b []byte) (int, error) {
	total := 0
	for total < len(b) {
		var n uint32
		err := windows.WriteFile(c.h, b[total:], &n, nil)
		total += int(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.h == 0 {
		return nil
	}
	err := windows.CloseHandle(c.h)
	c.h = 0
	return err
}

func (c *conn) LocalAddr() net.Addr  { return addr{} }
func (c *conn) RemoteAddr() net.Addr { return addr{} }

func (c *conn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

func (c *conn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

type addr struct{}

func (addr) Network() string { return "namedpipe" }
func (addr) String() string  { return "namedpipe" }
