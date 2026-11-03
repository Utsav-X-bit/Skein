//go:build !windows

package services

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
)

func runHelperJSON(ctx context.Context, verb string, payload any, args ...string) ([]byte, error) {
	path := os.Getenv("SKEIN_HELPER")
	if path == "" {
		path = defaultHelperPath()
	}
	cmd := helperCommand(ctx, path, append([]string{verb}, args...)...)
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		cmd.Stdin = bytes.NewReader(raw)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	if runErr != nil {
		return out.Bytes(), runErr
	}
	return out.Bytes(), nil
}
