// Step 6 (ADR-001 §6): the single validated service-identity path.
//
// The service always registers `skeind` (the service binary), resolved by
// an explicit, validated search — never by PATH, never by the helper's
// directory, and never by "whatever exe is already registered". Those
// three sources are exactly Agent E's F-06 planted-binary hole:
// daemonExecutable()'s old "helper's directory first, then PATH" order
// is deleted, not kept behind a flag.
//
// Search order (RegisterDaemon):
//  1. An explicit path supplied by the installer/package (helper
//     `install --daemon <path>`, or the Step 7 daemon update path
//     calling UpgradeDaemon with the staged binary). Validated as below.
//  2. Package directories only (packageDirs). The first candidate that
//     passes validation wins; a candidate that exists but fails
//     validation is skipped, never trusted.
//
// The helper's directory is never consulted, os.Executable's directory
// is never consulted, and PATH is never consulted. A caller that wants
// the invoking binary registered must pass it explicitly (and it must
// still pass validation) — there is no implicit "current exe" path.
//
// Validation checklist (ValidateDaemonPath, in plan §6.1 order):
//  1. Absolute path.
//  2. Regular file, not a symlink (Lstat).
//  3. Parent directory not world-writable (Unix: dir mode & 0o022 == 0;
//     Windows: fail-closed conservative rule — the parent must be
//     under %ProgramFiles% (or %ProgramFiles(x86)%), which are
//     admin-only by default. A real parent-ACL check still needs
//     verification on a Windows VM (plan §6.1); until then an
//     explicit path outside ProgramFiles is rejected rather than
//     trusted on an unchecked heuristic).
//  4. Basename exactly `skeind` / `skeind.exe`. `skein` is NOT an
//     acceptable service binary after the collapse, even though it
//     has a `run` command — one service identity.
//
// The basename rule is a subset of Step 2's server-identity
// verification (api.daemonImageNames allows skein or skeind), so a
// binary registered here always passes VerifyUnixServer /
// VerifyPipeServer when it later runs as the daemon.
//
// Upgrade and rollback (RegisterDaemon / RollbackDaemon):
//   - Before rewriting the registration, RegisterDaemon copies the
//     previously registered binary to <old>.old and the current
//     config file to a timestamped backup next to it. A missing
//     previous binary aborts the upgrade before any rewrite — the
//     rollback target must exist, or there is no rollback.
//   - A failed registration write restores the previous registration
//     (best effort on the system backend; on Linux the unit write
//     itself is temp+rename, so a reload failure also restores the
//     previous unit bytes). The .old binary and the config backup
//     are NEVER deleted on failure, and are never deleted by a
//     successful rollback either, so a retry remains possible.
//   - RollbackDaemon re-validates the old binary, restores it to
//     its registered path from the .old copy if the file is gone,
//     restores the config bytes from the backup, and only then
//     rewrites the registration back. Config restore here is a
//     byte-for-byte restore of the exact bytes RegisterDaemon
//     backed up — never a parse, edit, or new config write — so it
//     is not a second config writer; Step 9's enforcement sweep
//     should record this rollback exception explicitly.
//   - RegisterDaemon registers only: it does not start the service.
//     Callers (ensureService, MintBootstrap) own start + the 15 s
//     wait-for-Running, as before.
//   - No config content is written by this file: the config backup
//     is a byte copy, and the live config is written only through
//     internal/configstore by the app layer (Step 3).
package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
)

// RegisterOptions selects the daemon binary for one registration.
type RegisterOptions struct {
	// ConfigPath is the system config the service runs with. Required,
	// absolute.
	ConfigPath string
	// DaemonPath, when non-empty, is the explicit installer/package
	// candidate. It is validated exactly like a package candidate;
	// it is the only way to register a binary outside packageDirs.
	// Empty means package-directory search only.
	DaemonPath string
}

// Registration records one successful RegisterDaemon call, including
// everything RollbackDaemon needs. Backup paths are "" when there was
// nothing to back up (fresh install, or no config file yet — the
// bootstrap path registers before any config exists).
type Registration struct {
	DaemonPath         string
	PreviousDaemonPath string
	HadPrevious        bool
	// BinaryBackupPath is <previous binary>.old, a byte copy of the
	// previously registered binary taken before the rewrite.
	BinaryBackupPath string
	// ConfigBackupPath is the timestamped byte copy of the config
	// file taken before the rewrite.
	ConfigBackupPath string
}

// backend is the OS service-manager surface RegisterDaemon drives.
// The production backend wraps the existing install/rewrite/stop/
// start/installed functions; tests swap it for a fake (see
// register_test.go). RegisterDaemon never touches systemctl/sc
// directly, so the whole upgrade/rollback flow is testable without
// a real system service.
type backend interface {
	installed() (executable, configPath string, ok bool, err error)
	install(executable, configPath string) error
	rewrite(executable, configPath string) error
	stop() error
	start() error
}

type systemBackend struct{}

func (systemBackend) installed() (string, string, bool, error) { return installed() }
func (systemBackend) install(exe, cfg string) error            { return install(exe, cfg) }
func (systemBackend) rewrite(exe, cfg string) error            { return rewrite(exe, cfg) }
func (systemBackend) stop() error                              { return stop() }
func (systemBackend) start() error                             { return start() }

var svcBackend backend = systemBackend{}

// packageDirs are the only directories the package search consults.
// Tests override this var to temp dirs; production code never does.
// It deliberately excludes the helper's directory, the current
// executable's directory, and every PATH entry.
var packageDirs = defaultPackageDirs()

func defaultPackageDirs() []string {
	if runtime.GOOS == "windows" {
		dirs := []string{}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			dirs = append(dirs, filepath.Join(pf, "Skein", "skeind.exe"))
		}
		if pf := os.Getenv("ProgramFiles(x86)"); pf != "" {
			dirs = append(dirs, filepath.Join(pf, "Skein", "skeind.exe"))
		}
		return dirs
	}
	return []string{
		"/usr/local/bin/skeind",
		"/usr/bin/skeind",
		"/usr/libexec/skeind",
	}
}

// daemonBaseName is the only acceptable service-binary basename.
func daemonBaseName() string {
	if runtime.GOOS == "windows" {
		return "skeind.exe"
	}
	return "skeind"
}

// ValidateDaemonPath enforces the §6.1 checklist, in order. It
// rejects, with a distinct reason each: non-absolute paths, symlinks
// and non-regular files, world-writable parents, and any basename
// other than skeind/skeind.exe.
func ValidateDaemonPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("service: daemon path %q is not absolute", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("service: daemon path %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("service: daemon path %q is not a regular file (symlinks rejected)", path)
	}
	if err := checkParentWritable(path); err != nil {
		return err
	}
	if filepath.Base(path) != daemonBaseName() {
		return fmt.Errorf("service: daemon path %q: basename must be %s (skein is not a service binary)", path, daemonBaseName())
	}
	return nil
}

// ResolveDaemonPath returns the validated daemon binary for an
// install/upgrade: the explicit candidate when given, otherwise the
// first valid package-directory candidate. It never consults PATH,
// the helper's directory, or the currently registered executable.
func ResolveDaemonPath(explicit string) (string, error) {
	if explicit != "" {
		if err := ValidateDaemonPath(explicit); err != nil {
			return "", fmt.Errorf("service: explicit daemon path: %w", err)
		}
		return explicit, nil
	}
	var tried []string
	for _, cand := range packageDirs {
		tried = append(tried, cand)
		if err := ValidateDaemonPath(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("service: no valid skeind found in package directories (%s); install the Skein package first (PATH and the helper directory are never searched)", strings.Join(tried, ", "))
}

// RegisterDaemon registers newDaemon as the service binary for
// configPath, with upgrade backups and restore-on-failure. It stops
// a previously registered service first (idempotent) and leaves the
// service stopped; the caller starts it.
func RegisterDaemon(opts RegisterOptions) (Registration, error) {
	if !filepath.IsAbs(opts.ConfigPath) {
		return Registration{}, fmt.Errorf("service: config path %q is not absolute", opts.ConfigPath)
	}
	daemonPath, err := ResolveDaemonPath(opts.DaemonPath)
	if err != nil {
		return Registration{}, err
	}
	prevExe, prevCfg, hadPrev, err := svcBackend.installed()
	if err != nil {
		return Registration{}, fmt.Errorf("service: read existing registration: %w", err)
	}
	reg := Registration{DaemonPath: daemonPath, HadPrevious: hadPrev, PreviousDaemonPath: prevExe}
	if !hadPrev {
		if err := svcBackend.install(daemonPath, opts.ConfigPath); err != nil {
			return Registration{}, fmt.Errorf("service: install %s: %w", daemonPath, err)
		}
		return reg, nil
	}
	// Upgrade path: stop first (a running Windows service rejects a
	// binPath rewrite; a running Linux service must not be
	// re-pointed under it), then back up before any rewrite.
	if err := svcBackend.stop(); err != nil {
		return Registration{}, fmt.Errorf("service: stop existing service before upgrade: %w", err)
	}
	// The rollback target must exist. A registration pointing at a
	// deleted binary cannot be rolled back to; refuse to destroy
	// the only record of it — uninstall first, then fresh-install.
	if _, err := os.Lstat(prevExe); err != nil {
		return Registration{}, fmt.Errorf("service: upgrade refused: previously registered binary %q is missing (%v); uninstall first, then install", prevExe, err)
	}
	bakBin := prevExe + ".old"
	if err := copyFile(prevExe, bakBin); err != nil {
		return Registration{}, fmt.Errorf("service: back up previous binary to %s: %w", bakBin, err)
	}
	reg.BinaryBackupPath = bakBin
	if _, err := os.Stat(opts.ConfigPath); err == nil {
		stamp := time.Now().Format("20060102-150405")
		bakCfg := opts.ConfigPath + ".upgrade-bak-" + stamp
		if err := copyFile(opts.ConfigPath, bakCfg); err != nil {
			return Registration{}, fmt.Errorf("service: back up config to %s: %w", bakCfg, err)
		}
		reg.ConfigBackupPath = bakCfg
	}
	if err := svcBackend.rewrite(daemonPath, opts.ConfigPath); err != nil {
		// Best-effort restore of the previous registration; the
		// backups are never deleted, so a failed restore is still
		// recoverable by hand from the paths in the error.
		restoreErr := svcBackend.rewrite(prevExe, prevCfg)
		if restoreErr != nil {
			return Registration{}, fmt.Errorf("service: upgrade to %s failed (%v); restoring %s also failed (%v); previous binary backup at %s, config backup at %s", daemonPath, err, prevExe, restoreErr, bakBin, reg.ConfigBackupPath)
		}
		return Registration{}, fmt.Errorf("service: upgrade to %s failed (%v); previous registration %s restored; previous binary backup at %s, config backup at %s", daemonPath, err, prevExe, bakBin, reg.ConfigBackupPath)
	}
	return reg, nil
}

// UpgradeDaemon is RegisterDaemon with an explicit new binary — the
// entry point the Step 7 daemon update path calls with its staged,
// verified binary. The new path is the argument; the old path is
// only the rollback target, never the candidate (this is the fix
// for the Agent D "rewrite passes the already-installed exe" bug
// by construction).
func UpgradeDaemon(newDaemonPath, configPath string) (Registration, error) {
	return RegisterDaemon(RegisterOptions{ConfigPath: configPath, DaemonPath: newDaemonPath})
}

// RollbackDaemon undoes a RegisterDaemon/UpgradeDaemon upgrade:
// it re-validates the old binary, restores it from the .old copy
// if the registered file is gone, restores the config bytes from
// the backup, and rewrites the registration back. Backups are
// never deleted, on success or failure.
func RollbackDaemon(reg Registration, configPath string) error {
	if !reg.HadPrevious || reg.PreviousDaemonPath == "" {
		return fmt.Errorf("service: nothing to roll back to (no previous registration recorded)")
	}
	if err := ValidateDaemonPath(reg.PreviousDaemonPath); err == nil {
		// Old binary still in place: nothing to restore from .old.
	} else if reg.BinaryBackupPath != "" {
		if _, err := os.Stat(reg.BinaryBackupPath); err != nil {
			return fmt.Errorf("service: rollback refused: previous binary %q is invalid (%v) and its backup %s is missing (%v); backups were not deleted", reg.PreviousDaemonPath, err, reg.BinaryBackupPath, err)
		}
		if err := copyFile(reg.BinaryBackupPath, reg.PreviousDaemonPath); err != nil {
			return fmt.Errorf("service: restore previous binary from %s: %w", reg.BinaryBackupPath, err)
		}
		if err := ValidateDaemonPath(reg.PreviousDaemonPath); err != nil {
			return fmt.Errorf("service: restored previous binary %q still invalid: %w", reg.PreviousDaemonPath, err)
		}
	} else {
		return fmt.Errorf("service: rollback refused: previous binary %q is invalid (%v) and no binary backup was recorded", reg.PreviousDaemonPath, err)
	}
	// Restore the config bytes before rewriting the registration,
	// so a registration-rewrite failure does not leave a new
	// binary pointed at a half-upgraded config.
	if reg.ConfigBackupPath != "" {
		if _, err := os.Stat(reg.ConfigBackupPath); err != nil {
			return fmt.Errorf("service: rollback refused: config backup %s is missing: %w", reg.ConfigBackupPath, err)
		}
		if err := copyFileAtomic(reg.ConfigBackupPath, configPath); err != nil {
			return fmt.Errorf("service: restore config from %s: %w", reg.ConfigBackupPath, err)
		}
	}
	_, _, installed, err := svcBackend.installed()
	if err != nil {
		return fmt.Errorf("service: rollback: read registration: %w", err)
	}
	if installed {
		if err := svcBackend.rewrite(reg.PreviousDaemonPath, configPath); err != nil {
			return fmt.Errorf("service: rollback: rewrite to %s: %w (binary and config backups at %s, %s were not deleted)", reg.PreviousDaemonPath, err, reg.BinaryBackupPath, reg.ConfigBackupPath)
		}
	} else if err := svcBackend.install(reg.PreviousDaemonPath, configPath); err != nil {
		return fmt.Errorf("service: rollback: reinstall %s: %w (backups at %s, %s were not deleted)", reg.PreviousDaemonPath, err, reg.BinaryBackupPath, reg.ConfigBackupPath)
	}
	return nil
}

// copyFile copies src to dst (same mode bits as src, best effort).
// It is a byte copy used for binary/config backups, not a config
// write: it never parses or modifies content.
//
// A destination created inside a directory whose DACL set-owner
// protected inherits no ACEs at all, so on Windows the new file would be
// deny-all (the write succeeds through the handle, but the backup is then
// unreadable when a rollback needs it). RestrictDACL sets that DACL and
// leaves the Unix mode from the create call untouched.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if err := fileperm.RestrictDACL(dst); err != nil {
		out.Close()
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyFileAtomic copies src to dst via temp+rename in dst's
// directory, so a rollback config restore never leaves a half-written
// config behind.
//
// The DACL is set before the mode: after set-owner the destination
// directory's DACL is protected with non-inheritable ACEs, so the temp
// file inherits nothing (deny-all on Windows) — and Chmod, which is
// attribute-based there, would itself be refused. Restrict runs first;
// the source mode is then applied on Unix, where it is the access
// control.
func copyFileAtomic(src, dst string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	info, err := os.Stat(src)
	if err != nil {
		tmp.Close()
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		tmp.Close()
		return err
	}
	defer in.Close()
	if err := fileperm.Restrict(name); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dst)
}
