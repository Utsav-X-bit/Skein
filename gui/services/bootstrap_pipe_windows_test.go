//go:build windows

package services

import (
	"io"
	"os"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

// The desktop side must accept exactly one local client and read its JSON
// envelope. UAC itself is not exercised here; the pipe mechanics are.
func TestBootstrapPipeReadsOneEnvelope(t *testing.T) {
	name, h, err := createBootstrapPipe()
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(h), name)
	defer f.Close()

	const envelope = `{"ok":true,"token":"test-token","verb":"join"}`
	writeErr := make(chan error, 1)
	go func() {
		conn, err := pipeconn.Dial(name)
		if err != nil {
			writeErr <- err
			return
		}
		_, err = conn.Write([]byte(envelope))
		conn.Close()
		writeErr <- err
	}()

	if err := windows.ConnectNamedPipe(windows.Handle(f.Fd()), nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
		t.Fatalf("ConnectNamedPipe: %v", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	if string(data) != envelope {
		t.Fatalf("pipe payload = %q, want %q", data, envelope)
	}
}
