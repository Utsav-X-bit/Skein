//go:build windows

package configstore

import (
	"golang.org/x/sys/windows"
)

// osLock is a Windows LockFileEx handle over the first byte of the lock
// file. Every party locks the same byte range, so the first byte is the
// whole-file mutex. LockFileEx locks are released by the OS on process
// death, so a crashed service never leaves a stale lock behind. This
// path is compile-checked here; runtime behaviour on a real Windows
// machine is explicitly deferred to M3 Tier 5 (see the Step 3 report).
type osLock struct {
	h windows.Handle
}

func openLock(path string) (*osLock, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Share mode must allow other processes to open the file: exclusion
	// comes from LockFileEx, not from denying the open. With share mode 0
	// a second process would fail at CreateFile instead of getting
	// ErrLocked from the lock attempt.
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return &osLock{h: h}, nil
}

func (l *osLock) lock() error {
	var ov windows.Overlapped
	return windows.LockFileEx(l.h,
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &ov)
}

func (l *osLock) unlock() error {
	var ov windows.Overlapped
	return windows.UnlockFileEx(l.h, 0, 1, 0, &ov)
}

func (l *osLock) close() error { return windows.CloseHandle(l.h) }
