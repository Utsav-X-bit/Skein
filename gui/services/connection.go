package services

import (
	"context"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// ConnectionService pauses and resumes the dataplane. It is a daemon state,
// separate from service lifecycle.
type ConnectionService struct{}

// Status returns the current connection state.
func (s *ConnectionService) Status(ctx context.Context) (api.Connection, error) {
	c, err := ControlClient()
	if err != nil {
		return api.Connection{}, err
	}
	var st api.Status
	if err := c.do(ctx, "GET", "/api/v1/status", nil, &st); err != nil {
		return api.Connection{}, err
	}
	return st.Connection, nil
}

// Connect resumes the dataplane.
func (s *ConnectionService) Connect(ctx context.Context) error {
	c, err := ControlClient()
	if err != nil {
		return err
	}
	return c.do(ctx, "POST", "/api/v1/connection", map[string]string{"action": "connect"}, nil)
}

// Disconnect pauses the dataplane.
func (s *ConnectionService) Disconnect(ctx context.Context) error {
	c, err := ControlClient()
	if err != nil {
		return err
	}
	return c.do(ctx, "POST", "/api/v1/connection", map[string]string{"action": "disconnect"}, nil)
}
