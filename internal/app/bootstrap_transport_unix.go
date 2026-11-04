//go:build !windows

package app

// bootstrapTransport returns the service control transport for
// bootstrap mode on Unix: the control socket, no pipe.
func bootstrapTransport() (socket, pipe string) { return ControlSocket, "" }
