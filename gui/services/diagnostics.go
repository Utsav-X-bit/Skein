package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// DiagnosticsService exposes logs, security facts, and the support bundle.
type DiagnosticsService struct{}

func (s *DiagnosticsService) Logs(ctx context.Context, sinceMS int64, limit int) ([]api.LogEntry, error) {
	c, err := ControlClient()
	if err != nil {
		return nil, err
	}
	var out struct {
		Entries []api.LogEntry `json:"entries"`
	}
	err = c.do(ctx, "GET", fmt.Sprintf("/api/v1/logs?since=%d&limit=%d", sinceMS, limit), nil, &out)
	return out.Entries, err
}

func (s *DiagnosticsService) Bundle(ctx context.Context) (api.DiagnosticBundle, error) {
	c, err := ControlClient()
	if err != nil {
		return api.DiagnosticBundle{}, err
	}
	var out api.DiagnosticBundle
	err = c.do(ctx, "GET", "/api/v1/diagnostics", nil, &out)
	return out, err
}

func (s *DiagnosticsService) Security(ctx context.Context) (api.SecurityStatus, error) {
	c, err := ControlClient()
	if err != nil {
		return api.SecurityStatus{}, err
	}
	var out api.SecurityStatus
	err = c.do(ctx, "GET", "/api/v1/security", nil, &out)
	return out, err
}

// ExportBundle writes the redacted support bundle to a native save dialog
// path. The bundle is the same JSON the daemon serves; the file is 0600 so a
// shared machine does not leak peer names or paths to other users.
func (s *DiagnosticsService) ExportBundle(ctx context.Context) (string, error) {
	bundle, err := s.Bundle(ctx)
	if err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	name := "skein-diagnostics-" + time.Now().Format("20060102-150405") + ".json"
	path, err := saveFileDialog(name, "JSON", "*.json")
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", errors.New("diagnostics export cancelled")
	}
	if err := writeFileAtomic(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("diagnostics export: %w", err)
	}
	return path, nil
}
