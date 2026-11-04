//go:build windows

package link

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
)

// MSG_TRUNC from ws2def.h.
const msgTrunc = 0x0100

// isTruncationError reports whether err is a truncated-datagram error.
// On Windows ReadMsgUDP returns WSAEMSGSIZE ("wsarecvmsg: ...") instead
// of setting MSG_TRUNC, so the Recv paths map it to ErrTruncated.
func isTruncationError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, windows.WSAEMSGSIZE) {
		return true
	}
	return strings.Contains(err.Error(), "wsarecvmsg")
}
