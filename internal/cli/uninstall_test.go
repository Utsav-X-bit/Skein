package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestUninstallDeclined(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cmdUninstall(context.Background(), nil, strings.NewReader("no\n"), &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Uninstall cancelled.") {
		t.Fatalf("missing cancellation: %s", out.String())
	}
}

func TestUninstallRejectsExplicitConfig(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"-c", "/tmp/custom.toml", "uninstall", "--yes"}, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d, want 2: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "does not accept --config") {
		t.Fatalf("missing safety explanation: %s", errOut.String())
	}
}
