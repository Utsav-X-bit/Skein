package api

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Role is the IPC authorisation level of a caller. Roles are ordered:
// None < Read < Mutate < Secret. Bootstrap is reserved for the future
// bootstrap endpoints (M5 Step 4); it is never granted from the access
// file and never held by a bearer or ordinary kernel caller.
//
//   - Read: status, peers, routes, activities, config view (redacted),
//     diagnostics, logs, events, security, firewall view, capabilities,
//     identity.
//   - Mutate: everything in Read, plus peer add/remove, name, config
//     patch, connection control, probe, peer invites, firewall repair.
//   - Secret: everything in Mutate, plus network-invite generation, which
//     discloses the live network secret. Secret requires the kernel
//     transport (Unix socket / Windows pipe); a bearer token never
//     exceeds Read, even if it is valid.
type Role int

const (
	RoleNone Role = iota
	RoleRead
	RoleMutate
	RoleSecret
	RoleBootstrap
)

func (r Role) String() string {
	switch r {
	case RoleRead:
		return "read"
	case RoleMutate:
		return "mutate"
	case RoleSecret:
		return "secret"
	case RoleBootstrap:
		return "bootstrap"
	default:
		return "none"
	}
}

// parseAccessRole parses a role name as it appears in the access file.
// Bootstrap is deliberately rejected here: bootstrap authorisation comes
// from a single-use token issued by the minimal helper (Step 4), never
// from a standing access-file line.
func parseAccessRole(s string) (Role, error) {
	switch s {
	case "read":
		return RoleRead, nil
	case "mutate":
		return RoleMutate, nil
	case "secret":
		return RoleSecret, nil
	default:
		return RoleNone, fmt.Errorf("unknown role %q (want read, mutate, or secret)", s)
	}
}

// ParseAccessGrants parses the access file format:
//
//	uid:role      (Linux, e.g. "1001:mutate")
//	SID:role      (Windows, e.g. "S-1-5-21-1-2-3-1000:secret")
//	uid           (legacy bare line -> RoleRead, see below)
//	SID           (legacy bare line -> RoleRead)
//	# comment, blank lines ignored
//
// Deviation from ADR-001-implementation-plan §2.1, recorded in the Step 1
// report: the plan says bare uid -> mutate, but the Step 1 task requires
// the least privilege that keeps status reads working for existing
// grants, and mutate for every legacy line would silently hand every
// previously-granted user peer/config/firewall control. Bare lines
// therefore map to Read. New grants must be written with an explicit
// role (helper grant-user --role, Step 10 wires the verb; the daemon
// bootstrap grant in Step 4 writes secret explicitly).
func ParseAccessGrants(r io.Reader) (map[string]Role, error) {
	grants := map[string]Role{}
	sc := bufio.NewScanner(r)
	// The access file is small; a long line is a malformed file, not a grant.
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, role, err := parseAccessLine(line)
		if err != nil {
			return nil, err
		}
		grants[id] = role // last line wins, matching grant-update semantics
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return grants, nil
}

func parseAccessLine(line string) (string, Role, error) {
	if i := strings.LastIndex(line, ":"); i >= 0 {
		idPart, rolePart := line[:i], line[i+1:]
		role, err := parseAccessRole(rolePart)
		if err != nil {
			return "", RoleNone, fmt.Errorf("access line %q: %w", line, err)
		}
		id, err := normaliseAccessIdentity(idPart)
		if err != nil {
			return "", RoleNone, fmt.Errorf("access line %q: %w", line, err)
		}
		return id, role, nil
	}
	id, err := normaliseAccessIdentity(line)
	if err != nil {
		return "", RoleNone, fmt.Errorf("access line %q: %w", line, err)
	}
	return id, RoleRead, nil
}

func normaliseAccessIdentity(s string) (string, error) {
	if s == "" {
		return "", errors.New("empty identity")
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "uid:" + s, nil
	}
	if strings.HasPrefix(s, "S-") {
		return s, nil
	}
	return "", fmt.Errorf("invalid identity %q (want a uid or a SID)", s)
}

// SetGrants installs the authorisation table. A nil map means "not
// configured" (see roleFor); an empty non-nil map means "configured with
// no grants" (every non-privileged kernel caller is denied).
func (s *Server) SetGrants(grants map[string]Role) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]Role, len(grants))
	for k, v := range grants {
		cp[k] = v
	}
	s.grants = cp
}

// SetGrant merges one caller/role pair into the authorisation table
// (Step 4: the post-transition daemon applies the bootstrap grant to
// its live server directly, in addition to the persisted access file
// the daemon loaded at construction). A nil table becomes a table
// containing only this grant; use SetGrants for a full replacement.
func (s *Server) SetGrant(id string, role Role) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grants == nil {
		s.grants = map[string]Role{}
	}
	s.grants[id] = role
}

// LoadGrantsFromFile loads the access file at path into the authorisation
// table. A missing file is not an error: it leaves the table
// unconfigured, so kernel callers fall back to read-only (roleFor).
// A present-but-malformed file is an error, so the daemon fails closed
// rather than running with a silently ignored grant file.
func (s *Server) LoadGrantsFromFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	grants, err := ParseAccessGrants(f)
	if err != nil {
		return err
	}
	s.SetGrants(grants)
	return nil
}

// roleFor resolves a caller's role. Transport is enforced here, not in
// each handler: a bearer caller never exceeds Read, and an unknown caller
// (no kernel identity, no valid token) gets None. Unix root (uid 0, no
// SID) and the Windows SYSTEM / Administrators SIDs already own the
// system (they can read the config file directly), so denying them
// the API would be theatre; they are implicitly Secret. Every other
// kernel caller needs an explicit grant when a table is configured;
// with no table configured, a kernel caller gets Read only, so status
// reads keep working but nothing mutates and no secret is disclosed.
//
// M9 §2c: implicit secret is a kernel-transport concept only, and
// uid 0 means Unix root only. Windows pipe callers carry UID -1
// (Windows has no uid; see verifyClient) — but verifyClient used
// to leave UID at Go's zero value 0, so the bare `c.UID == 0`
// check fired for every Windows pipe caller and the access-file
// table was decorative. The uid branch therefore also requires an
// empty SID, so a SID-carrying Windows caller never trips it; the
// block also requires the kernel transport. (Unverified pipe
// clients, which would otherwise arrive as SID-less callers, are
// dropped by the pipe accept loop before they reach roleFor.)
func (s *Server) roleFor(c Caller) Role {
	if c.Transport == TransportBearer {
		return RoleRead
	}
	id := c.ID()
	if id == "unknown" {
		return RoleNone
	}
	if c.Transport == TransportKernel && ((c.UID == 0 && c.SID == "") || id == "S-1-5-18" || id == "S-1-5-32-544") {
		return RoleSecret
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.grants == nil {
		return RoleRead
	}
	role, ok := s.grants[id]
	if !ok {
		return RoleNone
	}
	return role
}
