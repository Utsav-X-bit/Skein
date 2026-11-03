//go:build !windows

package main

import "errors"

var _ = readPipeRequest
var _ = writePipeResult

func readPipeRequest(string) ([]byte, error) {
	return nil, errors.New("--pipe is only used on Windows")
}

func writePipeResult(string, []byte) error {
	return errors.New("--pipe is only used on Windows")
}
