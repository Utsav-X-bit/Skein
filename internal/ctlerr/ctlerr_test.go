package ctlerr

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestClassifyStatus(t *testing.T) {
	cases := map[int]Kind{
		http.StatusUnauthorized:        Unauthenticated,
		http.StatusForbidden:           Denied,
		http.StatusLocked:              Locked,
		http.StatusBadRequest:          Other,
		http.StatusConflict:            Other,
		http.StatusInternalServerError: Other,
		0:                              Other,
	}
	for code, want := range cases {
		if got := ClassifyStatus(code); got != want {
			t.Errorf("ClassifyStatus(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestThreeStatesHaveDistinctRemediations(t *testing.T) {
	seen := map[string]Kind{}
	for _, k := range []Kind{Unauthenticated, Denied, DaemonDown, Locked} {
		rem := Remediation(k)
		if rem == "" {
			t.Fatalf("Remediation(%v) is empty", k)
		}
		if prev, ok := seen[rem]; ok {
			t.Fatalf("remediation shared by %v and %v: %q", prev, k, rem)
		}
		seen[rem] = k
		// A denied call must not be presented as retryable-as-is.
		if k == Denied && !strings.Contains(rem, "retrying unchanged will fail") {
			t.Errorf("Denied remediation does not warn against unchanged retry: %q", rem)
		}
	}
}

func TestErrorFormattingAndKindOf(t *testing.T) {
	err := New(http.StatusForbidden, "secret role required")
	if KindOf(err) != Denied {
		t.Fatalf("KindOf = %v, want Denied", KindOf(err))
	}
	if !strings.Contains(err.Error(), "access denied") || !strings.Contains(err.Error(), "secret role required") {
		t.Fatalf("Error() = %q", err.Error())
	}
	if KindOf(errors.New("plain")) != Other {
		t.Fatal("plain error classified as non-Other")
	}
	if KindOf(NewDown(errors.New("connection refused"))) != DaemonDown {
		t.Fatal("NewDown not classified DaemonDown")
	}
}
