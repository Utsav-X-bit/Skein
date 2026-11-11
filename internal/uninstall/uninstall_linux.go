//go:build linux

// Package uninstall removes a Skein installation from this host: the system
// service, the firewall rules Skein added, the installed binaries and desktop
// integration, the node's configuration, identity and backups, and the
// invoking user's desktop data.
package uninstall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Utsav-X-bit/skein/internal/app"
	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/service"
)

// layout is the exact set of paths the installer (scripts/install-linux.sh)
// and the nfpm package write outside a user's home. Uninstall only ever
// deletes these paths, never a directory scan, so a source checkout or a
// development build is never touched.
type layout struct {
	cli, daemon, desktop, helper, apparmor string
	integration                            []string
	configDir, stateDir, runDir            string
	configPath                             string
	userHome                               string
	userData                               []string
	otherUsers                             []userFiles
}

type userFiles struct {
	home  string
	paths []string
}

// files is every single-file artifact of the installation.
func (p layout) files() []string {
	return append([]string{p.cli, p.daemon, p.desktop, p.helper, p.apparmor}, p.integration...)
}

func installedPaths() layout {
	return layout{
		cli:      "/usr/local/bin/skein",
		daemon:   "/usr/local/bin/skeind",
		desktop:  "/usr/local/bin/skein-desktop",
		helper:   "/usr/libexec/skein-helper",
		apparmor: "/etc/apparmor.d/skein-desktop",
		integration: []string{
			"/usr/share/polkit-1/actions/org.skein.helper.policy",
			"/usr/share/applications/skein-desktop.desktop",
			"/usr/share/icons/hicolor/128x128/apps/skein-desktop.png",
			// Shipped by the nfpm package only; absent after a manual install.
			"/usr/lib/systemd/system/skein.service",
			"/usr/lib/sysusers.d/skein.conf",
			"/usr/lib/tmpfiles.d/skein.conf",
			// Left behind if `systemctl disable` failed; harmless to unlink.
			"/etc/systemd/system/multi-user.target.wants/skein.service",
		},
		configDir:  app.SystemDir(),
		stateDir:   filepath.Dir(app.BackupRoot()),
		runDir:     "/run/skein",
		configPath: app.SystemConfigPath(),
	}
}

// system is the host surface uninstall touches. Tests replace it wholesale so
// no test can stop a real service, run a package manager, or edit firewall
// rules.
type system struct {
	exec             func(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
	euid             func() int
	exe              func() (string, error)
	verify           func(string) error
	status           func() (service.Status, error)
	stop             func() error
	uninstallService func() error
	removeFirewall   func(firewall.Config) error
	removeGroup      func(context.Context) error
	checkProcesses   func(string) error
	loadConfig       func(string) (conf.Config, error)
}

var host = system{
	exec:             runCommand,
	euid:             os.Geteuid,
	exe:              os.Executable,
	verify:           verifiedExecutable,
	status:           service.ReadStatus,
	stop:             service.Stop,
	uninstallService: service.Uninstall,
	removeFirewall:   firewall.Remove,
	removeGroup:      removeSkeinGroup,
	checkProcesses:   checkProcesses,
	loadConfig:       conf.Load,
}

// Run removes the installation and returns a message for the caller to print.
// The caller owns the confirmation prompt. Without root it re-executes the
// verified installed CLI through sudo or pkexec.
func Run(ctx context.Context) (string, error) {
	if host.euid() != 0 {
		return elevate(ctx)
	}
	// All child tools run with a system-only PATH, including the service and
	// firewall helpers. Never execute a user-controlled binary as root.
	if err := os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin"); err != nil {
		return "", err
	}
	p := installedPaths()
	home, why := invokingHome(resolveAccount)
	if home == "" {
		return "", errors.New(why)
	}
	dirs, err := userDataDirs(home)
	if err != nil {
		return "", fmt.Errorf("cannot safely remove user data: %w", err)
	}
	p.userHome, p.userData = home, dirs
	homes, err := localUserHomes(ctx)
	if err != nil {
		return "", err
	}
	for _, other := range homes {
		if other == home {
			continue
		}
		dirs, err := userDataDirs(other)
		if err != nil {
			return "", fmt.Errorf("cannot safely remove user data: %w", err)
		}
		p.otherUsers = append(p.otherUsers, userFiles{other, dirs})
	}
	return remove(ctx, p, host)
}

// remove does the work. Every deletion targets a path from p; an error means
// the installation is only partially gone, and it names what is left.
func remove(ctx context.Context, p layout, s system) (string, error) {
	if err := s.checkProcesses("skein-desktop"); err != nil {
		return "", err
	}
	// Ownership is checked before anything changes: deleting files a package
	// manager owns would corrupt its database.
	mgr, pkg, err := packageOwnership(ctx, s, p.files())
	if err != nil {
		return "", err
	}

	// The service must actually be stopped, and its registration gone, before
	// any binary it runs is deleted.
	serviceGone, err := stopAndRemoveService(s)
	if err != nil {
		return "", err
	}
	if err := s.checkProcesses("skeind"); err != nil {
		return "", err
	}

	var removed, problems []string
	drop := func(path string, tree bool) {
		if _, err := os.Lstat(path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			}
			return
		}
		remove := os.Remove
		if tree {
			remove = os.RemoveAll
		}
		if err := remove(path); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			return
		}
		removed = append(removed, path)
	}

	if serviceGone {
		removed = append(removed, "the service")
	}

	// Firewall rules are named after the config that is about to be deleted,
	// so they are removed first, while the daemon is already stopped.
	cfg, err := s.loadConfig(p.configPath)
	switch {
	case err == nil && cfg.Node.Interface != "" && cfg.Node.ListenPort != 0:
		fw := firewall.Config{Interface: cfg.Node.Interface, Port: cfg.Node.ListenPort}
		if err := s.removeFirewall(fw); err != nil {
			return "", fmt.Errorf("firewall rules were not removed; configuration was kept: %w", err)
		}
		removed = append(removed, "firewall rules")
	case err == nil:
		// No network was ever created, so the daemon added no rules.
	case errors.Is(err, fs.ErrNotExist):
	default:
		return "", fmt.Errorf("cannot identify firewall rules; configuration was kept: %s: %w", p.configPath, err)
	}

	if mgr.name != "" {
		if err := removePackage(ctx, s, mgr, pkg); err != nil {
			return "", fmt.Errorf("the service was stopped, but the %s package could not be removed (%w); configuration was kept", pkg, err)
		}
		removed = append(removed, "the "+pkg+" package")
	}

	if err := s.removeGroup(ctx); err != nil {
		return "", fmt.Errorf("Skein group was not removed; configuration was kept: %w", err)
	}

	if _, err := os.Lstat(p.apparmor); err == nil {
		_, detail, err := s.exec(ctx, "apparmor_parser", "-R", p.apparmor)
		if err != nil {
			return "", fmt.Errorf("could not unload Skein's AppArmor profile; configuration was kept: %w: %s", err, strings.TrimSpace(detail))
		}
	}

	for _, f := range p.files() {
		drop(f, false)
	}
	for _, d := range []string{p.configDir, p.stateDir, p.runDir} {
		drop(d, true)
	}
	users := p.otherUsers
	if p.userHome != "" {
		users = append(users, userFiles{p.userHome, p.userData})
	}
	for _, user := range users {
		root, err := os.OpenRoot(user.home)
		if err != nil {
			problems = append(problems, fmt.Sprintf("open user home %s: %v", user.home, err))
		} else {
			for _, d := range user.paths {
				rel, err := filepath.Rel(user.home, d)
				if err == nil {
					_, err = root.Lstat(rel)
				}
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err == nil {
					err = root.RemoveAll(rel)
				}
				if err != nil {
					problems = append(problems, fmt.Sprintf("%s: %v", d, err))
				} else {
					removed = append(removed, d)
				}
			}
			root.Close()
		}
	}

	switch {
	case len(removed) == 0 && len(problems) == 0:
		return "Skein is not installed; nothing to remove.", nil
	case len(problems) > 0:
		done := "nothing"
		if len(removed) > 0 {
			done = strings.Join(removed, ", ")
		}
		return "", fmt.Errorf("uninstall incomplete (removed: %s): %s", done, strings.Join(problems, "; "))
	default:
		return "Skein was removed: " + strings.Join(removed, ", ") + ".", nil
	}
}

// stopAndRemoveService stops the service, verifies it stopped, and removes its
// registration. removed is false when no service was registered.
func stopAndRemoveService(s system) (bool, error) {
	st, err := s.status()
	if err != nil {
		return false, fmt.Errorf("cannot read the service state, so nothing was deleted: %w", err)
	}
	if st.State == service.StateMissing {
		return false, nil
	}
	if err := s.stop(); err != nil {
		return false, fmt.Errorf("the service could not be stopped, so nothing was deleted: %w", err)
	}
	st, err = s.status()
	if err != nil {
		return false, fmt.Errorf("could not verify the service stopped, so nothing was deleted: %w", err)
	}
	if st.State != service.StateStopped && st.State != service.StateMissing {
		return false, fmt.Errorf("the service is %s after stop, so nothing was deleted", st.State)
	}
	if err := s.uninstallService(); err != nil {
		return false, fmt.Errorf("the service registration could not be removed, so nothing was deleted: %w", err)
	}
	return true, nil
}

// pkgManager is one package manager: how to ask it who owns a path, and how to
// remove a package. The remove commands are tried in order (apt-get, then apt).
type pkgManager struct {
	name   string
	query  []string
	remove [][]string
}

var managers = []pkgManager{
	{
		name:   "dpkg",
		query:  []string{"dpkg-query", "-S"},
		remove: [][]string{{"apt-get", "remove", "-y", "--"}, {"apt", "remove", "-y", "--"}},
	},
	{
		name:   "rpm",
		query:  []string{"rpm", "-qf"},
		remove: [][]string{{"dnf", "remove", "-y", "--"}, {"yum", "remove", "-y", "--"}},
	},
	{
		name:   "pacman",
		query:  []string{"pacman", "-Qo"},
		remove: [][]string{{"pacman", "-R", "--noconfirm", "--"}},
	},
}

// packageOwnership reports the package manager and package that own any of
// files. An empty manager means Skein was installed by hand. An error means
// ownership could not be determined, so nothing may be deleted.
func packageOwnership(ctx context.Context, s system, files []string) (pkgManager, string, error) {
	var present []string
	for _, f := range files {
		if _, err := os.Lstat(f); err == nil {
			present = append(present, f)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return pkgManager{}, "", fmt.Errorf("cannot inspect %s: %w", f, err)
		}
	}
	if len(present) == 0 {
		return pkgManager{}, "", nil
	}
	var found pkgManager
	for _, m := range managers {
		for _, f := range present {
			args := append(append([]string{}, m.query[1:]...), f)
			out, errOut, err := s.exec(ctx, m.query[0], args...)
			if errors.Is(err, exec.ErrNotFound) {
				break // this package manager is not installed
			}
			if err != nil {
				if notOwned(errOut) || notOwned(out) {
					continue
				}
				return pkgManager{}, "", fmt.Errorf("cannot tell whether %s belongs to a package, so nothing was deleted: %v: %s", f, err, strings.TrimSpace(errOut))
			}
			pkg := owner(m.name, out)
			if pkg == "" {
				return pkgManager{}, "", fmt.Errorf("%s belongs to a package but its name could not be read from %q, so nothing was deleted", f, strings.TrimSpace(out))
			}
			if pkg != "skein-desktop" {
				return pkgManager{}, "", fmt.Errorf("%s belongs to unrelated package %q; nothing was deleted", f, pkg)
			}
			if found.name != "" && found.name != m.name {
				return pkgManager{}, "", errors.New("multiple package managers claim Skein files; remove the packages manually")
			}
			found = m
		}
	}
	if found.name != "" {
		return found, "skein-desktop", nil
	}
	return pkgManager{}, "", nil
}

// notOwned recognises the "no package owns this path" answer, which is a
// normal non-zero exit for every package manager.
func notOwned(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "no path found matching") ||
		strings.Contains(m, "is not owned by any package") ||
		strings.Contains(m, "no package owns")
}

// owner extracts the package name from each manager's query output.
func owner(manager, out string) string {
	out = strings.TrimSpace(out)
	switch manager {
	case "dpkg":
		// "pkg: /path" or "pkg1, pkg2: /path"
		if i := strings.Index(out, ":"); i >= 0 {
			out = out[:i]
		}
		if i := strings.Index(out, ","); i >= 0 {
			out = out[:i]
		}
	case "pacman":
		// "/path is owned by pkg version"
		if _, rest, ok := strings.Cut(out, " is owned by "); ok {
			out = rest
		}
	}
	if fields := strings.Fields(out); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func removePackage(ctx context.Context, s system, m pkgManager, pkg string) error {
	var failures []string
	for _, prefix := range m.remove {
		argv := append(append([]string{}, prefix...), pkg)
		out, errOut, err := s.exec(ctx, argv[0], argv[1:]...)
		if err == nil {
			return nil
		}
		if errors.Is(err, exec.ErrNotFound) {
			failures = append(failures, argv[0]+" is not installed")
			continue
		}
		msg := strings.TrimSpace(out + "\n" + errOut)
		if msg != "" {
			return fmt.Errorf("%s: %v: %s", strings.Join(argv, " "), err, msg)
		}
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return fmt.Errorf("no package manager command is available (%s); remove it with your package manager", strings.Join(failures, ", "))
}

// invokingHome returns the home directory of the user who asked for the
// uninstall. It is resolved through NSS, never from $HOME, so an environment
// variable cannot point root at an arbitrary path.
func invokingHome(resolve func(string) (*user.User, error)) (string, string) {
	key := os.Getenv("PKEXEC_UID")
	if key == "" {
		key = os.Getenv("SUDO_USER")
	}
	if key == "" {
		key = strconv.Itoa(os.Geteuid())
	}
	u, err := resolve(key)
	if err != nil {
		return "", fmt.Sprintf("The desktop data of %q was kept: %v.", key, err)
	}
	return u.HomeDir, ""
}

func resolveAccount(key string) (*user.User, error) {
	if _, err := strconv.Atoi(key); err == nil {
		return user.LookupId(key)
	}
	return user.Lookup(key)
}

// localUserHomes enumerates registered user homes. Source trees are
// never searched; only known Skein data directories below these homes are used.
func localUserHomes(ctx context.Context) ([]string, error) {
	out, stderr, err := runCommand(ctx, "getent", "passwd")
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate user homes: %w: %s", err, strings.TrimSpace(stderr))
	}
	var homes []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 {
			return nil, errors.New("cannot parse user account database")
		}
		if _, err := strconv.Atoi(fields[2]); err != nil {
			return nil, fmt.Errorf("invalid uid for account %q", fields[0])
		}
		home := filepath.Clean(fields[5])
		if !filepath.IsAbs(home) || home == "/" || seen[home] {
			continue
		}
		info, err := os.Stat(home)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("cannot inspect home %s: %w", home, err)
		}
		if info.IsDir() {
			homes = append(homes, home)
			seen[home] = true
		}
	}
	return homes, nil
}

// userDataDirs validates the invoking user's desktop data directories before
// root deletes them: the Wails app data under XDG config, data and cache.
func userDataDirs(home string) ([]string, error) {
	if home == "" || !filepath.IsAbs(home) {
		return nil, fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	if home == string(filepath.Separator) {
		return nil, errors.New("home directory is /")
	}
	var dirs []string
	for _, rel := range []string{".config/skein", ".config/skein-desktop", ".local/share/skein-desktop", ".cache/skein-desktop"} {
		dir, err := safeSubdir(home, rel)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// safeSubdir joins rel under home and refuses a parent that escapes home
// through a symlink, so a user-controlled link cannot aim root elsewhere.
func safeSubdir(home, rel string) (string, error) {
	dir := filepath.Join(home, rel)
	parent := filepath.Dir(dir)
	if fi, err := os.Lstat(parent); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		real, err := filepath.EvalSymlinks(parent)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", parent, err)
		}
		if real != home && !strings.HasPrefix(real, home+string(filepath.Separator)) {
			return "", fmt.Errorf("%s points outside %s", parent, home)
		}
	}
	return dir, nil
}

// elevate re-runs the installed CLI as root. It refuses to elevate anything
// that is not the root-owned installed CLI, so a binary in a user-writable
// directory can never be run with privilege.
func elevate(ctx context.Context) (string, error) {
	exe, err := host.exe()
	if err != nil {
		return "", fmt.Errorf("uninstall needs root; run it with sudo: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if exe != installedPaths().cli {
		return "", fmt.Errorf("uninstall needs root; run: sudo %s uninstall --yes", exe)
	}
	if err := host.verify(exe); err != nil {
		return "", fmt.Errorf("refusing to elevate %s: %v; run: sudo %s uninstall --yes", exe, err, exe)
	}

	out, errOut, err := host.exec(ctx, "sudo", "-n", "--", exe, "uninstall", "--yes")
	if err == nil {
		return childMessage(out), nil
	}
	if !sudoCannotPrompt(err, errOut) {
		return "", childFailure(err, out, errOut)
	}

	out, errOut, err = host.exec(ctx, "pkexec", exe, "uninstall", "--yes")
	if err == nil {
		return childMessage(out), nil
	}
	return "", fmt.Errorf("uninstall needs root and neither sudo nor pkexec could elevate: %v; run: sudo %s uninstall --yes",
		childFailure(err, out, errOut), exe)
}

// sudoCannotPrompt reports whether sudo failed to elevate at all (no cached
// credential, no terminal, no askpass) rather than the child failing.
func sudoCannotPrompt(err error, stderr string) bool {
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	m := strings.ToLower(stderr)
	for _, s := range []string{
		"a password is required",
		"a terminal is required",
		"no askpass program",
		"is not in the sudoers",
		"not allowed to execute",
	} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

func childMessage(out string) string {
	if msg := strings.TrimSpace(out); msg != "" {
		return msg
	}
	return "Skein was removed."
}

func childFailure(err error, out, errOut string) error {
	if msg := strings.TrimSpace(out + "\n" + errOut); msg != "" {
		return fmt.Errorf("%v: %s", err, msg)
	}
	return err
}

// verifiedExecutable accepts only the installed CLI: a root-owned regular file
// in a root-owned directory that neither group nor others can write.
func verifiedExecutable(path string) error {
	for _, p := range []string{filepath.Dir(path), path} {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", p)
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s is writable by group or other", p)
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 {
			return fmt.Errorf("%s is not owned by root", p)
		}
	}
	if fi, err := os.Lstat(path); err != nil {
		return err
	} else if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

func runCommand(ctx context.Context, name string, args ...string) (string, string, error) {
	var out, errOut strings.Builder
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

func removeSkeinGroup(ctx context.Context) error {
	out, stderr, err := runCommand(ctx, "getent", "group", "skein")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 2 {
			return nil
		}
		return fmt.Errorf("getent: %w: %s", err, strings.TrimSpace(stderr))
	}
	fields := strings.Split(strings.TrimSpace(out), ":")
	if len(fields) != 4 || fields[0] != "skein" {
		return errors.New("unexpected group entry")
	}
	_, stderr, err = runCommand(ctx, "groupdel", "skein")
	if err != nil {
		return fmt.Errorf("groupdel: %w: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

func checkProcesses(name string) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return fmt.Errorf("cannot inspect running processes: %w", err)
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot inspect process %s: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(data)) == name {
			return fmt.Errorf("%s is still running (pid %s); quit it and retry uninstall", name, entry.Name())
		}
	}
	return nil
}
