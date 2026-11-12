package services

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/service"
)

// stubLifecycle replaces the OS reader and the elevated helper for one test
// and shortens the transition poll so the bounded wait is exercised without
// real sleeping. Tests using it must not run in parallel.
func stubLifecycle(t *testing.T, status func() (service.Status, error), helper func(context.Context, string, ...string) error) {
	t.Helper()
	oldRead, oldHelper := readServiceStatus, runHelper
	oldPoll, oldTimeout := lifecyclePoll, lifecycleTimeout
	readServiceStatus = status
	runHelper = helper
	lifecyclePoll = time.Millisecond
	lifecycleTimeout = time.Second
	t.Cleanup(func() {
		readServiceStatus, runHelper = oldRead, oldHelper
		lifecyclePoll, lifecycleTimeout = oldPoll, oldTimeout
	})
}

type helperCall struct {
	verb string
	args []string
}

func recordHelper(calls *[]helperCall, err error) func(context.Context, string, ...string) error {
	return func(_ context.Context, verb string, args ...string) error {
		*calls = append(*calls, helperCall{verb: verb, args: slices.Clone(args)})
		return err
	}
}

func fixedStatus(st service.State) func() (service.Status, error) {
	return func() (service.Status, error) { return service.Status{State: st}, nil }
}

// A zero helper exit is not success: if the authoritative state contradicts
// the requested outcome, the mutation must fail visibly.
func TestStartRejectsFalseHelperSuccess(t *testing.T) {
	var calls []helperCall
	stubLifecycle(t, fixedStatus(service.StateStopped), recordHelper(&calls, nil))

	err := (&ServiceService{}).Start(context.Background())
	if err == nil {
		t.Fatal("Start = nil, want error (helper succeeded but service is stopped)")
	}
	if !strings.Contains(err.Error(), "stopped") || !strings.Contains(err.Error(), "running") {
		t.Fatalf("Start error = %q, want both observed (stopped) and desired (running) states", err)
	}
	if len(calls) != 1 || calls[0].verb != "start" {
		t.Fatalf("helper calls = %+v, want one start", calls)
	}
}

// A helper failure is reconciled against the OS: when the state already shows
// the desired outcome the mutation succeeds (this is what lets Repair stop an
// already-removed service), and when it does not the error carries both the
// helper failure and the observed state.
func TestMutationReconcilesHelperFailureWithAuthoritativeState(t *testing.T) {
	helperErr := errors.New("pkexec: unit skein.service not loaded")

	var calls []helperCall
	stubLifecycle(t, fixedStatus(service.StateMissing), recordHelper(&calls, helperErr))
	if err := (&ServiceService{}).Stop(context.Background()); err != nil {
		t.Fatalf("Stop with missing service = %v, want nil (nothing running is the desired state)", err)
	}

	calls = nil
	stubLifecycle(t, fixedStatus(service.StateStopped), recordHelper(&calls, helperErr))
	err := (&ServiceService{}).Start(context.Background())
	if err == nil {
		t.Fatal("Start = nil, want error (helper failed and service is still stopped)")
	}
	if !strings.Contains(err.Error(), helperErr.Error()) || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("Start error = %q, want helper failure and authoritative state", err)
	}
}

// Installs, restarts and purges change things the service state cannot prove
// (which binary is registered, whether the process cycled, whether config was
// deleted), so a helper failure must surface even when the state looks right.
func TestUnobservableIntentsRequireHelperSuccess(t *testing.T) {
	helperErr := errors.New("elevation denied")

	cases := []struct {
		name  string
		state service.State
		call  func(*ServiceService) error
	}{
		{"install", service.StateStopped, func(s *ServiceService) error { return s.Install(context.Background()) }},
		{"install-with-daemon", service.StateStopped, func(s *ServiceService) error {
			return s.InstallWithDaemon(context.Background(), "/opt/skein/skeind")
		}},
		{"restart", service.StateRunning, func(s *ServiceService) error { return s.Restart(context.Background()) }},
		{"uninstall-purge", service.StateMissing, func(s *ServiceService) error {
			return s.UninstallWithPurge(context.Background(), true)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []helperCall
			stubLifecycle(t, fixedStatus(tc.state), recordHelper(&calls, helperErr))
			err := tc.call(&ServiceService{})
			if err == nil {
				t.Fatalf("%s = nil, want error (helper failed and state cannot confirm the intent)", tc.name)
			}
			if !strings.Contains(err.Error(), helperErr.Error()) {
				t.Fatalf("%s error = %q, want helper failure", tc.name, err)
			}
		})
	}

	// Plain uninstall's intent is observable: a missing service means the
	// registration is gone, so the same helper failure is reconciled.
	var calls []helperCall
	stubLifecycle(t, fixedStatus(service.StateMissing), recordHelper(&calls, helperErr))
	if err := (&ServiceService{}).Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall with missing service = %v, want nil (registration is gone)", err)
	}
}

// The Settings repair sequence is Stop → Install → Start through these same
// verbs. On a missing service the stop helper fails ("unit not loaded") while
// the authoritative state already shows nothing running, so repair must
// proceed to install and start instead of aborting on the helper error.
func TestRepairSequenceOnMissingService(t *testing.T) {
	state := service.StateMissing
	var verbs []string
	stubLifecycle(t,
		func() (service.Status, error) { return service.Status{State: state}, nil },
		func(_ context.Context, verb string, _ ...string) error {
			verbs = append(verbs, verb)
			switch verb {
			case "stop":
				return errors.New("unit skein.service not loaded")
			case "install":
				state = service.StateStopped
			case "start":
				state = service.StateRunning
			}
			return nil
		})

	svc := &ServiceService{}
	ctx := context.Background()
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("repair stop on missing service = %v, want nil", err)
	}
	if err := svc.Install(ctx); err != nil {
		t.Fatalf("repair install = %v, want nil", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("repair start = %v, want nil", err)
	}
	if !slices.Equal(verbs, []string{"stop", "install", "start"}) {
		t.Fatalf("repair helper verbs = %v, want [stop install start]", verbs)
	}
}

// An asynchronous start (SCM returns START_PENDING) is waited out instead of
// being reported as success or failure on the first read.
func TestStartWaitsForAsyncTransition(t *testing.T) {
	states := []service.State{service.StateStarting, service.StateStarting, service.StateRunning}
	reads := 0
	stubLifecycle(t, func() (service.Status, error) {
		i := min(reads, len(states)-1)
		reads++
		return service.Status{State: states[i]}, nil
	}, recordHelper(new([]helperCall), nil))

	if err := (&ServiceService{}).Start(context.Background()); err != nil {
		t.Fatalf("Start = %v, want nil after transition reaches running", err)
	}
	if reads < len(states) {
		t.Fatalf("state reads = %d, want >= %d (must poll through the transition)", reads, len(states))
	}
}

// The transition wait is bounded by the caller's context, not an unbounded
// poll: a service stuck in STARTING fails with the last observed state.
func TestTransitionWaitHonorsContext(t *testing.T) {
	stubLifecycle(t, fixedStatus(service.StateStarting), recordHelper(new([]helperCall), nil))
	lifecycleTimeout = time.Minute // the caller's deadline must win

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := (&ServiceService{}).Start(ctx)
	if err == nil {
		t.Fatal("Start = nil, want deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start error = %v, want context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "starting") {
		t.Fatalf("Start error = %q, want last observed state", err)
	}
}

// An unreadable state is never a success, even with a zero helper exit.
func TestMutationReportsUnreadableState(t *testing.T) {
	readErr := errors.New("systemctl: executable file not found in $PATH")
	stubLifecycle(t, func() (service.Status, error) { return service.Status{}, readErr },
		recordHelper(new([]helperCall), nil))

	err := (&ServiceService{}).Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), readErr.Error()) {
		t.Fatalf("Start error = %v, want unconfirmed state wrapping %v", err, readErr)
	}

	helperErr := errors.New("helper exit 1")
	stubLifecycle(t, func() (service.Status, error) { return service.Status{}, readErr },
		recordHelper(new([]helperCall), helperErr))
	err = (&ServiceService{}).Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), helperErr.Error()) || !strings.Contains(err.Error(), readErr.Error()) {
		t.Fatalf("Start error = %v, want both helper failure and state read failure", err)
	}
}

// The onboarding "Start service" path on a machine with no registered unit
// must explain setup, not repeat systemd's wording or the helper envelope.
func TestStartOnMissingServiceExplainsSetup(t *testing.T) {
	helperErr := errors.New("service: systemctl start: exit status 5: Failed to start skein.service: Unit skein.service not found.")
	stubLifecycle(t, fixedStatus(service.StateMissing), recordHelper(new([]helperCall), helperErr))

	err := (&ServiceService{}).Start(context.Background())
	if err == nil {
		t.Fatal("Start = nil, want error")
	}
	got := err.Error()
	if !strings.Contains(got, "isn't installed") || !strings.Contains(got, "Create or join a network") {
		t.Fatalf("Start error = %q, want plain setup guidance", got)
	}
	if strings.Contains(got, "systemctl") || strings.Contains(got, "missing") {
		t.Fatalf("Start error = %q, want no systemd or state jargon", got)
	}
}

// Boot start is a policy change: a zero helper exit is not success unless the
// OS reports the requested start type afterwards.
func TestSetStartAtBootVerifiesThePolicy(t *testing.T) {
	var calls []helperCall
	stubLifecycle(t, func() (service.Status, error) {
		return service.Status{State: service.StateRunning, Enabled: true}, nil
	}, recordHelper(&calls, nil))
	if err := (&ServiceService{}).SetStartAtBoot(context.Background(), true); err != nil {
		t.Fatalf("SetStartAtBoot(true) = %v", err)
	}
	if len(calls) != 1 || calls[0].verb != "enable" {
		t.Fatalf("helper calls = %+v, want one enable", calls)
	}

	calls = nil
	stubLifecycle(t, func() (service.Status, error) {
		return service.Status{State: service.StateRunning, Enabled: false}, nil
	}, recordHelper(&calls, nil))
	if err := (&ServiceService{}).SetStartAtBoot(context.Background(), false); err != nil {
		t.Fatalf("SetStartAtBoot(false) = %v", err)
	}
	if len(calls) != 1 || calls[0].verb != "disable" {
		t.Fatalf("helper calls = %+v, want one disable", calls)
	}

	stubLifecycle(t, fixedStatus(service.StateRunning), recordHelper(new([]helperCall), nil))
	if err := (&ServiceService{}).SetStartAtBoot(context.Background(), true); err == nil {
		t.Fatal("SetStartAtBoot = nil, want an error when the OS did not apply it")
	}
}
