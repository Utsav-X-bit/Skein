//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

// pipeSession is one helper connection to the GUI-created pipe. The GUI
// creates the instance, writes the request, and closes its write side; the
// helper reads to EOF, runs the verb, writes the result, and closes.
type pipeSession struct {
	conn net.Conn
}

// openPipeSession connects as a client to the pipe named by the GUI.
func openPipeSession(name string) (*pipeSession, error) {
	conn, err := pipeconn.Dial(name)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(180 * time.Second))
	return &pipeSession{conn: conn}, nil
}

// readRequest reads the complete request JSON.
func (s *pipeSession) readRequest() ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(s.conn, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("empty request on the helper pipe")
	}
	return data, nil
}

// verifyDesktop refuses to send a bootstrap token to a pipe server that is
// not the Skein desktop beside this helper. A squatter cannot create the
// first instance (the GUI uses FILE_FLAG_FIRST_PIPE_INSTANCE), and this check
// closes the remaining same-user impersonation case.
func (s *pipeSession) verifyDesktop() error {
	h, ok := pipeconn.ServerHandle(s.conn)
	if !ok {
		return errors.New("bootstrap pipe: no server handle")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("bootstrap pipe: helper path: %w", err)
	}
	expected := filepath.Join(filepath.Dir(exe), "skein-desktop.exe")
	if _, err := api.VerifyPipeServer(h, expected); err != nil {
		return fmt.Errorf("bootstrap pipe server is not the desktop: %w", err)
	}
	return nil
}

// writeResult sends the result JSON and closes the connection.
func (s *pipeSession) writeResult(payload []byte) error {
	if _, err := s.conn.Write(payload); err != nil {
		s.conn.Close()
		return err
	}
	return s.conn.Close()
}
