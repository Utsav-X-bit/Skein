package services

import (
	"context"
	"runtime"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/version"
)

// AppService reports GUI and daemon identity and owns the event stream.
type AppService struct {
	cancel context.CancelFunc
}

// ServiceStartup starts the daemon event subscription.
func (s *AppService) ServiceStartup(ctx context.Context, _ any) error {
	streamCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go s.watchEvents(streamCtx)
	return nil
}

// ServiceShutdown stops the event subscription.
func (s *AppService) ServiceShutdown() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

// Capabilities returns the daemon protocol and feature flags.
func (s *AppService) Capabilities(ctx context.Context) (api.Capabilities, error) {
	c, err := ControlClient()
	if err != nil {
		return api.Capabilities{}, err
	}
	var out api.Capabilities
	err = c.do(ctx, "GET", "/api/v1/capabilities", nil, &out)
	return out, err
}

// Version returns this GUI build's version.
func (s *AppService) Version(context.Context) (string, error) { return version.Info(), nil }

// Platform returns the OS the GUI is running on.
func (s *AppService) Platform(context.Context) (string, error) { return runtime.GOOS, nil }
