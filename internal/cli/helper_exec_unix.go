//go:build !windows

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// helperNoPkexec is the same dev-only bypass the GUI uses. securityEnv
// is empty in release builds, so a release CLI always elevates.
func helperNoPkexec() bool { return securityEnv("SKEIN_HELPER_NO_PKEXEC") == "1" }

// geteuid is a variable so the elevation decision is testable without
// running the test binary as root.
var geteuid = os.Geteuid

// helperInvocation returns the program and argv for one helper verb.
// Root (and the dev-tree bypass) runs the packaged helper directly.
// Everyone else goes through pkexec with the shipped polkit action:
// the helper binds its bootstrap token to PKEXEC_UID, while the CLI
// itself stays the invoking user and posts the token over the kernel
// socket as that same uid. No shell is involved.
func helperInvocation(helperPath, verb string, args []string) (string, []string) {
	direct := append([]string{verb}, args...)
	if verb == "status" || geteuid() == 0 || helperNoPkexec() || runtime.GOOS != "linux" {
		return helperPath, direct
	}
	return "pkexec", append([]string{helperPath}, direct...)
}

// execSystemHelper runs one minimal-helper verb through the platform's
// approved elevation path. Stdout is captured in memory only: a
// bootstrap token is never put in argv, the environment, or a file.
func execSystemHelper(verb string, args ...string) ([]byte, error) {
	helperPath := systemHelperPath()
	program, argv := helperInvocation(helperPath, verb, args)
	cmd := exec.Command(program, argv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), helperExecError(verb, helperPath, program, err, errb.String())
	}
	return out.Bytes(), nil
}

// helperExecError turns an exec failure into the next step. A helper
// failure still returns its stdout JSON to the caller (mintSystemToken
// parses the envelope), so the exec error only carries stderr and the
// elevation diagnosis.
func helperExecError(verb, helperPath, program string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if errors.Is(err, exec.ErrNotFound) {
		if program == "pkexec" {
			return fmt.Errorf("helper %s: pkexec is not installed; install polkit, or run `sudo %s %s`", verb, helperPath, verb)
		}
		return fmt.Errorf("helper %s: %s not found; is the Skein package installed?", verb, helperPath)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && program == "pkexec" {
		switch exitErr.ExitCode() {
		case 126:
			return fmt.Errorf("helper %s: elevation was cancelled or not authorized; approve the polkit prompt, or run `sudo %s %s`", verb, helperPath, verb)
		case 127:
			return fmt.Errorf("helper %s: pkexec could not execute %s; install polkit, or run `sudo %s %s`", verb, helperPath, helperPath, verb)
		}
	}
	if stderr != "" {
		return fmt.Errorf("helper %s: %w: %s", verb, err, stderr)
	}
	return fmt.Errorf("helper %s: %w", verb, err)
}
