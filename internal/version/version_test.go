package version

import (
	"strings"
	"testing"
)

func TestInfoIdentifiesTheBuild(t *testing.T) {
	info := Info()
	if info == "" {
		t.Fatal("empty version")
	}
	if !strings.HasPrefix(info, Version) {
		t.Fatalf("Info = %q, want the %q prefix", info, Version)
	}
	if strings.Contains(info, "  ") {
		t.Fatalf("Info has empty fields: %q", info)
	}
}
