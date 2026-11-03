package services

import (
	"context"
	"fmt"

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
