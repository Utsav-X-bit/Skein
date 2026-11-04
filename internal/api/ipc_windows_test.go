//go:build windows

package api

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// waitNamedPipeProc is kernel32's WaitNamedPipeW (not wrapped by
// x/sys/windows). It waits until a pipe instance is listening.
var waitNamedPipeProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")

func waitNamedPipe(t *testing.T, name *uint16, timeoutMs uint32) error {
	t.Helper()
	r1, _, errno := waitNamedPipeProc.Call(uintptr(unsafe.Pointer(name)), uintptr(timeoutMs))
	if r1 == 0 {
		if errno != nil {
			return errno
		}
		return errors.New("wait named pipe failed")
	}
	return nil
}

func mustUTF16(t *testing.T, s string) *uint16 {
	t.Helper()
	u16, err := windows.UTF16FromString(s)
	if err != nil {
		t.Fatal(err)
	}
	return &u16[0]
}

// dialPipe connects to the named pipe as a raw client, mirroring
// pipeconn.Dial's retry on ERROR_PIPE_BUSY (the daemon's accept loop
// has a transient window with no listening instance).
func dialPipe(t *testing.T, name string) net.Conn {
	t.Helper()
	name16 := mustUTF16(t, name)
	var h windows.Handle
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		h, err = windows.CreateFile(name16, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			break
		}
		if err != windows.ERROR_PIPE_BUSY {
			break
		}
		if werr := waitNamedPipe(t, name16, 1000); werr != nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("dial %s: %v", name, err)
	}
	return &pipeConn{h: h, name: name, release: func() {}}
}

// oneShotTransport serves one HTTP request over a pre-connected pipe.
type oneShotTransport struct{ conn net.Conn }

func (ot oneShotTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Write(ot.conn); err != nil {
		return nil, err
	}
	return http.ReadResponse(bufio.NewReader(ot.conn), req)
}

// TestPipeSquat: FILE_FLAG_FIRST_PIPE_INSTANCE keeps a squatter-first
// bind from shadowing the daemon, and a late squatter instance must not
// divert clients from the daemon's listening instance. (Windows allows
// multiple pipe instances, so late creation itself cannot fail; the
// client-side defence against a listening squatter is VerifyPipeServer.)

func TestPipeSquat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	squatted := "skein-test-squat"
	squatter, err := windows.CreateNamedPipe(
		mustUTF16(t, `\\.\pipe\`+squatted), windows.PIPE_ACCESS_DUPLEX, pipeMode, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatalf("squatter create: %v", err)
	}
	defer windows.CloseHandle(squatter)
	if _, err := NewPipeListener(ctx, squatted, "", 4); err == nil {
		t.Fatal("listener bound over a squatted pipe name")
	}

	late := "skein-test-late"
	access := filepath.Join(t.TempDir(), "access")
	sid, err := CurrentSID()
	if err != nil {
		t.Fatalf("current sid: %v", err)
	}
	if err := os.WriteFile(access, []byte(sid+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := NewPipeListener(ctx, late, access, 4)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	defer ln.Close()
	// A late squatter can create a second instance; what must hold is
	// that the daemon keeps serving clients on its listening instance.
	squatterLate, err := windows.CreateNamedPipe(
		mustUTF16(t, `\\.\pipe\`+late), windows.PIPE_ACCESS_DUPLEX, pipeMode, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatalf("late squatter create: %v", err)
	}
	defer windows.CloseHandle(squatterLate)
	srv, _, _, _ := newTestServer(t)
	// Production serves pipe listeners with ConnContext =
	// api.CallerContext (daemon/listeners.go); without it the
	// kernel caller never reaches wrapRole and every call 401s.
	go func() { _ = (&http.Server{Handler: srv.Handler(), ConnContext: CallerContext}).Serve(ln) }()
	c := dialPipe(t, `\\.\pipe\`+late)
	defer c.Close()
	req, _ := http.NewRequest(http.MethodGet, "http://skein/api/v1/status", nil)
	resp, err := (&http.Client{Transport: oneShotTransport{conn: c}}).Do(req)
	if err != nil {
		t.Fatalf("dial after late squat: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status after late squat = %d, want 200", resp.StatusCode)
	}
}

// TestPipeTwoClients: two concurrent clients authenticate by kernel
// identity (no bearer token) against a DACL that admits the current user.
func TestPipeTwoClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sid, err := CurrentSID()
	if err != nil {
		t.Fatalf("current sid: %v", err)
	}
	access := filepath.Join(t.TempDir(), "access")
	if err := os.WriteFile(access, []byte(sid+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	name := "skein-test-two"
	ln, err := NewPipeListener(ctx, name, access, 8)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	defer ln.Close()

	srv, _, _, _ := newTestServer(t)
	// Same ConnContext wiring as production (see TestPipeSquat).
	go func() { _ = (&http.Server{Handler: srv.Handler(), ConnContext: CallerContext}).Serve(ln) }()

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := dialPipe(t, `\\.\pipe\`+name)
			defer c.Close()
			req, _ := http.NewRequest(http.MethodGet, "http://skein/api/v1/status", nil)
			resp, err := (&http.Client{Transport: oneShotTransport{conn: c}}).Do(req)
			if err != nil {
				t.Errorf("request: %v", err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200 (kernel identity, no bearer)", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
}

// TestMatchServerImage is the Windows unit-logic half of Step 2
// server-identity verification. It runs only on Windows (this file
// is windows-only); on Linux it is compile-checked by the Windows
// cross-build, not executed. The device-path cases cover the
// pre-Step-2 bug: GetProcessImageFileNameW returns an NT device
// path, so a drive-letter expectation must match by suffix, not by
// prefix.
func TestMatchServerImage(t *testing.T) {
	device := `\Device\HarddiskVolume3\Program Files\Skein\skeind.exe`
	cases := []struct {
		image, expected string
		want            bool
	}{
		{device, `C:\Program Files\Skein\skeind.exe`, true},
		{device, `C:\Program Files\Skein`, true},
		{device, `C:\Program Files\Skein\skein.exe`, false},
		{device, `C:\Other\skeind.exe`, false},
		{device, "", true},
		{`\Device\HarddiskVolume3\Windows\notepad.exe`, "", false},
		{`C:\Program Files\Skein\skeind.exe`, `C:\Program Files\Skein\skeind.exe`, true},
		{`c:\program files\skein\skeind.exe`, `C:\Program Files\Skein\skeind.exe`, true},
	}
	for _, tc := range cases {
		if got := matchServerImage(tc.image, tc.expected); got != tc.want {
			t.Errorf("matchServerImage(%q, %q) = %v, want %v", tc.image, tc.expected, got, tc.want)
		}
	}
}
