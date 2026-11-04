//go:build !windows

package configstore

import (
	"os"
	"syscall"
)

// osLock is a Unix flock handle. flock locks are released by the OS on
// process death, so a crashed daemon never leaves a stale lock behind:
// no PID-file parsing, no "is the owner still alive" heuristic. A lock
// request on a second file description for the same file — even from the
// same process — is denied while the first is held, which is what makes
// the lock-held test meaningful in-process as well as across processes.
type osLock struct {
	f *os.File
}

func openLock(path string) (*osLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &osLock{f: f}, nil
}

func (l *osLock) lock() error {
	return syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func (l *osLock) unlock() error {
	return syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
}

func (l *osLock) close() error { return l.f.Close() }
