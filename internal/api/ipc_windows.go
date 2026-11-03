//go:build windows

// The Windows control plane: a direct Win32 named pipe at
// \\.\pipe\skein-control with a DACL, server-side client verification via
// impersonation, and client-side server image verification.
package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows lacks ImpersonateNamedPipeClient and
// GetProcessImageFileNameW, so they are bound directly.
var (
	impersonateNamedPipeClientProc = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")
	getProcessImageFileNameProc    = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessImageFileNameW")
)

const (
	sidSystem         = "S-1-5-18"
	sidAdministrators = "S-1-5-32-544"

	pipeTypeByte     = 0x0
	pipeReadModeByte = 0x0
	pipeWait         = 0x0
	// pipeMode rejects remote (SMB redirect) clients; identity is
	// only meaningful on the local machine.
	pipeMode = pipeTypeByte | pipeReadModeByte | pipeWait | windows.PIPE_REJECT_REMOTE_CLIENTS
	pipeBuf  = 64 << 10
)

// readGrantedSIDs parses the access file the helper writes: one SID string
// per line, # comments and blank lines ignored. An empty name means no file;
// a missing file means no grants.
func readGrantedSIDs(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sids []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sids = append(sids, line)
	}
	return sids, nil
}

// sidFromString converts a string SID (S-1-5-...) to a *windows.SID.
func sidFromString(s string) (*windows.SID, error) {
	u16, err := windows.UTF16FromString(s)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	if err := windows.ConvertStringSidToSid(&u16[0], &sid); err != nil {
		return nil, err
	}
	return sid, nil
}

// pipeSecurityDescriptor builds a protected DACL admitting SYSTEM,
// Administrators, and every granted SID with full pipe access. SDDL is used
// because CreateNamedPipe expects a self-relative descriptor.
func pipeSecurityDescriptor(granted []string) (*windows.SECURITY_DESCRIPTOR, error) {
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
	for _, sid := range granted {
		if sid == sidSystem || sid == sidAdministrators {
			continue
		}
		if _, err := sidFromString(sid); err != nil {
			return nil, fmt.Errorf("sid %s: %w", sid, err)
		}
		sddl += "(A;;GA;;;" + sid + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("pipe SDDL: %w", err)
	}
	return sd, nil
}

// createPipeInstance creates one pipe instance. The first instance uses
// FILE_FLAG_FIRST_PIPE_INSTANCE so a squatter that opened the pipe name
// earlier cannot shadow the daemon.
func createPipeInstance(name string, first bool, sa *windows.SecurityAttributes) (windows.Handle, error) {
	u16, err := windows.UTF16FromString(name)
	if err != nil {
		return 0, err
	}
	flags := uint32(0)
	if first {
		flags = windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	return windows.CreateNamedPipe(&u16[0], windows.PIPE_ACCESS_DUPLEX|flags, pipeMode, windows.PIPE_UNLIMITED_INSTANCES, pipeBuf, pipeBuf, 0, sa)
}

// pipeListener owns the named pipe: a bounded accept loop that impersonates
// each client to record its identity.
type pipeListener struct {
	name    string
	granted map[string]bool
	sa      *windows.SecurityAttributes

	mu         sync.Mutex
	connecting windows.Handle // instance blocked in ConnectNamedPipe
	pending    windows.Handle // first instance, created before the loop starts

	conns      chan *pipeConn
	active     chan struct{} // live-connection slots (the connection cap)
	done       chan struct{}
	acceptDone chan struct{}
}

// NewPipeListener creates the named-pipe control server at \\.\pipe\<name>.
//
// The DACL admits SYSTEM, Administrators, and each SID in accessFile (one
// per line, persisted by the helper). The first instance is created
// synchronously with FILE_FLAG_FIRST_PIPE_INSTANCE, so a squatter that
// opened the pipe name first makes this call fail. Instances use
// PIPE_UNLIMITED_INSTANCES; live connections are bounded by maxConns (an
// over-cap client is disconnected immediately instead of hanging).
//
// Each accepted connection is impersonated (thread-bound) so the client's
// user SID lands in the connection context; a client whose SID or groups are
// not admitted gets an unknown caller, which the API layer rejects.
func NewPipeListener(ctx context.Context, name, accessFile string, maxConns int) (net.Listener, error) {
	if name == "" {
		name = "skein-control"
	}
	if maxConns <= 0 {
		maxConns = DefaultMaxConns
	}
	granted := []string{sidSystem, sidAdministrators}
	sids, err := readGrantedSIDs(accessFile)
	if err != nil {
		return nil, err
	}
	granted = append(granted, sids...)

	p := &pipeListener{
		name:    `\\.\pipe\` + name,
		granted: make(map[string]bool, len(granted)),
		conns:   make(chan *pipeConn, maxConns),
		active:  make(chan struct{}, maxConns),
		done:    make(chan struct{}),
	}
	for _, s := range granted {
		p.granted[s] = true
	}

	sd, err := pipeSecurityDescriptor(granted)
	if err != nil {
		return nil, err
	}
	p.sa = &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}

	h, err := createPipeInstance(p.name, true, p.sa)
	if err != nil {
		return nil, fmt.Errorf("create first pipe instance: %w", err)
	}
	log.Printf("skein: pipe created name=%q handle=%v", p.name, h)
	p.pending = h
	p.acceptDone = make(chan struct{})
	go p.acceptLoop(ctx)
	return p, nil
}

// Accept returns the next verified client connection, or net.ErrClosed once
// Close has run.
func (p *pipeListener) Accept() (net.Conn, error) {
	c, ok := <-p.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}

func (p *pipeListener) Addr() net.Addr { return pipeAddr{name: p.name} }

// Close stops the accept loop and wakes the instance blocked in
// ConnectNamedPipe.
func (p *pipeListener) Close() error {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	p.mu.Lock()
	connecting := p.connecting
	pending := p.pending
	p.pending = 0
	p.mu.Unlock()
	for _, h := range []windows.Handle{connecting, pending} {
		if h != 0 {
			windows.DisconnectNamedPipe(h)
		}
	}
	<-p.acceptDone
	return nil
}

func (p *pipeListener) release() { <-p.active }

func (p *pipeListener) acceptLoop(ctx context.Context) {
	defer func() {
		log.Printf("skein: pipe accept loop exit name=%q", p.name)
		close(p.conns)
		close(p.acceptDone)
	}()
	for {
		// Bound live connections.
		select {
		case <-ctx.Done():
			return
		case <-p.done:
			return
		case p.active <- struct{}{}:
		default:
			// Connection cap reached: make a throwaway instance and
			// disconnect it so the over-cap client gets a prompt error
			// instead of hanging.
			if h, err := createPipeInstance(p.name, false, p.sa); err == nil {
				windows.ConnectNamedPipe(h, nil)
				windows.DisconnectNamedPipe(h)
				windows.CloseHandle(h)
			}
			select {
			case <-ctx.Done():
				return
			case <-p.done:
				return
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}

		var h windows.Handle
		var err error
		p.mu.Lock()
		if p.pending != 0 {
			h = p.pending
			p.pending = 0
		}
		p.mu.Unlock()
		if h == 0 {
			h, err = createPipeInstance(p.name, false, p.sa)
			if err != nil {
				log.Printf("skein: pipe create instance: %v", err)
				select {
				case <-ctx.Done():
					return
				case <-p.done:
					return
				case <-time.After(time.Second):
					continue
				}
			}
		}
		p.mu.Lock()
		p.connecting = h
		p.mu.Unlock()
		err = windows.ConnectNamedPipe(h, nil)
		p.mu.Lock()
		p.connecting = 0
		p.mu.Unlock()
		// ERROR_PIPE_CONNECTED: the client connected during
		// CreateNamedPipe; both outcomes are success.
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			log.Printf("skein: pipe connect: %v", err)
			windows.CloseHandle(h)
			continue
		}
		c := &pipeConn{h: h, name: p.name, release: p.release}
		c.caller = p.verifyClient(h)
		select {
		case p.conns <- c:
		case <-ctx.Done():
			_ = c.Close()
			return
		case <-p.done:
			_ = c.Close()
			return
		}
	}
}

// verifyClient impersonates the connected client (thread-bound) and returns
// its identity when the user SID or one of its groups is admitted; otherwise
// it returns an unknown caller, which the API layer rejects.
func (p *pipeListener) verifyClient(h windows.Handle) Caller {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var caller Caller
	if !impersonateNamedPipeClient(h) {
		return caller
	}
	defer windows.RevertToSelf()
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		return caller
	}
	defer tok.Close()

	if user, err := tok.GetTokenUser(); err == nil {
		caller.SID = user.User.Sid.String()
	}
	granted := p.granted[caller.SID]
	if !granted {
		if groups, err := tok.GetTokenGroups(); err == nil {
			for _, g := range groups.AllGroups() {
				if g.Sid != nil && p.granted[g.Sid.String()] {
					granted = true
					break
				}
			}
		}
	}
	if !granted {
		return Caller{}
	}
	return caller
}

func impersonateNamedPipeClient(h windows.Handle) bool {
	r, _, _ := impersonateNamedPipeClientProc.Call(uintptr(h))
	return r != 0
}

// pipeConn is one connected pipe instance. Reads and writes block
// (PIPE_WAIT); deadline methods are no-ops.
type pipeConn struct {
	h       windows.Handle
	name    string
	caller  Caller
	release func()

	once sync.Once
	err  error
}

func (c *pipeConn) Read(b []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(c.h, b, &n, nil)
	return int(n), err
}
func (c *pipeConn) Write(b []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(c.h, b, &n, nil)
	return int(n), err
}

// Close releases the pipe handle and the connection-cap slot.
func (c *pipeConn) Close() error {
	c.once.Do(func() {
		c.err = windows.CloseHandle(c.h)
		c.release()
	})
	return c.err
}
func (c *pipeConn) LocalAddr() net.Addr              { return pipeAddr{name: c.name} }
func (c *pipeConn) RemoteAddr() net.Addr             { return pipeAddr{name: c.name} }
func (c *pipeConn) SetDeadline(time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(time.Time) error { return nil }

// ConnContext carries the client identity from verifyClient, so the API
// authenticates the caller from the kernel instead of a bearer token.
func (c *pipeConn) ConnContext() context.Context { return WithCaller(context.Background(), c.caller) }

// pipeAddr is the pipe's network address.
type pipeAddr struct{ name string }

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return a.name }

// CurrentSID returns the primary user SID of the calling process. The helper
// persists it in the access file so the daemon admits it.
func CurrentSID() (string, error) {
	tok := windows.Token(windows.CurrentProcess())
	defer tok.Close()
	user, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

// VerifyPipeServer authenticates the daemon behind a connected pipe handle:
// the server process must exist and its image path must start with
// expectedImage (e.g. `C:\Program Files\Skein\skeind.exe`, compared
// case-insensitively). It returns the server PID.
func VerifyPipeServer(h windows.Handle, expectedImage string) (int, error) {
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(h, &pid); err != nil {
		return 0, fmt.Errorf("pipe server process id: %w", err)
	}
	if pid == 0 {
		return 0, errors.New("pipe reports no server process")
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, fmt.Errorf("open server process: %w", err)
	}
	defer windows.CloseHandle(proc)
	const maxPath = 1024
	buf := make([]uint16, maxPath)
	n, _, err := getProcessImageFileNameProc.Call(uintptr(proc), uintptr(unsafe.Pointer(&buf[0])), maxPath)
	if err != nil {
		return 0, fmt.Errorf("server image: %w", err)
	}
	image := windows.UTF16ToString(buf[:n])
	if !strings.HasPrefix(strings.ToLower(image), strings.ToLower(expectedImage)) {
		return 0, fmt.Errorf("server image %q is not under %q", image, expectedImage)
	}
	return int(pid), nil
}
