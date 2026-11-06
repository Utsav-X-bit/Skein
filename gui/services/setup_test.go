package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/service"
)

type recoveryTransport func(*http.Request) (*http.Response, error)

func (f recoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Once reset succeeds, a failed helper must not erase its backup from the
// Wails response or invite the user to repeat the destructive operation.
func TestResetPreservesRecoveryAfterStopFailure(t *testing.T) {
	oldClient, oldHelper, oldStatus := defaultClient, runHelper, readServiceStatus
	t.Cleanup(func() { defaultClient, runHelper, readServiceStatus = oldClient, oldHelper, oldStatus })
	defaultClient = &Client{base: "http://skein", hc: &http.Client{Transport: recoveryTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"backup":"/isolated/config.reset-bak-1","firewall_error":"permission denied"}`)), Header: make(http.Header)}, nil
	})}}
	runHelper = func(context.Context, string, ...string) error { return errors.New("approval denied") }
	readServiceStatus = func() (service.Status, error) { return service.Status{State: service.StateRunning}, nil }
	result, err := (&SetupService{}).ResetWithPurge(context.Background(), false, true)
	if err != nil {
		t.Fatalf("completed reset returned an error that would discard its Wails result: %v", err)
	}
	// Exercise the serialization boundary, not merely the Go struct: JS needs
	// both the completed backup and the retryable stop failure.
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]string
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response["backup"] != "/isolated/config.reset-bak-1" || response["firewall_error"] != "permission denied" || !strings.Contains(response["stop_error"], "running") {
		t.Fatalf("partial completion evidence lost across the binding: %s", raw)
	}
}
