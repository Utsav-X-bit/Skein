//go:build !windows

package link

import "syscall"

const msgTrunc = syscall.MSG_TRUNC
