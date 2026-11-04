package services

import (
	"context"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/service"
)

// ServiceService manages the daemon service lifecycle. Status reads the OS
// without elevation; mutations use the elevated helper.
type ServiceService struct{}

func (s *ServiceService) Status(context.Context) (api.ServiceStatus, error) {
	st, err := service.ReadStatus()
	if err != nil {
		return api.ServiceStatus{State: "error", Manager: managerName(), LastError: err.Error()}, nil
	}
	return api.ServiceStatus{State: string(st.State), Manager: managerName(), Installed: st.State != service.StateMissing}, nil
}

func (s *ServiceService) Install(ctx context.Context) error   { return runHelper(ctx, "install") }
func (s *ServiceService) Start(ctx context.Context) error     { return runHelper(ctx, "start") }
func (s *ServiceService) Stop(ctx context.Context) error      { return runHelper(ctx, "stop") }
func (s *ServiceService) Restart(ctx context.Context) error   { return runHelper(ctx, "restart") }
func (s *ServiceService) Uninstall(ctx context.Context) error { return runHelper(ctx, "uninstall") }

// InstallWithDaemon is Install with the packaging-known daemon path
// (plan §8.1: Install passes --daemon explicitly when the installer
// knows it, otherwise the helper resolves it). UninstallWithPurge is
// Uninstall with the purge flag the uninstall dialog needs. Both are
// new methods — the existing binding signatures above are unchanged,
// and the frontend wires these when bindings regenerate (M7).
func (s *ServiceService) InstallWithDaemon(ctx context.Context, daemonPath string) error {
	if daemonPath == "" {
		return s.Install(ctx)
	}
	return runHelper(ctx, "install", "--daemon", daemonPath)
}

func (s *ServiceService) UninstallWithPurge(ctx context.Context, purge bool) error {
	if !purge {
		return s.Uninstall(ctx)
	}
	return runHelper(ctx, "uninstall", "--purge")
}
