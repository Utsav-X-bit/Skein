//go:build windows

package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Utsav-X-bit/skein/internal/api"
)

const bootstrapPipeTimeout = 180 * time.Second

// currentProcessElevated reports whether this desktop process already has
// an elevated token. An elevated desktop can run the helper directly; a
// normal-token desktop uses the UAC pipe below.
func currentProcessElevated() bool {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		return false
	}
	defer tok.Close()
	return tok.IsElevated()
}

// createBootstrapPipe creates one random, local-only named pipe whose DACL
// admits only SYSTEM, Administrators and the current user. The first-instance
// flag prevents a squatter from creating the name before the desktop.
func createBootstrapPipe() (string, windows.Handle, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", 0, fmt.Errorf("bootstrap pipe name: %w", err)
	}
	name := `\\.\pipe\skein-bootstrap-` + hex.EncodeToString(nonce[:])
	sid, err := api.CurrentSID()
	if err != nil {
		return "", 0, fmt.Errorf("bootstrap pipe: caller SID: %w", err)
	}
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + sid + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return "", 0, fmt.Errorf("bootstrap pipe DACL: %w", err)
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", 0, err
	}
	h, err := windows.CreateNamedPipe(
		name16,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 64<<10, 64<<10, 0, sa,
	)
	if err != nil {
		return "", 0, fmt.Errorf("bootstrap pipe create: %w", err)
	}
	return name, h, nil
}

// mintBootstrapViaPipe launches the helper through UAC, has it mint the
// token, and reads the JSON envelope from the verified pipe. The token never
// enters argv, a temp file, or a log.
func mintBootstrapViaPipe(ctx context.Context, verb string) (string, error) {
	path := helperPath()
	name, h, err := createBootstrapPipe()
	if err != nil {
		return "", err
	}
	f := os.NewFile(uintptr(h), name)
	if f == nil {
		windows.CloseHandle(h)
		return "", errors.New("bootstrap pipe: wrap handle")
	}
	defer f.Close()

	argv := []string{"bootstrap", "--verb", verb, "--pipe", name}
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = windows.EscapeArg(arg)
	}
	verbPtr, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return "", err
	}
	filePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	argsPtr, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return "", err
	}
	if err := windows.ShellExecute(0, verbPtr, filePtr, argsPtr, nil, 0); err != nil {
		return "", fmt.Errorf("elevate helper: %w", err)
	}

	connected := make(chan error, 1)
	go func() { connected <- windows.ConnectNamedPipe(windows.Handle(f.Fd()), nil) }()
	select {
	case err := <-connected:
		if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			return "", fmt.Errorf("bootstrap pipe connect: %w", err)
		}
	case <-time.After(bootstrapPipeTimeout):
		return "", errors.New("helper did not connect; UAC may have been cancelled")
	case <-ctx.Done():
		return "", ctx.Err()
	}

	if err := verifyBootstrapClient(windows.Handle(f.Fd())); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return "", fmt.Errorf("bootstrap pipe read: %w", err)
	}
	return parseBootstrapToken(data, verb)
}

// verifyBootstrapClient rejects a pipe client whose image is not the Skein
// helper. The image read can be denied when the helper is elevated; the
// random name, first-instance flag and user-only DACL still constrain the
// client, and a wrong client can only corrupt the setup, never receive the
// token (the helper writes it).
func verifyBootstrapClient(h windows.Handle) error {
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(h, &pid); err != nil {
		return fmt.Errorf("bootstrap pipe client: %w", err)
	}
	image, err := api.PipeServerImage(pid)
	if err != nil {
		return nil
	}
	if !strings.HasSuffix(strings.ToLower(image), `\skein-helper.exe`) {
		return fmt.Errorf("bootstrap pipe client %q is not the Skein helper", image)
	}
	return nil
}
