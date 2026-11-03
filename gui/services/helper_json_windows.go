//go:build windows

package services

import (
	"context"
	"encoding/json"
	"os"
)

func runHelperJSON(ctx context.Context, verb string, payload any, args ...string) ([]byte, error) {
	path := os.Getenv("SKEIN_HELPER")
	if path == "" {
		path = defaultHelperPath()
	}
	requestPath := ""
	if payload != nil {
		req, err := os.CreateTemp("", "skein-helper-request-*.json")
		if err != nil {
			return nil, err
		}
		requestPath = req.Name()
		defer os.Remove(requestPath)
		if err := req.Chmod(0o600); err != nil {
			req.Close()
			return nil, err
		}
		if err := json.NewEncoder(req).Encode(payload); err != nil {
			req.Close()
			return nil, err
		}
		if err := req.Close(); err != nil {
			return nil, err
		}
	}
	res, err := os.CreateTemp("", "skein-helper-result-*.json")
	if err != nil {
		return nil, err
	}
	resultPath := res.Name()
	res.Close()
	os.Remove(resultPath)
	defer os.Remove(resultPath)
	return runElevatedHelper(ctx, path, verb, requestPath, resultPath, args...)
}
