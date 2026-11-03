//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

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
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
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

// writeResult sends the result JSON and closes the connection.
func (s *pipeSession) writeResult(payload []byte) error {
	if _, err := s.conn.Write(payload); err != nil {
		s.conn.Close()
		return err
	}
	return s.conn.Close()
}
