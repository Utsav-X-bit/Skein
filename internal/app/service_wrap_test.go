package app

import (
	"context"
	"errors"
	"testing"
)

// runWithService must route a service-launched process through the
// SCM wrapper and a foreground process directly. M9 §2c: the
// bootstrap branch previously skipped the wrapper, so a
// service-launched bootstrap daemon never reported to the SCM and
// its start timed out (1053) even though it was serving.
func TestRunWithServiceRoutesByLaunchMode(t *testing.T) {
	origIS, origRAS := isWindowsService, runAsService
	t.Cleanup(func() { isWindowsService, runAsService = origIS, origRAS })

	t.Run("service goes through wrapper", func(t *testing.T) {
		wrapped, direct := false, false
		isWindowsService = func() (bool, error) { return true, nil }
		runAsService = func(func(context.Context) error) error { wrapped = true; return nil }
		if err := runWithService(context.Background(), func(context.Context) error { direct = true; return nil }); err != nil {
			t.Fatalf("runWithService: %v", err)
		}
		if !wrapped || direct {
			t.Fatalf("service mode: wrapped=%v direct=%v, want wrapped only", wrapped, direct)
		}
	})

	t.Run("foreground runs directly", func(t *testing.T) {
		wrapped, direct := false, false
		isWindowsService = func() (bool, error) { return false, nil }
		runAsService = func(func(context.Context) error) error { wrapped = true; return nil }
		if err := runWithService(context.Background(), func(context.Context) error { direct = true; return nil }); err != nil {
			t.Fatalf("runWithService: %v", err)
		}
		if wrapped || !direct {
			t.Fatalf("foreground mode: wrapped=%v direct=%v, want direct only", wrapped, direct)
		}
	})

	t.Run("launch check error falls back to direct", func(t *testing.T) {
		direct := false
		isWindowsService = func() (bool, error) { return false, errors.New("no service manager") }
		runAsService = func(func(context.Context) error) error {
			t.Fatal("wrapper must not run when the check errors")
			return nil
		}
		if err := runWithService(context.Background(), func(context.Context) error { direct = true; return nil }); err != nil {
			t.Fatalf("runWithService: %v", err)
		}
		if !direct {
			t.Fatal("check error must fall back to a direct run")
		}
	})
}
