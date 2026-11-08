package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/api"
)

// stubDiagnosticsBundle points the shared client at a local fake daemon and
// restores the previous client. Tests using it must not run in parallel.
func stubDiagnosticsBundle(t *testing.T, bundle api.DiagnosticBundle) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/diagnostics" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(bundle)
	}))
	t.Cleanup(srv.Close)
	old := defaultClient
	defaultClient = &Client{hc: srv.Client(), base: srv.URL}
	t.Cleanup(func() { defaultClient = old })
}

func stubSaveDialog(t *testing.T, fn func(string, string, string) (string, error)) {
	t.Helper()
	old := saveFileDialog
	saveFileDialog = fn
	t.Cleanup(func() { saveFileDialog = old })
}

func TestExportBundleCancelledWritesNothing(t *testing.T) {
	dir := t.TempDir()
	stubDiagnosticsBundle(t, api.DiagnosticBundle{Version: "test"})
	called := false
	stubSaveDialog(t, func(name, filterName, pattern string) (string, error) {
		called = true
		if !strings.HasPrefix(name, "skein-diagnostics-") || !strings.HasSuffix(name, ".json") {
			t.Errorf("suggested name = %q", name)
		}
		return "", nil
	})
	_, err := (&DiagnosticsService{}).ExportBundle(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancelled export error = %v", err)
	}
	if !called {
		t.Fatal("export skipped the dialog")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled export wrote %v", entries)
	}
}

func TestExportBundleWriteFailureIsAnError(t *testing.T) {
	stubDiagnosticsBundle(t, api.DiagnosticBundle{Version: "test"})
	stubSaveDialog(t, func(string, string, string) (string, error) {
		return filepath.Join(t.TempDir(), "missing", "bundle.json"), nil
	})
	_, err := (&DiagnosticsService{}).ExportBundle(context.Background())
	if err == nil {
		t.Fatal("write into a missing directory = nil, want error")
	}
}

func TestExportBundleIncludesRedactions(t *testing.T) {
	bundle := api.DiagnosticBundle{
		GeneratedMS: 1700000000000,
		Version:     "0.1.0",
		Protocol:    1,
		Platform:    "linux",
		Redactions:  []string{"api_token", "network_secret"},
	}
	stubDiagnosticsBundle(t, bundle)
	out := filepath.Join(t.TempDir(), "bundle.json")
	stubSaveDialog(t, func(string, string, string) (string, error) { return out, nil })

	path, err := (&DiagnosticsService{}).ExportBundle(context.Background())
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	if path != out {
		t.Fatalf("path = %q, want %q", path, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got api.DiagnosticBundle
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("exported JSON: %v", err)
	}
	if len(got.Redactions) != 2 || got.Redactions[0] != "api_token" || got.Redactions[1] != "network_secret" {
		t.Fatalf("redactions = %v", got.Redactions)
	}
	if got.Version != "0.1.0" {
		t.Fatalf("version = %q", got.Version)
	}
	if !strings.Contains(string(raw), "\n  ") {
		t.Fatal("export is not indented JSON")
	}
}

func TestExportBundleTightensExistingFileMode(t *testing.T) {
	stubDiagnosticsBundle(t, api.DiagnosticBundle{Version: "test"})
	out := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(out, []byte("stale bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(out, 0o644); err != nil {
		t.Fatal(err)
	}
	stubSaveDialog(t, func(string, string, string) (string, error) { return out, nil })

	if _, err := (&DiagnosticsService{}).ExportBundle(context.Background()); err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("overwritten export mode = %#o, want 0600", perm)
	}
}
