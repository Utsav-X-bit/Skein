//go:build windows

package app

// bootstrapTransport returns the service control transport for
// bootstrap mode on Windows: the control pipe name, no socket.
func bootstrapTransport() (socket, pipe string) { return "", ControlPipeName }
