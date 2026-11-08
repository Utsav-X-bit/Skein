package service

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// fakeBackend records registrations in memory so RegisterDaemon /
// RollbackDaemon can be tested without systemctl/sc or a real
// system service (Step 6 tests must never touch /etc/skein or the
// real service, as Step 4 deliberately did).
type fakeBackend struct {
	exe, cfg string
	has      bool
	installs []string
	rewrites []string
	stops    int
	failRW   error // returned by install/rewrite, WITHOUT changing state
	failStop error
}

func (f *fakeBackend) installed() (string, string, bool, error) { return f.exe, f.cfg, f.has, nil }
func (f *fakeBackend) install(exe, cfg string) error {
	if f.failRW != nil {
		return f.failRW
	}
	f.exe, f.cfg, f.has = exe, cfg, true
	f.installs = append(f.installs, exe)
	return nil
}
func (f *fakeBackend) rewrite(exe, cfg string) error {
	if f.failRW != nil {
		return f.failRW
	}
	f.exe, f.cfg, f.has = exe, cfg, true
	f.rewrites = append(f.rewrites, exe)
	return nil
}
func (f *fakeBackend) stop() error {
	f.stops++
	return f.failStop
}
func (f *fakeBackend) start() error { return nil }

func useFake(t *testing.T, f *fakeBackend) {
	t.Helper()
	oldBackend, oldDirs := svcBackend, packageDirs
	svcBackend = f
	t.Cleanup(func() { svcBackend = oldBackend; packageDirs = oldDirs })
}

// mkBin creates a fresh subdirectory of parent (mode dirMode)
// holding a regular file named name with content, and returns
// its path. Each call gets its own directory, so two skeind
// binaries in one test root never collide.
func mkBin(t *testing.T, parent, name, content string, dirMode os.FileMode) string {
	t.Helper()
	dir, err := os.MkdirTemp(parent, "bindir-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		t.Fatal(err)
	}
	// Translate the Unix mode intent to Windows: a restricted parent
	// gets an admin-only DACL (so the new parent-ACL check accepts
	// it), while a deliberately group/other-writable parent keeps
	// its inherited user-write DACL (so the check rejects it).
	if runtime.GOOS == "windows" && dirMode&0o022 == 0 {
		if err := fileperm.Root(dir); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidateDaemonPathMatrix(t *testing.T) {
	root := t.TempDir()
	valid := mkBin(t, root, daemonBaseName(), "v1", 0o755)
	if err := ValidateDaemonPath(valid); err != nil {
		t.Fatalf("valid skeind rejected: %v", err)
	}
	// On Windows the parent-ACL check reports a non-admin parent in
	// its own words, not the Unix mode-check wording, and a missing
	// file reports the Windows "cannot find the path" text.
	writableWant := "writable by group/other"
	missingWant := "no such file"
	if runtime.GOOS == "windows" {
		writableWant = "is not admin-only"
		missingWant = "cannot find the path"
	}
	cases := []struct {
		name string
		path string
		want string // substring of the error
	}{
		{"relative", "skeind", "not absolute"},
		{"missing", filepath.Join(root, "nope", "skeind"), missingWant},
		{"wrong basename skein", mkBin(t, root, "skein", "v1", 0o755), "basename must be skeind"},
		{"wrong basename helper", mkBin(t, root, "skein-helper", "v1", 0o755), "basename must be skeind"},
		{"world-writable parent", mkBin(t, root, daemonBaseName(), "v1", 0o777), writableWant},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateDaemonPath(c.path); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ValidateDaemonPath(%q) = %v, want error containing %q", c.path, err, c.want)
			}
		})
	}
	// Symlink, even to a valid skeind, is rejected.
	t.Run("symlink", func(t *testing.T) {
		dir := filepath.Join(root, "link-dir")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "skeind")
		if err := os.Symlink(valid, link); err != nil {
			t.Fatal(err)
		}
		if err := ValidateDaemonPath(link); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("ValidateDaemonPath(symlink) = %v, want symlink rejection", err)
		}
	})
}

func TestResolveDaemonPathSearchExcludesHelperDirAndPATH(t *testing.T) {
	f := &fakeBackend{}
	useFake(t, f)
	root := t.TempDir()
	pkg := mkBin(t, root, daemonBaseName(), "package", 0o755)
	helper := mkBin(t, root, daemonBaseName(), "PLANTED", 0o755)
	// The planted binary lives in a "helper" directory that is
	// never in packageDirs, and PATH points at it too.
	helperDir := filepath.Dir(helper)
	t.Setenv("PATH", helperDir)
	packageDirs = []string{filepath.Join(root, "missing", "skeind"), pkg}
	got, err := ResolveDaemonPath("")
	if err != nil {
		t.Fatalf("ResolveDaemonPath = %v", err)
	}
	if got != pkg {
		t.Fatalf("ResolveDaemonPath = %q, want package %q (helper-dir/PATH plant must never win)", got, pkg)
	}
	// Explicit world-writable candidate is rejected even when named.
	bad := mkBin(t, root, daemonBaseName(), "planted", 0o777)
	if _, err := ResolveDaemonPath(bad); err == nil {
		t.Fatalf("explicit world-writable candidate accepted")
	}
	// No valid candidate: the error must name the package search,
	// not PATH or the helper directory.
	packageDirs = []string{filepath.Join(root, "missing", "skeind")}
	if _, err := ResolveDaemonPath(""); err == nil || !strings.Contains(err.Error(), "package directories") {
		t.Fatalf("empty search error = %v, want package-directories error", err)
	}
}

func TestRegisterDaemonInstallUpgradeAndRollback(t *testing.T) {
	f := &fakeBackend{}
	useFake(t, f)
	root := t.TempDir()
	binA := mkBin(t, root, daemonBaseName(), "binary-A", 0o755)
	binB := mkBin(t, root, daemonBaseName(), "binary-B", 0o755)
	cfgPath := filepath.Join(root, "skein.toml")
	if err := os.WriteFile(cfgPath, []byte("config-v1"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Fresh install: no previous, no backups, fake records install.
	reg0, err := RegisterDaemon(RegisterOptions{ConfigPath: cfgPath, DaemonPath: binA})
	if err != nil {
		t.Fatalf("fresh install: %v", err)
	}
	if reg0.HadPrevious || reg0.BinaryBackupPath != "" || reg0.ConfigBackupPath != "" {
		t.Fatalf("fresh install recorded backups: %+v", reg0)
	}
	if f.exe != binA || !f.has {
		t.Fatalf("fake after install = %q, want %q", f.exe, binA)
	}

	// Upgrade: new binary adopted, old kept as .old, config backed up.
	reg, err := UpgradeDaemon(binB, cfgPath)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if f.exe != binB {
		t.Fatalf("fake after upgrade = %q, want new binary %q (already-installed exe must never be re-registered)", f.exe, binB)
	}
	if reg.PreviousDaemonPath != binA || reg.BinaryBackupPath != binA+".old" {
		t.Fatalf("upgrade registration = %+v, want previous %q with .old backup", reg, binA)
	}
	if got, _ := os.ReadFile(reg.BinaryBackupPath); string(got) != "binary-A" {
		t.Fatalf(".old backup = %q, want binary-A bytes", got)
	}
	if reg.ConfigBackupPath == "" {
		t.Fatal("upgrade recorded no config backup")
	}
	if got, _ := os.ReadFile(reg.ConfigBackupPath); string(got) != "config-v1" {
		t.Fatalf("config backup = %q, want config-v1", got)
	}

	// Simulate a later config change, then roll back: registration,
	// config bytes, and backups must all come back / stay put.
	if err := os.WriteFile(cfgPath, []byte("config-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RollbackDaemon(reg, cfgPath); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if f.exe != binA {
		t.Fatalf("fake after rollback = %q, want %q", f.exe, binA)
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != "config-v1" {
		t.Fatalf("config after rollback = %q, want config-v1", got)
	}
	if _, err := os.Stat(reg.BinaryBackupPath); err != nil {
		t.Fatalf(".old backup deleted by rollback: %v", err)
	}
	if _, err := os.Stat(reg.ConfigBackupPath); err != nil {
		t.Fatalf("config backup deleted by rollback: %v", err)
	}
}

func TestRegisterDaemonFailedUpgradeKeepsOldRegistrationAndBackups(t *testing.T) {
	f := &fakeBackend{}
	useFake(t, f)
	root := t.TempDir()
	binA := mkBin(t, root, daemonBaseName(), "binary-A", 0o755)
	binB := mkBin(t, root, daemonBaseName(), "binary-B", 0o755)
	cfgPath := filepath.Join(root, "skein.toml")
	if err := os.WriteFile(cfgPath, []byte("config-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterDaemon(RegisterOptions{ConfigPath: cfgPath, DaemonPath: binA}); err != nil {
		t.Fatal(err)
	}
	f.failRW = errors.New("simulated sc/systemctl failure")
	_, err := UpgradeDaemon(binB, cfgPath)
	if err == nil {
		t.Fatal("failed upgrade returned no error")
	}
	if f.exe != binA {
		t.Fatalf("fake after failed upgrade = %q, want old binary %q (service must not be bricked)", f.exe, binA)
	}
	// Backups were taken before the rewrite attempt and must survive it.
	if got, err := os.ReadFile(binA + ".old"); err != nil || string(got) != "binary-A" {
		t.Fatalf(".old after failed upgrade: %q, %v", got, err)
	}
	// Rollback with no recorded previous registration is refused,
	// and must not change anything.
	f.failRW = nil
	if err := RollbackDaemon(Registration{}, cfgPath); err == nil {
		t.Fatal("rollback without previous succeeded")
	}
	if f.exe != binA {
		t.Fatalf("fake after refused rollback = %q, want %q", f.exe, binA)
	}
}

func TestRegisterDaemonRefusesMissingPreviousBinary(t *testing.T) {
	f := &fakeBackend{}
	useFake(t, f)
	root := t.TempDir()
	binA := mkBin(t, root, daemonBaseName(), "binary-A", 0o755)
	binB := mkBin(t, root, daemonBaseName(), "binary-B", 0o755)
	cfgPath := filepath.Join(root, "skein.toml")
	if err := os.WriteFile(cfgPath, []byte("config-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterDaemon(RegisterOptions{ConfigPath: cfgPath, DaemonPath: binA}); err != nil {
		t.Fatal(err)
	}
	// The old binary vanishes (package removed it): upgrading
	// anyway would destroy the only rollback target.
	if err := os.Remove(binA); err != nil {
		t.Fatal(err)
	}
	before := len(f.rewrites)
	if _, err := UpgradeDaemon(binB, cfgPath); err == nil {
		t.Fatal("upgrade over a missing previous binary succeeded")
	}
	if len(f.rewrites) != before {
		t.Fatal("upgrade attempted a rewrite despite the missing rollback target")
	}
}

func TestRollbackDaemonRestoresBinaryFromOldCopy(t *testing.T) {
	f := &fakeBackend{}
	useFake(t, f)
	root := t.TempDir()
	binA := mkBin(t, root, daemonBaseName(), "binary-A", 0o755)
	binB := mkBin(t, root, daemonBaseName(), "binary-B", 0o755)
	cfgPath := filepath.Join(root, "skein.toml")
	if err := os.WriteFile(cfgPath, []byte("config-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterDaemon(RegisterOptions{ConfigPath: cfgPath, DaemonPath: binA}); err != nil {
		t.Fatal(err)
	}
	reg, err := UpgradeDaemon(binB, cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Old binary deleted after upgrade: rollback must resurrect
	// it from the .old copy, not fail or register the new binary.
	if err := os.Remove(binA); err != nil {
		t.Fatal(err)
	}
	if err := RollbackDaemon(reg, cfgPath); err != nil {
		t.Fatalf("rollback from .old: %v", err)
	}
	if got, _ := os.ReadFile(binA); string(got) != "binary-A" {
		t.Fatalf("restored binary = %q, want binary-A", got)
	}
	if f.exe != binA {
		t.Fatalf("fake after rollback = %q, want %q", f.exe, binA)
	}
}

func TestRegisterDaemonHelperDirPlantNeverRegistered(t *testing.T) {
	f := &fakeBackend{}
	useFake(t, f)
	root := t.TempDir()
	pkg := mkBin(t, root, daemonBaseName(), "package", 0o755)
	planted := mkBin(t, root, daemonBaseName(), "PLANTED", 0o755)
	packageDirs = []string{pkg}
	cfgPath := filepath.Join(root, "skein.toml")
	if err := os.WriteFile(cfgPath, []byte("config-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No explicit path: the package binary wins. The planted
	// binary's directory is not a search input at all.
	reg, err := RegisterDaemon(RegisterOptions{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("package install: %v", err)
	}
	if reg.DaemonPath != pkg || f.exe != pkg {
		t.Fatalf("registered %q, want package %q; planted %q must never become the service", reg.DaemonPath, pkg, planted)
	}
	for _, got := range f.installs {
		if got == planted {
			t.Fatalf("planted binary %q was registered", planted)
		}
	}
}

// TestCopyHelpersKeepUnixMode covers the two byte-copy helpers the
// upgrade/rollback path uses. On Unix a copy must keep the source's mode
// bits: a restored skeind has to stay executable and a config has to stay
// 0600. The Windows DACL half is in register_dacl_windows_test.go, where
// the fixture can name the current user's SID.
func TestCopyHelpersKeepUnixMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are Unix; the DACL half is register_dacl_windows_test.go")
	}
	src := filepath.Join(t.TempDir(), daemonBaseName())
	if err := os.WriteFile(src, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	dstDir := t.TempDir()
	for _, tc := range []struct {
		name string
		copy func(src, dst string) error
	}{
		{"copyFile", copyFile},
		{"copyFileAtomic", copyFileAtomic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(dstDir, tc.name)
			if err := tc.copy(src, dst); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			raw, err := os.ReadFile(dst)
			if err != nil {
				t.Fatalf("%s destination unreadable: %v", tc.name, err)
			}
			if string(raw) != "binary" {
				t.Fatalf("%s destination = %q", tc.name, raw)
			}
			info, err := os.Stat(dst)
			if err != nil {
				t.Fatal(err)
			}
			// The exec bit is the load-bearing part of "same mode
			// bits as src": a chmod-to-0600 copy is not runnable.
			if info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("%s mode = %o, want the source's exec bits kept", tc.name, info.Mode().Perm())
			}
		})
	}
}
