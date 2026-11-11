//go:build windows

package uninstall

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Utsav-X-bit/skein/internal/fileperm"
	"github.com/Utsav-X-bit/skein/internal/service"
)

// Layout owned by both Windows installers: the NSIS package
// (gui/build/windows/nsis) and scripts/install-windows.ps1. The script
// install has no shortcuts and no uninstall registry entry, so every item
// below is removed only when it exists.
const (
	installDirName = "Skein"

	// uninstallKeyPath is the Wails/NSIS Add-Remove-Programs key. It is
	// written to HKLM for the machine-wide install this package creates.
	uninstallKeyPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\SkeinSkein`

	// profileListPath is where Windows records every local profile root.
	profileListPath = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList`

	desktopImage = "skein-desktop.exe"
)

// env is every OS-owned input Run touches. Tests point the whole removal at
// a scratch tree and record the privileged steps instead of running them.
type env struct {
	systemRoot   string
	systemBin    string
	programFiles string
	programData  string
	systemDir    string
	allUsersMenu string
	powershell   string

	profiles       func() ([]profile, []string, error)
	stopService    func() error
	removeFirewall func() error
	deleteRegistry func() error
	pendingDelete  func(path string) error
	trustInstall   func(path string) error
	startCleanup   func(script string) (int, error)
	captureFile    func() (string, error)
	desktopRunning func() (bool, error)
}

// Run removes a complete Skein installation: the SCM service, Skein's own
// firewall rules, the packaged payload (NSIS package or
// scripts\install-windows.ps1 layout), the machine-wide configuration,
// identity, access list and backups, the invoking user's configuration and
// WebView2 data, the shortcuts, and the uninstall registry entry.
//
// It needs an Administrator terminal. Refusing unelevated execution avoids
// elevating an executable or result path controlled by an unprivileged user.
//
// Files still open in this process (skein.exe is the running image) cannot
// be deleted by it. They are handed to a detached cleanup worker that waits
// for this process to exit, and registered for deletion at the next reboot
// as the guarantee; the returned message names both.
func Run(ctx context.Context) (string, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return "", errors.New("run skein uninstall from an Administrator PowerShell; nothing was removed")
	}
	e, err := systemEnv()
	if err != nil {
		return "", err
	}
	if err := os.Chdir(e.systemRoot); err != nil {
		return "", fmt.Errorf("change to Windows directory: %w", err)
	}
	if err := os.Setenv("PATH", e.systemBin); err != nil {
		return "", err
	}
	if err := os.Setenv("PSModulePath", filepath.Join(e.systemBin, "WindowsPowerShell", "v1.0", "Modules")); err != nil {
		return "", err
	}
	return run(ctx, e)
}

// systemEnv resolves the real machine layout.
func systemEnv() (env, error) {
	programFiles, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return env{}, fmt.Errorf("resolve Program Files: %w", err)
	}
	programData, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return env{}, fmt.Errorf("resolve ProgramData: %w", err)
	}
	systemRoot, err := windows.KnownFolderPath(windows.FOLDERID_Windows, 0)
	if err != nil {
		return env{}, fmt.Errorf("resolve Windows directory: %w", err)
	}
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return env{}, fmt.Errorf("resolve system directory: %w", err)
	}
	programs, err := windows.KnownFolderPath(windows.FOLDERID_CommonPrograms, 0)
	if err != nil {
		return env{}, fmt.Errorf("resolve Start Menu: %w", err)
	}
	powershellPath := filepath.Join(systemDirectory, "WindowsPowerShell", "v1.0", "powershell.exe")

	return env{
		systemRoot:   systemRoot,
		systemBin:    systemDirectory,
		programFiles: programFiles,
		programData:  programData,
		systemDir:    filepath.Join(programData, installDirName),
		allUsersMenu: programs,
		powershell:   powershellPath,

		profiles:       func() ([]profile, []string, error) { return localProfiles(systemRoot) },
		stopService:    service.Uninstall,
		removeFirewall: func() error { return removeWindowsFirewall(powershellPath) },
		deleteRegistry: deleteUninstallKey,
		pendingDelete:  pendingDelete,
		trustInstall:   trustInstallDir,
		startCleanup: func(script string) (int, error) {
			return startCleanupProcess(powershellPath, script)
		},
		captureFile:    captureFile,
		desktopRunning: desktopRunning,
	}, nil
}

// run performs the elevated removal.
func run(ctx context.Context, e env) (string, error) {
	// A running desktop locks skein-desktop.exe and its WebView2 data; the
	// NSIS uninstaller refuses for the same reason. Its window Close button
	// only hides it to the tray, so the refusal must name the tray.
	if running, err := e.desktopRunning(); err != nil {
		return "", fmt.Errorf("check whether Skein Desktop is running: %w; nothing was removed", err)
	} else if running {
		return "", errors.New("Skein Desktop is running (it may be another user's session); quit it from its system-tray menu, then run the uninstall again. Nothing was removed")
	}

	profiles, profileSkips, err := e.profiles()
	if err != nil {
		return "", fmt.Errorf("enumerate local profiles: %w; nothing was removed", err)
	}
	if len(profileSkips) > 0 {
		return "", fmt.Errorf("cannot inspect all profiles: %s; nothing was removed", strings.Join(profileSkips, "; "))
	}
	dirs, dataSkips := userDataTargets(profiles, e.systemRoot)
	links, linkSkips := shortcutTargets(profiles, e.allUsersMenu)
	if len(dataSkips) > 0 || len(linkSkips) > 0 {
		return "", fmt.Errorf("cannot inspect all profile data: %s; nothing was removed", strings.Join(append(dataSkips, linkSkips...), "; "))
	}
	installDir := filepath.Join(e.programFiles, installDirName)
	if _, err := os.Lstat(installDir); err == nil {
		if err := e.trustInstall(installDir); err != nil {
			return "", fmt.Errorf("unsafe installation directory: %w; nothing was removed", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	var r report

	// The service is the single writer for the system config and holds both
	// the daemon image and %ProgramData%\Skein. Stop it before either is
	// touched, and abort if it will not stop: deleting files around a
	// running daemon is how an uninstall leaves a half-removed install.
	if err := e.stopService(); err != nil {
		return "", fmt.Errorf("stop and remove the skein service: %w; nothing was removed", err)
	}
	r.removed(`system service "skein"`)

	// Firewall rules are removed by their exact display names only. Skein
	// never guesses ownership of a rule it did not create.
	if err := e.removeFirewall(); err != nil {
		return "", fmt.Errorf("Skein firewall rules could not be removed; configuration was kept: %w", err)
	}

	// Config, identity, access list, state, and backups all live inside the
	// system directory; Skein.bak is a previous purge backup.
	for _, dir := range []string{e.systemDir, e.systemDir + ".bak"} {
		existed, err := removeTree(dir, e.programData)
		switch {
		case err != nil:
			r.fail(dir, err)
		case existed:
			r.removed(dir)
		}
	}

	for _, t := range dirs {
		existed, err := removeTree(t.path, t.root)
		switch {
		case err != nil:
			r.fail(t.path, err)
		case existed:
			r.removed(t.path)
		}
	}

	for _, t := range links {
		existed, err := removeFile(t.path, t.root)
		switch {
		case err != nil:
			r.fail(t.path, err)
		case existed:
			r.removed(t.path)
		}
	}

	if err := e.deleteRegistry(); err != nil {
		r.fail("uninstall registry entry", err)
	} else {
		r.removed(`uninstall entry ` + uninstallKeyPath + ` (HKLM and HKCU)`)
	}

	// The payload goes last: it contains the helper and this executable.
	removeInstallDir(ctx, e, &r)

	text, failed := r.text()
	if failed {
		return "", errors.New(text)
	}
	return text, nil
}

// removeInstallDir deletes %ProgramFiles%\Skein. Files locked by a running
// image cannot be deleted, so they are handed to a cleanup worker that waits
// for this process to exit, and registered for deletion at reboot as a
// guarantee if that worker cannot start.
func removeInstallDir(ctx context.Context, e env, r *report) {
	dir := filepath.Join(e.programFiles, installDirName)
	if err := ownedChild(dir, e.programFiles); err != nil {
		r.fail(dir, err)
		return
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		r.fail(dir, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		r.fail(dir, errors.New("is a reparse point; refusing to delete through it"))
		return
	}
	r.note("shared Wintun driver and any Wintun adapter (not provably Skein-only)")

	removeErr := os.RemoveAll(dir)
	if removeErr == nil {
		r.removed(dir)
		return
	}
	left := remainingPaths(dir)
	if len(left) == 0 {
		r.fail(dir, removeErr)
		return
	}

	// Quarantine the old directory under a unique admin-only name before
	// scheduling deletion. A reinstall at the canonical path is never queued
	// for deletion on reboot.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		r.fail(dir, err)
		return
	}
	quarantine := dir + ".uninstall-" + hex.EncodeToString(nonce[:])
	if err := os.Rename(dir, quarantine); err != nil {
		r.fail(dir, fmt.Errorf("cannot isolate locked files; close Skein processes and retry: %w", err))
		return
	}
	left = remainingPaths(quarantine)
	registered := true
	for _, p := range append(append([]string{}, left...), quarantine) {
		if err := e.pendingDelete(p); err != nil {
			registered = false
			r.fail(p, fmt.Errorf("register deletion at the next reboot: %w", err))
		}
	}

	logPath, err := e.captureFile()
	detail := fmt.Sprintf("%d locked file(s) remain in %s", len(left), quarantine)
	if registered {
		detail += "; registered for deletion at the next reboot"
	}
	if err != nil {
		r.fail(dir, fmt.Errorf("%s; the cleanup log could not be created: %w", detail, err))
		return
	}
	if _, err := e.startCleanup(cleanupScript([]string{quarantine}, logPath, os.Getpid())); err != nil {
		r.fail(dir, fmt.Errorf("%s; the cleanup process could not start: %w", detail, err))
		return
	}
	if err := waitCleanupLog(ctx, logPath); err != nil {
		r.fail(dir, fmt.Errorf("%s; %w", detail, err))
		return
	}
	for _, p := range left {
		r.pending = append(r.pending, p)
	}
	r.pending = append(r.pending, quarantine)
	r.logPath = logPath
}

// target is a path the uninstall may delete and the directory it must be a
// direct child of.
type target struct{ path, root string }

// shortcutTargets lists the Start Menu and desktop links the NSIS installer
// creates: the all-users Start Menu and Public desktop, plus every local
// profile's own equivalents. Every directory is validated as a real
// directory, so a reparse point is never traversed.
func shortcutTargets(profiles []profile, allUsersMenu string) (out []target, skipped []string) {
	seen := make(map[string]bool)
	add := func(dir string) {
		if dir == "" {
			return
		}
		key := strings.ToLower(dir)
		if seen[key] {
			return
		}
		seen[key] = true
		if err := requirePlainDir(dir); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				skipped = append(skipped, fmt.Sprintf("%s: %v", dir, err))
			}
			return
		}
		out = append(out, target{path: filepath.Join(dir, "Skein.lnk"), root: dir})
	}
	add(allUsersMenu)

	for _, p := range profiles {
		if serviceAccount(p.sid) {
			continue
		}
		clean := filepath.Clean(p.path)
		if !filepath.IsAbs(clean) || filepath.Dir(clean) == clean {
			continue
		}
		if err := requirePlainDir(clean); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				skipped = append(skipped, fmt.Sprintf("%s: %v", p.path, err))
			}
			continue
		}
		// The Public desktop shares the parent directory of the real
		// profiles, so it is derived from a ProfileList path rather than
		// %PUBLIC%.
		if usersRoot := filepath.Dir(clean); filepath.Base(usersRoot) != usersRoot {
			add(filepath.Join(usersRoot, "Public", "Desktop"))
		}
		add(filepath.Join(clean, "Desktop"))
		add(filepath.Join(clean, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs"))
	}
	return out, skipped
}

// ownedChild proves path is a direct child of root before anything deletes
// it, so a redirected or malformed environment variable can never widen the
// removal to another directory.
func ownedChild(path, root string) error {
	if root == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("refusing to delete %q: %q is not an absolute root", path, root)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("refusing to delete %q: not an absolute path", path)
	}
	cleanRoot := filepath.Clean(root)
	if filepath.Dir(cleanRoot) == cleanRoot {
		return fmt.Errorf("refusing to delete %q: %q is a drive root", path, root)
	}
	clean := filepath.Clean(path)
	if !strings.EqualFold(filepath.Dir(clean), cleanRoot) {
		return fmt.Errorf("refusing to delete %q: not directly inside %q", path, root)
	}
	if base := filepath.Base(clean); base == "." || base == ".." {
		return fmt.Errorf("refusing to delete %q: not a named entry", path)
	}
	return nil
}

// removeTree deletes a directory only when it is provably a direct child of
// root. A reparse point is refused rather than followed, so a junction
// planted in place of the Skein directory can never redirect this into
// unrelated data. It reports whether the directory existed.
func removeTree(path, root string) (bool, error) {
	if err := ownedChild(path, root); err != nil {
		return false, err
	}
	handles, err := lockDirectories(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { closeDirectories(handles) }()
	dir, err := os.OpenRoot(path)
	if err != nil {
		return false, err
	}
	f, err := dir.Open(".")
	if err != nil {
		dir.Close()
		return false, err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err == nil {
		for _, entry := range entries {
			if err = dir.RemoveAll(entry.Name()); err != nil {
				break
			}
		}
	}
	dir.Close()
	if err != nil {
		return false, err
	}
	// Keep ancestors locked but release the target before removing its now
	// empty directory. os.Remove never recursively follows a replacement.
	windows.CloseHandle(handles[len(handles)-1])
	handles = handles[:len(handles)-1]
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, nil
}

// removeFile deletes one link or file that is a direct child of dir.
func removeFile(path, dir string) (bool, error) {
	if err := ownedChild(path, dir); err != nil {
		return false, err
	}
	handles, err := lockDirectories(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer closeDirectories(handles)
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// remainingPaths lists what a failed RemoveAll could not delete.
func remainingPaths(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, ent := range entries {
		out = append(out, filepath.Join(dir, ent.Name()))
	}
	return out
}

// profile is one Windows ProfileList entry: the account SID and its profile
// root.
type profile struct {
	sid  string
	path string
}

// localProfiles reads the real local profile roots from the Windows
// ProfileList. The environment is deliberately not used: an unelevated caller
// can set %APPDATA% and %USERPROFILE%, and this runs privileged.
func localProfiles(systemRoot string) ([]profile, []string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, profileListPath, registry.READ)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", profileListPath, err)
	}
	defer k.Close()
	sids, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", profileListPath, err)
	}
	out := make([]profile, 0, len(sids))
	var skipped []string
	for _, sid := range sids {
		if serviceAccount(sid) {
			continue
		}
		sub, err := registry.OpenKey(k, sid, registry.QUERY_VALUE)
		if err != nil {
			skipped = append(skipped, sid+": "+err.Error())
			continue
		}
		raw, _, err := sub.GetStringValue("ProfileImagePath")
		sub.Close()
		if err != nil || raw == "" {
			skipped = append(skipped, sid+": no usable ProfileImagePath")
			continue
		}
		raw, err = expandProfilePath(raw, systemRoot)
		if err != nil {
			skipped = append(skipped, sid+": "+err.Error())
			continue
		}
		out = append(out, profile{sid: sid, path: raw})
	}
	return out, skipped, nil
}

func expandProfilePath(path, systemRoot string) (string, error) {
	var out strings.Builder
	for {
		i := strings.IndexByte(path, '%')
		if i < 0 {
			out.WriteString(path)
			return out.String(), nil
		}
		out.WriteString(path[:i])
		path = path[i+1:]
		j := strings.IndexByte(path, '%')
		if j < 0 {
			return "", errors.New("malformed profile path")
		}
		switch strings.ToLower(path[:j]) {
		case "systemdrive":
			out.WriteString(filepath.VolumeName(systemRoot))
		case "systemroot", "windir":
			out.WriteString(systemRoot)
		default:
			return "", fmt.Errorf("unsupported variable %%%s%% in profile path", path[:j])
		}
		path = path[j+1:]
	}
}

// serviceAccount reports the SIDs whose "profile" is not a user profile.
func serviceAccount(sid string) bool {
	switch strings.ToUpper(sid) {
	case "S-1-5-18", "S-1-5-19", "S-1-5-20":
		return true
	}
	return false
}

// userDataTargets is every local profile's Skein data: the per-user config
// and identity, the desktop's history and settings, and the WebView2 user
// data directory (Wails uses %APPDATA%\<exe name>).
func userDataTargets(profiles []profile, systemRoot string) (dirs []target, skipped []string) {
	for _, p := range profiles {
		if serviceAccount(p.sid) {
			continue
		}
		roaming, err := roamingAppData(p.path, systemRoot)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				skipped = append(skipped, fmt.Sprintf("%s (%s): %v", p.path, p.sid, err))
			}
			continue
		}
		for _, name := range []string{"skein", "skein-desktop", desktopImage} {
			dirs = append(dirs, target{path: filepath.Join(roaming, name), root: roaming})
		}
	}
	return dirs, skipped
}

// roamingAppData validates a profile root and returns its roaming AppData
// directory. Every step must be a real directory: a junction planted in a
// profile must never redirect a privileged delete.
func roamingAppData(profilePath, systemRoot string) (string, error) {
	if !filepath.IsAbs(profilePath) {
		return "", errors.New("profile path is not absolute")
	}
	clean := filepath.Clean(profilePath)
	if filepath.Dir(clean) == clean {
		return "", errors.New("profile path is a drive root")
	}
	sep := string(filepath.Separator)
	if systemRoot != "" && strings.HasPrefix(strings.ToLower(clean)+sep, strings.ToLower(filepath.Clean(systemRoot))+sep) {
		return "", errors.New("profile lives under the Windows directory")
	}
	appData := filepath.Join(clean, "AppData")
	roaming := filepath.Join(appData, "Roaming")
	for _, dir := range []string{clean, appData, roaming} {
		if err := requirePlainDir(dir); err != nil {
			return "", fmt.Errorf("%s: %w", dir, err)
		}
	}
	return roaming, nil
}

// requirePlainDir rejects anything that is not a real directory, so a
// reparse point is never traversed.
func requirePlainDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("is a reparse point")
	}
	return nil
}

// desktopRunning reports whether skein-desktop.exe is running. It only ever
// reads process state; the desktop is never killed from here. It fails
// closed: a tasklist that cannot run is not evidence that the desktop is
// closed.
func desktopRunning() (bool, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return false, err
	}
	cmd := exec.Command(filepath.Join(systemDir, "tasklist.exe"),
		"/FI", "IMAGENAME eq "+desktopImage, "/NH", "/FO", "CSV")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	// A match starts with the quoted image name; the "no tasks" notice is
	// localized prose and never starts with a quote.
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(out))), `"`+desktopImage+`"`), nil
}

// deleteUninstallKey removes the Add-Remove-Programs entry from both views
// the installer can have written it to.
func deleteUninstallKey() error {
	var errs []error
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		err := registry.DeleteKey(root, uninstallKeyPath)
		if err == nil || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			continue
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %w", uninstallKeyPath, errors.Join(errs...))
}

// pendingDelete registers a path for deletion at the next reboot. It is the
// guarantee behind the cleanup worker, not the primary path: the worker
// removes the file as soon as this process exits.
func pendingDelete(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

// captureFile creates the worker log in an atomically admin-only directory.
func captureFile() (string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(base, "Skein-uninstall-"+hex.EncodeToString(nonce[:]))
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	sd, err := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return "", err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(name, &sa); err != nil {
		return "", err
	}
	log := filepath.Join(dir, "cleanup.log")
	f, err := os.OpenFile(log, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		os.Remove(dir)
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return log, nil
}

// cleanupScript is the detached worker: wait for the uninstalling process,
// retry the delete until the image is released, and record the outcome.
func cleanupScript(targets []string, log string, pid int) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'SilentlyContinue'\n")
	fmt.Fprintf(&b, "$log = %s\n", psQuote(log))
	b.WriteString("function Note($m) { Add-Content -LiteralPath $log -Value $m }\n")
	b.WriteString("Note 'started'\n")
	fmt.Fprintf(&b, "$target = %d\n", pid)
	b.WriteString("for ($i = 0; $i -lt 240; $i++) { if (-not (Get-Process -Id $target -ErrorAction SilentlyContinue)) { break }; Start-Sleep -Milliseconds 500 }\n")
	b.WriteString("$paths = @(")
	for i, t := range targets {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(psQuote(t))
	}
	b.WriteString(")\n")
	b.WriteString(`for ($i = 0; $i -lt 120; $i++) {
  $blocked = $false
  foreach ($p in $paths) {
    if (Test-Path -LiteralPath $p) {
      Remove-Item -LiteralPath $p -Recurse -Force
      if (Test-Path -LiteralPath $p) { $blocked = $true }
    }
  }
  if (-not $blocked) { break }
  Start-Sleep -Milliseconds 500
}
$left = @()
foreach ($p in $paths) { if (Test-Path -LiteralPath $p) { $left += $p } }
if ($left.Count -eq 0) {
  Note 'done: removed'
  Remove-Item -LiteralPath $log -Force
  Remove-Item -LiteralPath (Split-Path -Parent $log) -Force
} else { Note ('failed: still present: ' + ($left -join '; ')) }
`)
	return b.String()
}

// waitCleanupLog confirms the worker actually ran. It does not wait for the
// delete itself, which can only happen after this process exits; the worker
// records that outcome in the same log.
func waitCleanupLog(ctx context.Context, log string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(log); err == nil {
			text := string(data)
			if strings.Contains(text, "started") || strings.Contains(text, "done:") || strings.Contains(text, "failed:") {
				return nil
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the cleanup process did not report that it started (log: %s)", log)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// startCleanupProcess launches the worker detached, so it survives this
// process and its console.
func startCleanupProcess(powershell, script string) (int, error) {
	args := powershellArgs(script)
	flags := []uint32{
		windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB,
		windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
	}
	var lastErr error
	for _, f := range flags {
		cmd := exec.Command(powershell, args...)
		// The worker must not inherit a working directory inside the tree
		// it is deleting: Windows locks a process's current directory.
		cmd.Dir = filepath.Dir(powershell)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: f}
		if err := cmd.Start(); err != nil {
			// A job that forbids breakaway rejects the first flag set.
			lastErr = err
			continue
		}
		pid := cmd.Process.Pid
		_ = cmd.Process.Release()
		return pid, nil
	}
	return 0, lastErr
}

// powershellArgs passes the script as UTF-16 base64, so no path or argument
// is ever parsed by a shell.
func powershellArgs(script string) []string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(raw[i*2:], unit)
	}
	return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-WindowStyle", "Hidden", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw)}
}

// psQuote is a PowerShell single-quoted literal: nothing is expanded inside
// it and a quote is escaped by doubling.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// report accumulates what the uninstall did, so the returned message is the
// complete, honest account: removed, pending, left in place, failed.
type report struct {
	removedItems []string
	pending      []string
	notes        []string
	failed       []string
	logPath      string
}

func (r *report) removed(item string) { r.removedItems = append(r.removedItems, item) }

func (r *report) note(item string) { r.notes = append(r.notes, item) }

func (r *report) fail(item string, err error) {
	r.failed = append(r.failed, fmt.Sprintf("%s: %v", item, err))
}

// text renders the report and reports whether any step failed.
func (r *report) text() (string, bool) {
	var b strings.Builder
	if len(r.failed) > 0 {
		fmt.Fprintf(&b, "Skein was not fully removed: %d step(s) failed.\n", len(r.failed))
	} else if len(r.pending) > 0 {
		b.WriteString("Skein uninstall is pending final cleanup after this command exits.\n")
	} else {
		b.WriteString("Skein was removed from this computer.\n")
	}

	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString("\n" + title + "\n")
		for _, item := range items {
			b.WriteString("  - " + item + "\n")
		}
	}
	section("Removed (or already absent):", r.removedItems)
	section("Pending removal (the executable is still open):", r.pending)
	if r.logPath != "" && len(r.pending) > 0 {
		b.WriteString("  cleanup log: " + r.logPath + "\n")
	}
	section("Left in place:", r.notes)
	section("Failed:", r.failed)
	return b.String(), len(r.failed) > 0
}

func removeWindowsFirewall(powershell string) error {
	script := `$ErrorActionPreference = 'Stop'
$names = @('Skein underlay', 'Skein overlay', 'Skein overlay out', 'Skein overlay v6', 'Skein overlay out v6', 'skein')
$rules = @(Get-NetFirewallRule -PolicyStore PersistentStore | Where-Object { $names -contains $_.DisplayName })
if ($rules.Count -gt 0) { $rules | Remove-NetFirewallRule -ErrorAction Stop }
if (@(Get-NetFirewallRule -PolicyStore PersistentStore | Where-Object { $names -contains $_.DisplayName }).Count -ne 0) { throw 'Skein firewall rules remain' }`
	out, err := exec.Command(powershell, powershellArgs(script)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove Skein firewall rules: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// lockDirectories prevents replacement of any ancestor while a privileged
// removal is in progress. Reparse points are opened, inspected and refused.
func lockDirectories(path string) ([]windows.Handle, error) {
	var chain []string
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		chain = append(chain, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	var handles []windows.Handle
	for i := len(chain) - 1; i >= 0; i-- {
		name, err := windows.UTF16PtrFromString(chain[i])
		if err != nil {
			closeDirectories(handles)
			return nil, err
		}
		h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			closeDirectories(handles)
			return nil, &os.PathError{Op: "open directory", Path: chain[i], Err: err}
		}
		handles = append(handles, h)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil {
			closeDirectories(handles)
			return nil, err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
			info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			closeDirectories(handles)
			return nil, fmt.Errorf("%s is not a plain directory", chain[i])
		}
	}
	return handles, nil
}

func closeDirectories(handles []windows.Handle) {
	for i := len(handles) - 1; i >= 0; i-- {
		windows.CloseHandle(handles[i])
	}
}

func trustInstallDir(path string) error {
	handles, err := lockDirectories(path)
	if err != nil {
		return err
	}
	defer closeDirectories(handles)
	for _, dir := range []string{filepath.Dir(path), path} {
		ok, err := fileperm.DirAdminOnly(dir)
		if err != nil || !ok {
			return fmt.Errorf("%s is not admin-only: %v", dir, err)
		}
		sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if err != nil || sd == nil {
			return fmt.Errorf("read owner of %s: %v", dir, err)
		}
		owner, _, err := sd.Owner()
		if err != nil || owner == nil {
			return fmt.Errorf("read owner of %s: %v", dir, err)
		}
		system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
		if err != nil {
			return err
		}
		admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
		if err != nil {
			return err
		}
		if owner.Equals(system) || owner.Equals(admins) {
			continue
		}
		ti, _, _, err := windows.LookupSID("", `NT SERVICE\TrustedInstaller`)
		if err != nil || !owner.Equals(ti) {
			return fmt.Errorf("%s is not owned by SYSTEM, Administrators or TrustedInstaller", dir)
		}
	}
	return nil
}
