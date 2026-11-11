//go:build windows

package uninstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

const (
	aliceSID = "S-1-5-21-1111111111-2222222222-3333333333-1001"
	bobSID   = "S-1-5-21-1111111111-2222222222-3333333333-1002"
)

// recorded captures the privileged steps a test must not actually run.
type recorded struct {
	calls   []string
	pending []string
	script  string
	logPath string
}

// scratch points the whole removal at a temp tree. Every OS-owned input is a
// scratch path and every privileged step is recorded.
type scratch struct {
	e     env
	rec   *recorded
	users string
	alice string
	bob   string
}

func scratchEnv(t *testing.T) *scratch {
	t.Helper()
	root := t.TempDir()
	users := filepath.Join(root, "Users")
	s := &scratch{
		users: users,
		alice: filepath.Join(users, "alice"),
		bob:   filepath.Join(users, "bob"),
		rec:   &recorded{logPath: filepath.Join(root, "cleanup.log")},
	}
	s.e = env{
		systemRoot:   filepath.Join(root, "Windows"),
		programFiles: filepath.Join(root, "Program Files"),
		programData:  filepath.Join(root, "ProgramData"),
		systemDir:    filepath.Join(root, "ProgramData", "Skein"),
		allUsersMenu: filepath.Join(root, "ProgramData", "Microsoft", "Windows", "Start Menu", "Programs"),
		powershell:   filepath.Join(root, "powershell.exe"),

		profiles: func() ([]profile, []string, error) {
			return []profile{
				{sid: "S-1-5-18", path: filepath.Join(root, "Windows", "system32", "config", "systemprofile")},
				{sid: aliceSID, path: s.alice},
				{sid: bobSID, path: s.bob},
			}, nil, nil
		},
		stopService:    func() error { s.rec.calls = append(s.rec.calls, "service"); return nil },
		removeFirewall: func() error { s.rec.calls = append(s.rec.calls, "firewall"); return nil },
		deleteRegistry: func() error { s.rec.calls = append(s.rec.calls, "registry"); return nil },
		pendingDelete:  func(path string) error { s.rec.pending = append(s.rec.pending, path); return nil },
		trustInstall:   func(string) error { return nil },
		captureFile:    func() (string, error) { return s.rec.logPath, nil },
		startCleanup: func(script string) (int, error) {
			s.rec.script = script
			return 4242, os.WriteFile(s.rec.logPath, []byte("started\n"), 0o600)
		},
		desktopRunning: func() (bool, error) { return false, nil },
	}
	return s
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s still exists (err=%v)", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s was removed: %v", path, err)
	}
}

// roaming is a profile's roaming AppData directory.
func roaming(profile string) string {
	return filepath.Join(profile, "AppData", "Roaming")
}

// TestRunRemovesSkeinAndKeepsUnrelated is the whole-install behaviour for
// both installer layouts (NSIS adds shortcuts and the registry entry, the
// PowerShell script does not) and for every local profile.
func TestRunRemovesSkeinAndKeepsUnrelated(t *testing.T) {
	s := scratchEnv(t)
	installDir := filepath.Join(s.e.programFiles, "Skein")
	publicDesktop := filepath.Join(s.users, "Public", "Desktop")

	owned := []string{
		filepath.Join(s.e.systemDir, "skein.toml"),
		filepath.Join(s.e.systemDir, "identity.key"),
		filepath.Join(s.e.systemDir, "access"),
		filepath.Join(s.e.systemDir, "state.json"),
		filepath.Join(s.e.systemDir, "backups", "20260101T000000", "skein.toml"),
		filepath.Join(s.e.programData, "Skein.bak", "skein.toml"),
		filepath.Join(installDir, "skeind.exe"),
		filepath.Join(installDir, "skein.exe"),
		filepath.Join(installDir, "skein-helper.exe"),
		filepath.Join(installDir, "wintun.dll"),
		filepath.Join(installDir, "uninstall.exe"),
		filepath.Join(s.e.allUsersMenu, "Skein.lnk"),
		filepath.Join(publicDesktop, "Skein.lnk"),
		filepath.Join(s.alice, "Desktop", "Skein.lnk"),
		filepath.Join(roaming(s.alice), "Microsoft", "Windows", "Start Menu", "Programs", "Skein.lnk"),
		filepath.Join(s.bob, "Desktop", "Skein.lnk"),
	}
	for _, profile := range []string{s.alice, s.bob} {
		owned = append(owned,
			filepath.Join(roaming(profile), "skein", "skein.toml"),
			filepath.Join(roaming(profile), "skein", "identity.key"),
			filepath.Join(roaming(profile), "skein-desktop", "history", "history.jsonl"),
			filepath.Join(roaming(profile), "skein-desktop.exe", "EBWebView", "Local State"),
		)
	}
	for _, p := range owned {
		writeFile(t, p)
	}
	unrelated := []string{
		filepath.Join(s.e.programData, "Other", "keep.txt"),
		filepath.Join(s.e.programFiles, "Other", "keep.exe"),
		filepath.Join(roaming(s.alice), "Other", "keep.json"),
		filepath.Join(publicDesktop, "Other.lnk"),
		filepath.Join(s.alice, "Desktop", "Other.lnk"),
		filepath.Join(s.bob, "Desktop", "Other.lnk"),
	}
	for _, p := range unrelated {
		writeFile(t, p)
	}

	msg, err := run(context.Background(), s.e)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, msg)
	}
	for _, p := range owned {
		mustNotExist(t, p)
	}
	for _, p := range unrelated {
		mustExist(t, p)
	}
	if got := strings.Join(s.rec.calls, ","); got != "service,firewall,registry" {
		t.Errorf("privileged steps = %q, want service,firewall,registry", got)
	}
	if s.rec.script != "" {
		t.Errorf("cleanup worker started although nothing was locked")
	}
	if !strings.Contains(msg, "Skein was removed") {
		t.Errorf("message does not report success:\n%s", msg)
	}
	if !strings.Contains(msg, installDir) {
		t.Errorf("message does not name the install directory:\n%s", msg)
	}
	if !strings.Contains(msg, filepath.Join(roaming(s.bob), "skein")) {
		t.Errorf("message does not name the second profile's data:\n%s", msg)
	}
}

// TestRunSchedulesLockedExecutableForCleanup is the running-image case: the
// locked file is not reported as removed, it is registered for reboot and
// handed to a worker that waits for this process to exit.
func TestRunSchedulesLockedExecutableForCleanup(t *testing.T) {
	s := scratchEnv(t)
	installDir := filepath.Join(s.e.programFiles, "Skein")
	locked := filepath.Join(installDir, "skein.exe")
	writeFile(t, locked)
	writeFile(t, filepath.Join(installDir, "wintun.dll"))

	// Opening without FILE_SHARE_DELETE reproduces the running-image lock
	// deterministically.
	name, err := windows.UTF16PtrFromString(locked)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", locked, err)
	}
	defer windows.CloseHandle(h)

	msg, err := run(context.Background(), s.e)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, msg)
	}
	mustNotExist(t, installDir)
	if len(s.rec.pending) != 2 {
		t.Fatalf("expected the locked file and its directory to be pending, got %v", s.rec.pending)
	}
	quarantine := s.rec.pending[1]
	if filepath.Dir(quarantine) != s.e.programFiles || !strings.HasPrefix(filepath.Base(quarantine), "Skein.uninstall-") {
		t.Fatalf("unsafe pending directory: %s", quarantine)
	}
	if s.rec.pending[0] != filepath.Join(quarantine, "skein.exe") {
		t.Fatalf("unexpected pending file: %s", s.rec.pending[0])
	}
	mustExist(t, s.rec.pending[0])
	if !strings.Contains(msg, quarantine) || !strings.Contains(msg, s.rec.logPath) {
		t.Errorf("pending cleanup is not identified in the result:\n%s", msg)
	}
	// A reinstall must not share any path queued for reboot deletion.
	writeFile(t, locked)
	for _, p := range s.rec.pending {
		if strings.EqualFold(p, locked) || strings.EqualFold(p, installDir) {
			t.Errorf("reinstall path was queued for deletion: %s", p)
		}
	}
}

// TestRunFailsWhenCleanupCannotStart proves a lock is never reported as a
// silent success: without a worker the caller gets an error naming the
// reboot fallback.
func TestRunFailsWhenCleanupCannotStart(t *testing.T) {
	s := scratchEnv(t)
	locked := filepath.Join(s.e.programFiles, "Skein", "skein.exe")
	writeFile(t, locked)
	name, err := windows.UTF16PtrFromString(locked)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", locked, err)
	}
	defer windows.CloseHandle(h)

	s.e.startCleanup = func(string) (int, error) { return 0, errors.New("powershell is unavailable") }

	msg, err := run(context.Background(), s.e)
	if err == nil {
		t.Fatalf("expected an error, got message:\n%s", msg)
	}
	if !strings.Contains(err.Error(), "cleanup process could not start") || !strings.Contains(err.Error(), "next reboot") {
		t.Errorf("error does not explain recovery: %s", err)
	}
	if len(s.rec.pending) != 2 || !strings.Contains(err.Error(), s.rec.pending[1]) {
		t.Errorf("error does not identify the quarantined directory: %s", err)
	}
}

// TestRunRefusesWhileDesktopRuns proves the desktop check happens before any
// mutation, and fails closed.
func TestRunRefusesWhileDesktopRuns(t *testing.T) {
	s := scratchEnv(t)
	config := filepath.Join(s.e.systemDir, "skein.toml")
	writeFile(t, config)

	s.e.desktopRunning = func() (bool, error) { return true, nil }
	if _, err := run(context.Background(), s.e); err == nil || !strings.Contains(err.Error(), "Skein Desktop is running") {
		t.Fatalf("want a desktop-running refusal, got %v", err)
	}
	if len(s.rec.calls) != 0 {
		t.Errorf("privileged steps ran: %v", s.rec.calls)
	}
	mustExist(t, config)

	s.e.desktopRunning = func() (bool, error) { return false, errors.New("tasklist failed") }
	if _, err := run(context.Background(), s.e); err == nil || !strings.Contains(err.Error(), "nothing was removed") {
		t.Fatalf("want a fail-closed probe error, got %v", err)
	}
	mustExist(t, config)
}

// TestRunAbortsWhenServiceWillNotStop proves the uninstall never deletes
// files around a running daemon.
func TestRunAbortsWhenServiceWillNotStop(t *testing.T) {
	s := scratchEnv(t)
	config := filepath.Join(s.e.systemDir, "skein.toml")
	writeFile(t, config)
	s.e.stopService = func() error { return errors.New("service did not stop") }

	if _, err := run(context.Background(), s.e); err == nil || !strings.Contains(err.Error(), "nothing was removed") {
		t.Fatalf("want a service refusal, got %v", err)
	}
	if len(s.rec.calls) != 0 {
		t.Errorf("later steps ran: %v", s.rec.calls)
	}
	mustExist(t, config)
}

// TestRunIsIdempotentOnACleanMachine covers an install that is already gone.
func TestRunIsIdempotentOnACleanMachine(t *testing.T) {
	s := scratchEnv(t)
	msg, err := run(context.Background(), s.e)
	if err != nil {
		t.Fatalf("run on a clean machine: %v\n%s", err, msg)
	}
	if !strings.Contains(msg, "Removed (or already absent)") {
		t.Errorf("message does not report the absent items:\n%s", msg)
	}
}

// TestRunFailsWhenProfilesCannotBeRead proves an unreadable ProfileList is
// reported instead of silently skipping every user's data.
func TestRunFailsWhenProfilesCannotBeRead(t *testing.T) {
	s := scratchEnv(t)
	s.e.profiles = func() ([]profile, []string, error) {
		return nil, nil, errors.New("access denied")
	}
	_, err := run(context.Background(), s.e)
	if err == nil || !strings.Contains(err.Error(), "enumerate local profiles") {
		t.Fatalf("want a profile enumeration failure, got %v", err)
	}
}

func TestRunPreservesDataWhenPreflightFails(t *testing.T) {
	s := scratchEnv(t)
	config := filepath.Join(s.e.systemDir, "skein.toml")
	writeFile(t, config)
	s.e.profiles = func() ([]profile, []string, error) {
		return nil, []string{"inaccessible profile"}, nil
	}
	if _, err := run(context.Background(), s.e); err == nil {
		t.Fatal("expected an incomplete profile enumeration to stop uninstall")
	}
	mustExist(t, config)
	if len(s.rec.calls) != 0 {
		t.Fatalf("preflight failure changed system state: %v", s.rec.calls)
	}
}

func TestExpandProfilePathDoesNotTrustEnvironment(t *testing.T) {
	t.Setenv("SystemDrive", `Z:`)
	got, err := expandProfilePath(`%SystemDrive%\Users\alice`, `C:\Windows`)
	if err != nil || got != `C:\Users\alice` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := expandProfilePath(`%USERPROFILE%\alice`, `C:\Windows`); err == nil {
		t.Fatal("accepted a caller-controlled variable")
	}
}

func TestOwnedChildRejectsUnsafePaths(t *testing.T) {
	const root = `C:\ProgramData`
	for _, path := range []string{
		`Skein`,
		`C:\ProgramData`,
		`C:\ProgramData\Skein\..\..\Windows`,
		`C:\Windows\Skein`,
		`\\server\share\Skein`,
	} {
		if err := ownedChild(path, root); err == nil {
			t.Errorf("ownedChild(%q, %q) accepted an unsafe path", path, root)
		}
	}
	if err := ownedChild(`C:\ProgramData\Skein`, root); err != nil {
		t.Errorf("ownedChild rejected the real directory: %v", err)
	}
	if err := ownedChild(`C:\Skein`, `C:\`); err == nil {
		t.Error("ownedChild accepted a drive root as the parent")
	}
}

func TestRemoveTreeRefusesReparsePoint(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	keep := filepath.Join(target, "keep.txt")
	writeFile(t, keep)
	link := filepath.Join(root, "Skein")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a directory symlink: %v", err)
	}
	defer os.Remove(link)

	existed, err := removeTree(link, root)
	if err == nil {
		t.Fatalf("expected a reparse-point refusal, got existed=%v", existed)
	}
	mustExist(t, keep)
}

// TestUserDataTargetsCoversEveryProfile is the multi-profile requirement:
// every real profile's roaming AppData is targeted, service accounts and
// untrustworthy paths are not.
func TestUserDataTargetsCoversEveryProfile(t *testing.T) {
	root := t.TempDir()
	systemRoot := filepath.Join(root, "Windows")
	alice := filepath.Join(root, "Users", "alice")
	bob := filepath.Join(root, "Users", "bob")
	for _, p := range []string{alice, bob} {
		if err := os.MkdirAll(roaming(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	dirs, skipped := userDataTargets([]profile{
		{sid: "S-1-5-18", path: filepath.Join(systemRoot, "system32", "config", "systemprofile")},
		{sid: aliceSID, path: alice},
		{sid: bobSID, path: bob},
	}, systemRoot)
	if len(skipped) != 0 {
		t.Errorf("unexpected skips: %v", skipped)
	}
	want := map[string]bool{
		filepath.Join(roaming(alice), "skein"):             false,
		filepath.Join(roaming(alice), "skein-desktop"):     false,
		filepath.Join(roaming(alice), "skein-desktop.exe"): false,
		filepath.Join(roaming(bob), "skein"):               false,
		filepath.Join(roaming(bob), "skein-desktop"):       false,
		filepath.Join(roaming(bob), "skein-desktop.exe"):   false,
	}
	if len(dirs) != len(want) {
		t.Fatalf("targets = %v, want %d entries", dirs, len(want))
	}
	for _, d := range dirs {
		if !strings.EqualFold(d.root, roaming(filepath.Dir(filepath.Dir(filepath.Dir(d.path))))) {
			t.Errorf("%s has an unexpected root %s", d.path, d.root)
		}
		seen, ok := want[d.path]
		if !ok {
			t.Errorf("unexpected target %s", d.path)
			continue
		}
		if seen {
			t.Errorf("duplicate target %s", d.path)
		}
		want[d.path] = true
	}
	for path, seen := range want {
		if !seen {
			t.Errorf("missing target %s", path)
		}
	}

	// Service accounts, missing profiles, relative paths, and profiles under
	// the Windows directory are never targeted.
	dirs, _ = userDataTargets([]profile{
		{sid: "S-1-5-19", path: filepath.Join(root, "Windows", "ServiceProfiles", "LocalService")},
		{sid: aliceSID, path: filepath.Join(root, "Users", "ghost")},
	}, systemRoot)
	if len(dirs) != 0 {
		t.Errorf("targets for service account/missing profile: %v", dirs)
	}
	dirs, skipped = userDataTargets([]profile{
		{sid: aliceSID, path: `Users\alice`},
		{sid: bobSID, path: filepath.Join(systemRoot, "system32", "config", "systemprofile")},
	}, systemRoot)
	if len(dirs) != 0 || len(skipped) != 2 {
		t.Errorf("dirs=%v skipped=%v, want no targets and two skips", dirs, skipped)
	}
}

func TestRoamingAppDataRefusesReparsePoint(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-profile")
	if err := os.MkdirAll(roaming(real), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "linked-profile")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a directory symlink: %v", err)
	}
	if _, err := roamingAppData(link, filepath.Join(root, "Windows")); err == nil {
		t.Fatal("roamingAppData followed a reparse point")
	}
}

// TestShortcutTargetsCoverEveryProfile proves per-user shortcuts are found
// through ProfileList entries for every profile, never through %APPDATA%.
func TestShortcutTargetsCoverEveryProfile(t *testing.T) {
	root := t.TempDir()
	users := filepath.Join(root, "Users")
	alice := filepath.Join(users, "alice")
	bob := filepath.Join(users, "bob")
	allUsersMenu := filepath.Join(root, "ProgramData", "Microsoft", "Windows", "Start Menu", "Programs")
	for _, dir := range []string{
		allUsersMenu,
		filepath.Join(users, "Public", "Desktop"),
		filepath.Join(alice, "Desktop"),
		filepath.Join(roaming(alice), "Microsoft", "Windows", "Start Menu", "Programs"),
		filepath.Join(bob, "Desktop"),
		filepath.Join(roaming(bob), "Microsoft", "Windows", "Start Menu", "Programs"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	profiles := []profile{{sid: aliceSID, path: alice}, {sid: bobSID, path: bob}}

	targets, skipped := shortcutTargets(profiles, allUsersMenu)
	if len(skipped) != 0 {
		t.Errorf("unexpected skips: %v", skipped)
	}
	if len(targets) != 6 {
		t.Fatalf("targets = %v, want 6", targets)
	}
	for _, want := range []string{
		filepath.Join(allUsersMenu, "Skein.lnk"),
		filepath.Join(users, "Public", "Desktop", "Skein.lnk"),
		filepath.Join(alice, "Desktop", "Skein.lnk"),
		filepath.Join(roaming(alice), "Microsoft", "Windows", "Start Menu", "Programs", "Skein.lnk"),
		filepath.Join(bob, "Desktop", "Skein.lnk"),
		filepath.Join(roaming(bob), "Microsoft", "Windows", "Start Menu", "Programs", "Skein.lnk"),
	} {
		found := false
		for _, target := range targets {
			if strings.EqualFold(target.path, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing shortcut target %s", want)
		}
	}

	// A service account and a profile that does not exist add nothing.
	targets, _ = shortcutTargets([]profile{
		{sid: "S-1-5-18", path: filepath.Join(root, "Windows", "system32", "config", "systemprofile")},
		{sid: bobSID, path: filepath.Join(users, "ghost")},
	}, allUsersMenu)
	if len(targets) != 1 || !strings.EqualFold(targets[0].path, filepath.Join(allUsersMenu, "Skein.lnk")) {
		t.Errorf("unexpected targets %v, want only the all-users link", targets)
	}
}

func TestCleanupScriptEscapesPaths(t *testing.T) {
	dir := `C:\Users\o'brien\AppData\Local\Temp\skein`
	log := `C:\Users\o'brien\AppData\Local\Temp\o'brien.log`
	script := cleanupScript([]string{dir}, log, 99)

	if !strings.Contains(script, `'C:\Users\o''brien\AppData\Local\Temp\skein'`) {
		t.Errorf("install path is not single-quote escaped:\n%s", script)
	}
	if !strings.Contains(script, `'C:\Users\o''brien\AppData\Local\Temp\o''brien.log'`) {
		t.Errorf("log path is not single-quote escaped:\n%s", script)
	}
	if !strings.Contains(script, "$target = 99") {
		t.Errorf("script does not wait for the uninstalling process:\n%s", script)
	}
	if !strings.Contains(script, "Note 'started'") {
		t.Errorf("script does not record that it started:\n%s", script)
	}
}
