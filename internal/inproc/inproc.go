// Package inproc is the Android in-process control transport: the GUI
// services layer and the daemon are the same process and the same UID
// on Android, and there is no Unix socket (/run/skein/control.sock
// does not exist), so the control API is served over net.Pipe pairs
// instead of a socket.
//
// Authentication is NOT weakened: the server end of each pair stamps
// the app's own UID as the kernel caller (the same identity the Unix
// socket would have derived from SO_PEERCRED), and the daemon grants
// that UID the secret role, so api.Server.wrapRole still enforces the
// per-route role gate. No other process can dial this transport — it
// is not backed by a socket, pipe, or TCP port — so the grant is
// reachable only from inside the app.
package inproc

import (
	"context"
	"net"
	"os"
	"sync"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// inProcAddr is the address of the in-process listener.
type inProcAddr struct{}

func (inProcAddr) Network() string { return "inproc" }
func (inProcAddr) String() string  { return "in-process" }

// serverConn wraps the server end of a net.Pipe pair and stamps the
// app's own UID as the kernel caller, so api.CallerContext (used as
// the control server's ConnContext) sees the same identity the Unix
// socket would have derived from SO_PEERCRED.
type serverConn struct {
	net.Conn
	uid int
}

// ConnContext is what api.CallerContext looks for: it returns the
// connection context carrying the kernel-derived caller.
func (c serverConn) ConnContext() context.Context {
	return api.WithCaller(context.Background(), api.Caller{UID: c.uid, Transport: api.TransportKernel})
}

// Listener is a net.Listener backed by net.Pipe pairs. Dial creates a
// pair and delivers the server end to Accept; the client end is
// returned to the caller. It exists so the daemon's
// StartControlListeners can serve it with the same http.Server (and
// the same ConnContext) as the Unix socket.
type Listener struct {
	mu     sync.Mutex
	ch     chan net.Conn
	done   chan struct{}
	closed bool
}

// NewListener returns an unclosed in-process listener.
func NewListener() *Listener {
	return &Listener{ch: make(chan net.Conn), done: make(chan struct{})}
}

// Accept waits for the next dialled connection.
func (l *Listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close unblocks Accept and makes further Dial calls fail. It is
// idempotent.
func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		close(l.done)
	}
	return nil
}

// Addr returns the in-process address.
func (l *Listener) Addr() net.Addr { return inProcAddr{} }

// Dial returns the client end of a fresh pipe pair whose server end
// is queued for Accept. The context bounds the queue wait; the
// server is normally already accepting, so this returns at once.
func (l *Listener) Dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	wrapped := serverConn{Conn: server, uid: os.Getuid()}
	select {
	case l.ch <- wrapped:
		return client, nil
	case <-l.done:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
}

var (
	defaultMu sync.RWMutex
	current   = NewListener()
)

// Current returns the control listener for the current daemon generation.
func Current() *Listener {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return current
}

// Reset closes the previous generation, releasing pending dials, and installs
// a fresh listener before the next daemon starts.
func Reset() {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	_ = current.Close()
	current = NewListener()
}

// Dial dials the current daemon generation.
func Dial(ctx context.Context) (net.Conn, error) { return Current().Dial(ctx) }
