//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Utsav-X-bit/skein/internal/pipeconn"
)

func readPipeRequest(name string) ([]byte, error) {
	c, err := pipeconn.Dial(name)
	if err != nil {
		return nil, fmt.Errorf("open helper pipe: %w", err)
	}
	defer c.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(io.LimitReader(c, 16<<10)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("read helper request: %w", err)
	}
	return raw, nil
}

func writePipeResult(name string, data []byte) error {
	c, err := pipeconn.Dial(name)
	if err != nil {
		return fmt.Errorf("open helper pipe: %w", err)
	}
	defer c.Close()
	_, err = c.Write(data)
	return err
}
