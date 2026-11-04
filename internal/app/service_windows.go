//go:build windows

package app

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"

	"github.com/Utsav-X-bit/skein/internal/service"
)

// isWindowsService and runAsService are variables (not plain
// functions) so tests can stub the launch-mode decision and the SCM
// handshake without a real Windows service manager. Production code
// never reassigns them.
var isWindowsService = func() (bool, error) { return svc.IsWindowsService() }

var runAsService = func(run func(context.Context) error) error {
	// A Windows service has no console. Persist daemon logs so startup
	// failures are diagnosable from ProgramData.
	if f, err := os.OpenFile(filepath.Join(SystemDir(), "skein.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(f)
		os.Stderr = f
		defer f.Close()
	}
	return svc.Run(service.Name, &serviceHandler{run: run})
}

type serviceHandler struct {
	run func(context.Context) error
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			log.Printf("skein: service run returned: %v", err)
			if err != nil {
				return false, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}
