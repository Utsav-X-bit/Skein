package api

import (
	"strings"
	"testing"
)

// M9 §2c: Windows pipe callers carry UID -1 (Windows has no uid).
// Before the fix, verifyClient left UID at Go's zero value 0, so
// roleFor's uid-0 implicit-secret branch fired for every Windows
// pipe caller and the access-file table was decorative: a
// Read-granted user landed Secret. These tests pin the table as
// authoritative for Windows callers and Unix root as the only
// uid-0 secret. (A zero-value Caller is structurally identical to
// Unix root — TransportKernel is the zero value — so it is not
// asserted here: the Windows pipe accept loop drops SID-less
// connections before they ever reach roleFor, and Unix socket
// callers always carry a real uid.)
func TestRoleForImplicitSecretNeedsUnixRoot(t *testing.T) {
	const winSID = "S-1-5-21-1554901594-3398404858-1333051744-1001"
	build := func(grants map[string]Role) *Server {
		s, _, _, _ := newTestServer(t)
		if grants != nil {
			s.SetGrants(grants)
		}
		return s
	}
	table := map[string]Role{winSID: RoleRead}
	cases := []struct {
		name   string
		caller Caller
		grants map[string]Role
		want   Role
	}{
		{"windows pipe caller honours table read", Caller{SID: winSID, UID: -1, Transport: TransportKernel}, table, RoleRead},
		{"windows pipe caller honours table secret", Caller{SID: winSID, UID: -1, Transport: TransportKernel}, map[string]Role{winSID: RoleSecret}, RoleSecret},
		{"pre-fix zero-uid windows caller honours table", Caller{SID: winSID, UID: 0, Transport: TransportKernel}, table, RoleRead},
		{"windows pipe caller without grant is denied", Caller{SID: "S-1-5-21-1-2-3-1002", UID: -1, Transport: TransportKernel}, table, RoleNone},
		{"unix root stays secret", Caller{UID: 0, Transport: TransportKernel}, table, RoleSecret},
		{"windows SYSTEM stays secret", Caller{SID: "S-1-5-18", UID: -1, Transport: TransportKernel}, table, RoleSecret},
		{"windows Administrators stays secret", Caller{SID: "S-1-5-32-544", UID: -1, Transport: TransportKernel}, table, RoleSecret},
		{"bearer caller capped at read", Caller{UID: -1, Transport: TransportBearer}, map[string]Role{winSID: RoleSecret}, RoleRead},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := build(tc.grants).roleFor(tc.caller); got != tc.want {
				t.Fatalf("roleFor(%+v) = %v, want %v", tc.caller, got, tc.want)
			}
		})
	}
}

// A bare access-file line (no :role) parses to Read. This is the
// premise of the set-owner demotion bug (M9 §2c): writing a bare SID
// for the new owner silently drops them to Read on the next reload.
func TestParseAccessGrantsBareLineIsRead(t *testing.T) {
	grants, err := ParseAccessGrants(strings.NewReader("S-1-5-21-1554901594-3398404858-1333051744-1001\nS-1-5-21-1554901594-3398404858-1333051744-1002:secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	if grants["S-1-5-21-1554901594-3398404858-1333051744-1001"] != RoleRead {
		t.Fatalf("bare line = %v, want Read", grants["S-1-5-21-1554901594-3398404858-1333051744-1001"])
	}
	if grants["S-1-5-21-1554901594-3398404858-1333051744-1002"] != RoleSecret {
		t.Fatalf("secret line = %v, want Secret", grants["S-1-5-21-1554901594-3398404858-1333051744-1002"])
	}
}
