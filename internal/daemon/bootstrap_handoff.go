package daemon

import (
	"context"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// handoffWait bounds how long a request that arrives after bootstrap
// success waits for the normal handler. The transition is in-process
// (close the config lock, build the daemon, adopt the handler); this
// deadline is a safety valve for a wedged transition, not a readiness
// delay. It stays below the 10s control-client timeout so a client sees
// the explicit 503 rather than its own timeout.
const handoffWait = 5 * time.Second

type handoffState uint8

const (
	// handoffServing: the bootstrap handler owns the listener.
	handoffServing handoffState = iota
	// handoffTransition: bootstrap committed; requests wait for the
	// normal handler instead of being answered from bootstrap mode.
	handoffTransition
	// handoffAdopted: the normal daemon owns the handler.
	handoffAdopted
	// handoffFailed: the transition failed; waiting requests get 503.
	handoffFailed
)

// ControlHandoff keeps the bootstrap control listener bound across the
// in-process transition to normal mode (B16). The listener and HTTP
// server are created once in bootstrap mode; only the handler changes.
// Closing and rebinding the socket/pipe — the old behaviour — left a
// window in which a client that connected immediately after a
// successful bootstrap saw a dead transport (Windows: "The pipe has
// been ended"; Unix: connection refused on the unlinked socket). Here
// the kernel keeps accepting connections, and requests that arrive
// during the transition are held until the normal API handler is
// adopted, then served by it.
type ControlHandoff struct {
	srv      *http.Server
	ln       net.Listener
	remove   string // Unix socket path to unlink when stopped
	serveErr chan error

	mu      sync.RWMutex
	state   handoffState
	handler http.Handler
	failed  error
	ready   chan struct{}

	readyOnce sync.Once
	stopOnce  sync.Once
}

// newControlHandoff wires srv to serve initial first and returns the
// handoff. The caller starts srv.Serve(ln) immediately; Stop waits for
// that Serve to return.
func newControlHandoff(srv *http.Server, ln net.Listener, initial http.Handler, remove string, serveErr chan error) *ControlHandoff {
	h := &ControlHandoff{
		srv:      srv,
		ln:       ln,
		remove:   remove,
		serveErr: serveErr,
		handler:  initial,
		ready:    make(chan struct{}),
	}
	srv.Handler = h
	return h
}

// ServeHTTP routes to the bootstrap handler while bootstrap mode owns
// the listener, to the normal handler once adopted, and otherwise waits
// for the transition to finish.
func (h *ControlHandoff) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	state, handler := h.state, h.handler
	h.mu.RUnlock()
	if state == handoffServing || state == handoffAdopted {
		handler.ServeHTTP(w, r)
		return
	}
	h.awaitReady(w, r)
}

// BeginTransition is called when the bootstrap commits: subsequent
// requests must not be answered from bootstrap mode.
func (h *ControlHandoff) BeginTransition() {
	h.mu.Lock()
	if h.state == handoffServing {
		h.state = handoffTransition
	}
	h.mu.Unlock()
}

// Adopt installs the normal API handler and wakes every request waiting
// on the transition. The returned stop function shuts the server down
// and removes the Unix socket; the normal daemon calls it on shutdown.
func (h *ControlHandoff) Adopt(handler http.Handler) func() {
	h.mu.Lock()
	h.handler = handler
	h.state = handoffAdopted
	h.mu.Unlock()
	h.signalReady()
	return h.Stop
}

// Fail reports a transition failure, wakes waiters with a 503, and
// stops the server. It is for the window between bootstrap success and
// normal-daemon construction; after Adopt, use the returned stop.
func (h *ControlHandoff) Fail(err error) {
	h.mu.Lock()
	h.failed = err
	if h.state != handoffAdopted {
		h.state = handoffFailed
	}
	h.mu.Unlock()
	h.signalReady()
	h.Stop()
}

func (h *ControlHandoff) signalReady() {
	h.readyOnce.Do(func() { close(h.ready) })
}

// awaitReady holds one request until the transition resolves. A request
// that arrived after bootstrap success must never be answered with
// bootstrap state, so it waits (bounded) for the normal handler.
func (h *ControlHandoff) awaitReady(w http.ResponseWriter, r *http.Request) {
	timer := time.NewTimer(handoffWait)
	defer timer.Stop()
	select {
	case <-h.ready:
		h.mu.RLock()
		state, handler, failed := h.state, h.handler, h.failed
		h.mu.RUnlock()
		if state == handoffAdopted && handler != nil {
			handler.ServeHTTP(w, r)
			return
		}
		msg := "bootstrap: daemon is not available; retry"
		if failed != nil {
			msg = "bootstrap: daemon failed to start: " + failed.Error()
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
	case <-r.Context().Done():
		return
	case <-timer.C:
		http.Error(w, "bootstrap: daemon is starting; retry", http.StatusServiceUnavailable)
	}
}

// Stop shuts the control server down once and removes the Unix socket.
// Waiters are released first, so a stop without an adoption (error path
// or shutdown during the transition) cannot leave a request parked.
func (h *ControlHandoff) Stop() {
	h.stopOnce.Do(func() {
		h.signalReady()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = h.srv.Shutdown(shutdownCtx)
		cancel()
		// Shutdown closes tracked listeners, but a Serve that has not
		// started yet would return without tracking this one; close it
		// explicitly so no socket/pipe instance survives Stop.
		_ = h.ln.Close()
		if h.remove != "" {
			_ = os.Remove(h.remove)
		}
		<-h.serveErr
	})
}
