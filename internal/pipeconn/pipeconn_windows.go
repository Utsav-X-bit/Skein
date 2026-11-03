// Package pipeconn opens a Windows named pipe as a net.Conn, using direct
// Win32 handles so the caller keeps the handle for identity checks.
package pipeconn

import (
	"fmt"
	"net"
	"os"
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
		0,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("pipeconn: open %s: %w", name, err)
	}
	return &conn{f: os.NewFile(uintptr(h), name)}, nil
}

// conn is a blocking net.Conn over one named pipe instance.
type conn struct {
	f *os.File

	mu       sync.Mutex
	deadline time.Time
}

var _ = net.Conn(&conn{})

func (c *conn) Read(b []byte) (int, error)  { return c.f.Read(b) }
func (c *conn) Write(b []byte) (int, error) { return c.f.Write(b) }
func (c *conn) Close() error                { return c.f.Close() }

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
