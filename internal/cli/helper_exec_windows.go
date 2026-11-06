//go:build windows

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// execSystemHelper runs one minimal-helper verb.
//
// bootstrap stays on the inherited-stdout channel: its token is
// stdout-only (the helper refuses --result for that verb), and UAC
// cannot hand a child's stdout back to an unelevated parent. Writing
// the token to a result file instead would leak it to disk, so this
// verb needs an already-elevated CLI and fails with that exact
// remediation when it is not.
//
// Every other verb goes through the UAC "runas" verb and a temporary
// result file, matching gui/services/helper_elevated_windows.go. Those
// results are service state only, never a secret, and the file is
// removed by this process.
func execSystemHelper(verb string, args ...string) ([]byte, error) {
	helperPath := systemHelperPath()
	elevated := windows.GetCurrentProcessToken().IsElevated()
	if verb == "bootstrap" && !elevated {
		return nil, fmt.Errorf("first setup needs an elevated CLI on Windows: run Start-Process powershell -Verb RunAs, then rerun your original system init/join/import command using & '%s'; keep any invite private. Skein Desktop can perform setup without elevating the desktop", filepath.Join(filepath.Dir(helperPath), "skein.exe"))
	}
	if verb == "bootstrap" || verb == "status" || elevated {
		cmd := exec.Command(helperPath, append([]string{verb}, args...)...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			if errb.Len() > 0 {
				return out.Bytes(), fmt.Errorf("helper %s: %w: %s", verb, err, strings.TrimSpace(errb.String()))
			}
			return out.Bytes(), fmt.Errorf("helper %s: %w (bootstrap on Windows needs an elevated prompt: UAC cannot return the stdout token to an unelevated CLI)", verb, err)
		}
		return out.Bytes(), nil
	}

	res, err := os.CreateTemp("", "skein-helper-result-*.json")
	if err != nil {
		return nil, err
	}
	resultPath := res.Name()
	res.Close()
	os.Remove(resultPath)
	defer os.Remove(resultPath)
	raw, err := runElevatedHelperWindows(helperPath, verb, resultPath, args...)
	if err != nil {
		return raw, err
	}
	var outcome struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &outcome); err != nil {
		return raw, fmt.Errorf("helper %s: invalid result: %w", verb, err)
	}
	if !outcome.OK {
		return raw, fmt.Errorf("helper %s: %s", verb, outcome.Error)
	}
	return raw, nil
}

// runElevatedHelperWindows launches the helper through the UAC "runas"
// verb and waits for its JSON result file. The helper cannot inherit
// redirected stdio across an elevation boundary, so the result travels
// in a user-only temp file created here.
func runElevatedHelperWindows(path, verb, resultPath string, args ...string) ([]byte, error) {
	argv := append([]string{verb, "--result", resultPath}, args...)
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = windows.EscapeArg(arg)
	}
	verbPtr, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return nil, err
	}
	filePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	argsPtr, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return nil, err
	}
	if err := windows.ShellExecute(0, verbPtr, filePtr, argsPtr, nil, 0); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return nil, fmt.Errorf("helper %s: elevation was cancelled", verb)
		}
		return nil, fmt.Errorf("elevate helper %s: %w", verb, err)
	}

	deadline := time.Now().Add(120 * time.Second)
	for {
		if data, err := os.ReadFile(resultPath); err == nil && len(data) > 0 {
			return data, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("helper %s did not return a result; UAC may have been cancelled", verb)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
