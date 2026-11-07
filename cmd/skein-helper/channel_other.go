//go:build !windows

package main

import "errors"

// openPipeSession: on Unix the elevated helper keeps stdin/stdout, so the
// pipe channel (which exists because a UAC process has no stdio) is unused.
func openPipeSession(string) (*pipeSession, error) {
	return nil, errors.New("--pipe is the Windows channel; on Unix use stdin/stdout")
}

type pipeSession struct{}

func (s *pipeSession) verifyDesktop() error     { return nil }
func (s *pipeSession) writeResult([]byte) error { return nil }
