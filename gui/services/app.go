package services

import (
	"context"
	"log"
	"runtime"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/service"
	"github.com/Utsav-X-bit/skein/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// AppService reports GUI and daemon identity and owns the event stream.
type AppService struct {
	cancel context.CancelFunc
}

var _ application.ServiceStartup = (*AppService)(nil)

// ServiceStartup starts the daemon event subscription.
func (s *AppService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	streamCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go s.watchEvents(streamCtx)
	// The service is manual-start by default: opening the desktop starts it.
	go startInstalledService(streamCtx)
	return nil
}

// startInstalledService starts an installed, stopped service so launching the
// desktop brings the mesh up without a boot-time unit. It never installs, and
// it leaves a running or mid-transition service alone.
func startInstalledService(ctx context.Context) {
	// Android VPN consent and connection must be user-initiated.
	if runtime.GOOS == "android" {
		return
	}
	st, err := readServiceStatus()
	if err != nil || st.State == service.StateMissing || st.State == service.StateRunning ||
		st.State == service.StateStarting || st.State == service.StateStopping {
		return
	}
	if err := (&ServiceService{}).Start(ctx); err != nil {
		log.Printf("skein: desktop: starting the service on launch: %v", err)
	}
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
