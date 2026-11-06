package services

import (
	"context"
	"fmt"
	"time"

	"github.com/Utsav-X-bit/skein/internal/api"
	"github.com/Utsav-X-bit/skein/internal/service"
)

// ServiceService manages the daemon service lifecycle. Status reads the OS
// without elevation; mutations use the elevated helper.
//
// The helper's exit status is not the outcome. systemd and the SCM accept a
// control and report it before the service finishes moving, and a helper can
// fail after it already changed the registration (or fail because the service
// was already in the requested state). Every mutation therefore re-reads the
// authoritative OS state afterwards, waits out a start/stop transition bounded
// by the caller's context, and reports success only when the observed state is
// the desired one — and, where state alone cannot prove the intent (install,
// restart, purge), only when the helper also succeeded. A state mismatch or an
// unreadable state is always an error, even when the helper exited zero.
type ServiceService struct{}

// Indirections for tests: the real implementations talk to systemd/SCM and to
// pkexec/UAC.
var (
	readServiceStatus = service.ReadStatus
	// lifecycleTimeout bounds the post-mutation wait when ctx carries no
	// deadline of its own.
	lifecycleTimeout = 30 * time.Second
	// lifecyclePoll is the re-read interval while a transition is in flight.
	lifecyclePoll = 250 * time.Millisecond
)

func (s *ServiceService) Status(context.Context) (api.ServiceStatus, error) {
	st, err := readServiceStatus()
	if err != nil {
		return api.ServiceStatus{State: "error", Manager: managerName(), LastError: err.Error()}, nil
	}
	return api.ServiceStatus{State: string(st.State), Manager: managerName(), Installed: st.State != service.StateMissing}, nil
}

func (s *ServiceService) Install(ctx context.Context) error {
	return s.mutate(ctx, "install", wantInstalled)
}

func (s *ServiceService) Start(ctx context.Context) error {
	return s.mutate(ctx, "start", wantRunning)
}

func (s *ServiceService) Stop(ctx context.Context) error {
	return s.mutate(ctx, "stop", wantStopped)
}

func (s *ServiceService) Restart(ctx context.Context) error {
	return s.mutate(ctx, "restart", wantRestarted)
}

func (s *ServiceService) Uninstall(ctx context.Context) error {
	return s.mutate(ctx, "uninstall", wantMissing)
}

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
	return s.mutate(ctx, "install", wantInstalled, "--daemon", daemonPath)
}

func (s *ServiceService) UninstallWithPurge(ctx context.Context, purge bool) error {
	if !purge {
		return s.Uninstall(ctx)
	}
	return s.mutate(ctx, "uninstall", wantPurged, "--purge")
}

// stateWanted describes the authoritative outcome a mutation must leave
// behind. name is what the error messages call it. helperMustSucceed is set
// when the observed state alone cannot prove the intent — the state cannot
// tell which daemon binary an install registered, whether a restart actually
// cycled the process, or whether a purge removed config as well — so a helper
// failure must surface even if the state looks right.
type stateWanted struct {
	name              string
	helperMustSucceed bool
	holds             func(service.State) bool
}

var (
	wantRunning = stateWanted{name: "running", holds: func(st service.State) bool {
		return st == service.StateRunning
	}}
	// A missing service satisfies "stopped": nothing is running, which is
	// what lets Repair stop an already-removed service and continue.
	wantStopped = stateWanted{name: "stopped", holds: func(st service.State) bool {
		return st == service.StateStopped || st == service.StateMissing
	}}
	// Any registered state is installed; unknown means the registration
	// exists but its state could not be read, so it cannot be confirmed.
	// The state does not name the registered binary, so the helper must
	// also have succeeded.
	wantInstalled = stateWanted{name: "installed", helperMustSucceed: true, holds: func(st service.State) bool {
		return st != service.StateMissing && st != service.StateUnknown
	}}
	// Uninstall's intent is fully observable: the registration is gone.
	wantMissing = stateWanted{name: "not installed", holds: func(st service.State) bool {
		return st == service.StateMissing
	}}
	// A purge also deletes config and identity, which the service state
	// cannot prove, so the helper must have succeeded.
	wantPurged = stateWanted{name: "not installed", helperMustSucceed: true, holds: func(st service.State) bool {
		return st == service.StateMissing
	}}
	// A restart must actually cycle the process; "running" alone does not
	// prove the old process was replaced.
	wantRestarted = stateWanted{name: "running", helperMustSucceed: true, holds: func(st service.State) bool {
		return st == service.StateRunning
	}}
)

// mutate runs one helper verb and then reconciles it with the OS state. The
// helper's error is included in every error it returns. A helper failure whose
// observed state already satisfies want is not an error when the state proves
// the whole intent (start/stop/uninstall); wants that the state cannot prove
// (install/restart/purge) set helperMustSucceed and always surface it.
func (s *ServiceService) mutate(ctx context.Context, verb string, want stateWanted, args ...string) error {
	helperErr := runHelper(ctx, verb, args...)
	st, stateErr := observeState(ctx, want)
	if stateErr != nil {
		if helperErr != nil {
			return fmt.Errorf("service %s: helper failed: %v; last observed state %s, result unconfirmed: %w", verb, helperErr, st.State, stateErr)
		}
		return fmt.Errorf("service %s: helper reported success but last observed state %s is unconfirmed: %w", verb, st.State, stateErr)
	}
	if !want.holds(st.State) {
		if helperErr != nil {
			return fmt.Errorf("service %s: helper failed: %v (authoritative state after mutation: %s, want %s)", verb, helperErr, st.State, want.name)
		}
		return fmt.Errorf("service %s: helper reported success but authoritative state is %s, want %s", verb, st.State, want.name)
	}
	if helperErr != nil && want.helperMustSucceed {
		return fmt.Errorf("service %s: helper failed: %v (authoritative state after mutation is %s, but that state cannot confirm the requested result)", verb, helperErr, st.State)
	}
	return nil
}

// observeState reads the authoritative OS state after a mutation. A
// start/stop transition is polled until it reaches a terminal state or the
// desired one, bounded by ctx (with a default timeout when ctx has none).
func observeState(ctx context.Context, want stateWanted) (service.Status, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lifecycleTimeout)
		defer cancel()
	}
	ticker := time.NewTicker(lifecyclePoll)
	defer ticker.Stop()
	for {
		st, err := readServiceStatus()
		if err != nil {
			return service.Status{}, err
		}
		if want.holds(st.State) || !transitional(st.State) {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-ticker.C:
		}
	}
}

func transitional(st service.State) bool {
	return st == service.StateStarting || st == service.StateStopping
}
