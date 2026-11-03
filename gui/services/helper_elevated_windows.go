//go:build windows

package services

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// runElevatedHelper launches the helper through the UAC "runas" verb and
// waits for the JSON result file. The helper cannot inherit redirected stdio
// across an elevation boundary, so request/result travel in user-only temp
// files created here and removed by the caller.
func runElevatedHelper(ctx context.Context, path, verb, requestPath, resultPath string, args ...string) ([]byte, error) {
	argv := []string{verb, "--result", resultPath}
	if requestPath != "" {
		argv = append(argv, "--request", requestPath)
	}
	argv = append(argv, args...)
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
		return nil, fmt.Errorf("elevate helper: %w", err)
	}

	deadline := time.Now().Add(120 * time.Second)
	for {
		if data, err := os.ReadFile(resultPath); err == nil && len(data) > 0 {
			return data, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("helper did not return a result; UAC may have been cancelled")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
