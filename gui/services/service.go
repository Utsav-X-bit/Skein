package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
//
// Failures are rendered for the user, not the developer: see serviceFailure.
func (s *ServiceService) mutate(ctx context.Context, verb string, want stateWanted, args ...string) error {
	helperErr := runHelper(ctx, verb, args...)
	st, stateErr := observeState(ctx, want)
	if stateErr != nil {
		return unconfirmedState(verb, st.State, helperErr, stateErr)
	}
	if !want.holds(st.State) {
		return serviceFailure(verb, want, st.State, helperErr, false)
	}
	if helperErr != nil && want.helperMustSucceed {
		return serviceFailure(verb, want, st.State, helperErr, true)
	}
	return nil
}

// plainError is user-facing text with the technical cause kept for errors.Is
// and for logs. Error prints only the message, so a JSON envelope or an exit
// status never reaches the banner.
type plainError struct {
	msg   string
	cause error
}

func (e *plainError) Error() string { return e.msg }
func (e *plainError) Unwrap() error { return e.cause }

// serviceFailure explains a mutation that did not reach want. stateProves is
// set when the observed state already matches but the helper failed, so the
// result cannot be confirmed.
func serviceFailure(verb string, want stateWanted, st service.State, helperErr error, stateProves bool) error {
	detail := ""
	if helperErr != nil {
		detail = strings.ToLower(helperErr.Error())
	}
	switch {
	case st == service.StateMissing && (verb == "start" || verb == "restart"):
		// Nothing to retry: the unit is not registered, so setup is the
		// next step, not another start.
		return &plainError{
			msg:   "Skein's background service isn't installed on this machine yet. Create or join a network first; that installs it.",
			cause: helperErr,
		}
	case strings.Contains(detail, "config not found"), strings.Contains(detail, "no configuration"):
		return &plainError{
			msg:   "Skein has no network configuration on this machine yet. Create or join a network first.",
			cause: helperErr,
		}
	}
	switch {
	case stateProves:
		return &plainError{
			msg:   fmt.Sprintf("The service is %s, but the helper reported a failure, so the result is unconfirmed. Details: %s", stateName(st), helperErr),
			cause: helperErr,
		}
	case helperErr == nil:
		return &plainError{
			msg: fmt.Sprintf("The helper reported success, but the service is %s instead of %s. Open Diagnostics → Logs, then retry.", stateName(st), want.name),
		}
	default:
		if st == service.StateMissing {
			return &plainError{
				msg:   fmt.Sprintf("The service could not be %s. Details: %s", past(verb), helperErr),
				cause: helperErr,
			}
		}
		return &plainError{
			msg:   fmt.Sprintf("The service could not be %s; it is %s. Details: %s", past(verb), stateName(st), helperErr),
			cause: helperErr,
		}
	}
}

// unconfirmedState reports a mutation whose resulting state could not be read.
func unconfirmedState(verb string, st service.State, helperErr, stateErr error) error {
	cause := errors.Join(helperErr, stateErr)
	if errors.Is(stateErr, context.DeadlineExceeded) || errors.Is(stateErr, context.Canceled) {
		return &plainError{
			msg:   fmt.Sprintf("The service is still %s and did not finish %s in time. Open Diagnostics → Logs, then retry.", stateName(st), gerund(verb)),
			cause: cause,
		}
	}
	msg := fmt.Sprintf("Skein could not read the service state after %s (%s).", gerund(verb), stateErr)
	if helperErr != nil {
		msg += fmt.Sprintf(" The helper also failed (%s).", helperErr)
	}
	msg += " Open Diagnostics → Logs, then retry."
	return &plainError{msg: msg, cause: cause}
}

// stateName is the observed state as prose, not as the API's wire value.
func stateName(st service.State) string {
	switch st {
	case service.StateMissing:
		return "not installed"
	case service.StateStopped:
		return "stopped"
	case service.StateStarting:
		return "starting"
	case service.StateStopping:
		return "stopping"
	case service.StateRunning:
		return "running"
	case service.StateError:
		return "in a failed state"
	default:
		return "in an unknown state"
	}
}

func past(verb string) string {
	switch verb {
	case "install":
		return "installed"
	case "uninstall":
		return "removed"
	case "restart":
		return "restarted"
	case "stop":
		return "stopped"
	default:
		return "started"
	}
}

func gerund(verb string) string {
	switch verb {
	case "install":
		return "installing"
	case "uninstall":
		return "removing"
	case "restart":
		return "restarting"
	case "stop":
		return "stopping"
	default:
		return "starting"
	}
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
