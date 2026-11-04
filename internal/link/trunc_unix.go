//go:build !windows

package link

import "syscall"

const msgTrunc = syscall.MSG_TRUNC

// isTruncationError is false on Unix: truncated datagrams surface via
// the MSG_TRUNC flag, not an error.
func isTruncationError(error) bool { return false }
