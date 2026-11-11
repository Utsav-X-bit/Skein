//go:build linux

package uninstall

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Utsav-X-bit/skein/internal/conf"
	"github.com/Utsav-X-bit/skein/internal/firewall"
	"github.com/Utsav-X-bit/skein/internal/service"
)

// recorder stands in for the host: every side effect is recorded, none runs.
type recorder struct {
	exec           []string
	handlers       map[string]func(args []string) (stdout, stderr string, err error)
	stopped        bool
	serviceRemoved bool
	firewall       *firewall.Config
	stopErr        error
	removeSvcErr   error
	firewallErr    error
}

func (r *recorder) system() system {
	return system{
		exec: func(_ context.Context, name string, args ...string) (string, string, error) {
			r.exec = append(r.exec, name+" "+strings.Join(args, " "))
			if fn, ok := r.handlers[name]; ok {
				return fn(args)
			}
			if name == "apparmor_parser" {
				return "", "", nil
			}
			return "", "", exec.ErrNotFound
		},
		euid:   func() int { return 0 },
		exe:    func() (string, error) { return "", errors.New("no executable") },
		verify: func(string) error { return nil },
		status: func() (service.Status, error) { return service.Status{State: service.StateMissing}, nil },
		stop: func() error {
			r.stopped = true
			return r.stopErr
		},
		uninstallService: func() error {
			r.serviceRemoved = true
			return r.removeSvcErr
		},
		removeFirewall: func(cfg firewall.Config) error {
			r.firewall = &cfg
			return r.firewallErr
		},
		removeGroup: func(context.Context) error { return nil },
		checkProcesses: func(string) error { return nil },
		loadConfig: func(string) (conf.Config, error) { return conf.Config{}, fs.ErrNotExist },
	}
}

func withHost(t *testing.T, s system) {
	t.Helper()
	old := host
	host = s
	t.Cleanup(func() { host = old })
}

// testLayout is an installation tree under a temp dir: nothing outside it can
// be touched by a test.
func testLayout(t *testing.T) layout {
	t.Helper()
	root := t.TempDir()
	p := layout{
		cli:      filepath.Join(root, "usr/local/bin/skein"),
		daemon:   filepath.Join(root, "usr/local/bin/skeind"),
		desktop:  filepath.Join(root, "usr/local/bin/skein-desktop"),
		helper:   filepath.Join(root, "usr/libexec/skein-helper"),
		apparmor: filepath.Join(root, "etc/apparmor.d/skein-desktop"),
		integration: []string{
			filepath.Join(root, "usr/share/polkit-1/actions/org.skein.helper.policy"),
			filepath.Join(root, "usr/share/applications/skein-desktop.desktop"),
		},
		configDir:  filepath.Join(root, "etc/skein"),
		stateDir:   filepath.Join(root, "var/lib/skein"),
		runDir:     filepath.Join(root, "run/skein"),
		configPath: filepath.Join(root, "etc/skein/skein.toml"),
		userHome:   filepath.Join(root, "home/user"),
		userData:   []string{filepath.Join(root, "home/user/.config/skein-desktop")},
	}
	for _, f := range p.files() {
		writeTestFile(t, f)
	}
	for _, d := range append([]string{p.configDir, p.stateDir, p.runDir}, p.userData...) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestRemoveStopsServiceBeforeDeleting(t *testing.T) {
	p := testLayout(t)
	r := &recorder{}
	s := r.system()
	running := true
	s.status = func() (service.Status, error) {
		if running {
			return service.Status{State: service.StateRunning}, nil
		}
		return service.Status{State: service.StateStopped}, nil
	}
	s.stop = func() error {
		if !exists(p.daemon) {
			t.Fatal("daemon binary deleted before the service was stopped")
		}
		running = false
		r.stopped = true
		return nil
	}

	if _, err := remove(context.Background(), p, s); err != nil {
		t.Fatal(err)
	}
	if !r.stopped || !r.serviceRemoved {
		t.Fatalf("service not stopped/removed: stopped=%v removed=%v", r.stopped, r.serviceRemoved)
	}
	if exists(p.daemon) || exists(p.cli) {
		t.Fatal("binaries survived")
	}
}

func TestRemoveAbortsWhenStopFails(t *testing.T) {
	p := testLayout(t)
	r := &recorder{stopErr: errors.New("boom")}
	s := r.system()
	s.status = func() (service.Status, error) { return service.Status{State: service.StateRunning}, nil }

	_, err := remove(context.Background(), p, s)
	if err == nil || !strings.Contains(err.Error(), "could not be stopped") {
		t.Fatalf("err = %v", err)
	}
	if !exists(p.cli) || !exists(p.configDir) {
		t.Fatal("files deleted despite a failed service stop")
	}
}

func TestRemoveAbortsWhenServiceStillRunning(t *testing.T) {
	p := testLayout(t)
	r := &recorder{}
	s := r.system()
	s.status = func() (service.Status, error) { return service.Status{State: service.StateRunning}, nil }

	_, err := remove(context.Background(), p, s)
	if err == nil || !strings.Contains(err.Error(), "running after stop") {
		t.Fatalf("err = %v", err)
	}
	if !exists(p.cli) {
		t.Fatal("files deleted while the service still runs")
	}
}

func TestRemoveDeletesOnlySkeinPaths(t *testing.T) {
	p := testLayout(t)
	r := &recorder{}
	root := filepath.Dir(filepath.Dir(p.configDir))
	source := filepath.Join(root, "src/checkout/main.go")
	writeTestFile(t, source)
	userNotes := filepath.Join(root, "home/user/notes.txt")
	writeTestFile(t, userNotes)

	msg, err := remove(context.Background(), p, r.system())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Skein was removed") {
		t.Fatalf("message = %q", msg)
	}
	paths := append(p.files(), p.configDir, p.stateDir, p.runDir)
	paths = append(paths, p.userData...)
	for _, path := range paths {
		if exists(path) {
			t.Errorf("%s was not removed", path)
		}
	}
	if !exists(source) {
		t.Fatal("a source tree outside the install was deleted")
	}
	if !exists(userNotes) {
		t.Fatal("unrelated user data was deleted")
	}
}

func TestRemoveRefusesUserDataSymlinkEscape(t *testing.T) {
	p := testLayout(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "share", "skein-desktop", "keep")
	writeTestFile(t, target)
	if err := os.Symlink(outside, filepath.Join(p.userHome, ".local")); err != nil {
		t.Fatal(err)
	}
	p.userData = []string{filepath.Join(p.userHome, ".local", "share", "skein-desktop")}
	if _, err := remove(context.Background(), p, (&recorder{}).system()); err == nil {
		t.Fatal("uninstall reported success after a user-data symlink escaped the home")
	}
	if !exists(target) {
		t.Fatal("uninstall deleted data outside the user's home")
	}
}

func TestRemoveFirewallUsesConfig(t *testing.T) {
	p := testLayout(t)
	r := &recorder{}
	s := r.system()
	s.loadConfig = func(path string) (conf.Config, error) {
		if path != p.configPath {
			t.Fatalf("loaded %q, want %q", path, p.configPath)
		}
		return conf.Config{Node: conf.Node{Interface: "skein0", ListenPort: 11010}}, nil
	}

	msg, err := remove(context.Background(), p, s)
	if err != nil {
		t.Fatal(err)
	}
	if r.firewall == nil || r.firewall.Interface != "skein0" || r.firewall.Port != 11010 {
		t.Fatalf("firewall = %+v", r.firewall)
	}
	if !strings.Contains(msg, "firewall rules") {
		t.Fatalf("message = %q", msg)
	}
}

func TestRemoveReportsFirewallError(t *testing.T) {
	p := testLayout(t)
	r := &recorder{firewallErr: errors.New("nope")}
	s := r.system()
	s.loadConfig = func(string) (conf.Config, error) {
		return conf.Config{Node: conf.Node{Interface: "skein0", ListenPort: 11010}}, nil
	}

	_, err := remove(context.Background(), p, s)
	if err == nil || !strings.Contains(err.Error(), "firewall rules were not removed") || !exists(p.configDir) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveSkipsFirewallWithoutNetworkConfig(t *testing.T) {
	p := testLayout(t)
	r := &recorder{}
	s := r.system()
	s.loadConfig = func(string) (conf.Config, error) { return conf.Config{}, nil }

	if _, err := remove(context.Background(), p, s); err != nil {
		t.Fatal(err)
	}
	if r.firewall != nil {
		t.Fatalf("firewall touched without a network config: %+v", r.firewall)
	}
}

func TestRemovePackageManaged(t *testing.T) {
	p := testLayout(t)
	r := &recorder{}
	r.handlers = map[string]func([]string) (string, string, error){
		"dpkg-query": func(args []string) (string, string, error) {
			if strings.HasSuffix(args[len(args)-1], "/skein") {
				return "skein-desktop: /usr/local/bin/skein", "", nil
			}
			return "", "dpkg-query: no path found matching pattern " + args[len(args)-1], errors.New("exit status 1")
		},
		"apt-get": func([]string) (string, string, error) { return "", "", nil },
	}

	msg, err := remove(context.Background(), p, r.system())
	if err != nil {
		t.Fatal(err)
	}
	want := "apt-get remove -y -- skein-desktop"
	found := false
	for _, c := range r.exec {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("calls = %q, want %q", r.exec, want)
	}
	if !strings.Contains(msg, "the skein-desktop package") {
		t.Fatalf("message = %q", msg)
	}
	if exists(p.cli) {
		t.Fatal("file survived")
	}
}

func TestRemoveRefusesUnrelatedPackage(t *testing.T) {
	p := testLayout(t)
	r := &recorder{handlers: map[string]func([]string) (string, string, error){
		"dpkg-query": func(args []string) (string, string, error) {
			if args[len(args)-1] == p.cli {
				return "skein-desktop: " + p.cli, "", nil
			}
			return "unrelated-package: " + args[len(args)-1], "", nil
		},
	}}
	if _, err := remove(context.Background(), p, r.system()); err == nil {
		t.Fatal("accepted a file owned by another package")
	}
	if !exists(p.cli) || !exists(p.daemon) || r.stopped {
		t.Fatal("modified the installation before rejecting package ownership")
	}
}

func TestRemovePackageFailureKeepsFiles(t *testing.T) {
	p := testLayout(t)
	r := &recorder{}
	r.handlers = map[string]func([]string) (string, string, error){
		"dpkg-query": func(args []string) (string, string, error) {
			if strings.HasSuffix(args[len(args)-1], "/skein") {
				return "skein-desktop: /usr/local/bin/skein", "", nil
			}
			return "", "no path found matching pattern", errors.New("exit status 1")
		},
		"apt-get": func([]string) (string, string, error) {
			return "", "E: dpkg was interrupted", errors.New("exit status 100")
		},
	}

	_, err := remove(context.Background(), p, r.system())
	if err == nil || !strings.Contains(err.Error(), "configuration was kept") {
		t.Fatalf("err = %v", err)
	}
	if !exists(p.cli) || !exists(p.configDir) {
		t.Fatal("files deleted after a failed package removal")
	}
}

func TestPackageOwnershipIndeterminate(t *testing.T) {
	p := testLayout(t)
	r := &recorder{}
	r.handlers = map[string]func([]string) (string, string, error){
		"dpkg-query": func([]string) (string, string, error) {
			return "", "dpkg-query: error: database is locked", errors.New("exit status 2")
		},
	}

	_, err := remove(context.Background(), p, r.system())
	if err == nil || !strings.Contains(err.Error(), "cannot tell whether") {
		t.Fatalf("err = %v", err)
	}
	if !exists(p.cli) {
		t.Fatal("files deleted without knowing package ownership")
	}
	if r.stopped {
		t.Fatal("service touched before ownership was settled")
	}
}

func TestRemoveNothingInstalled(t *testing.T) {
	root := t.TempDir()
	p := layout{
		cli:        filepath.Join(root, "bin/skein"),
		daemon:     filepath.Join(root, "bin/skeind"),
		desktop:    filepath.Join(root, "bin/skein-desktop"),
		helper:     filepath.Join(root, "libexec/skein-helper"),
		apparmor:   filepath.Join(root, "apparmor/skein-desktop"),
		configDir:  filepath.Join(root, "etc/skein"),
		stateDir:   filepath.Join(root, "var/lib/skein"),
		runDir:     filepath.Join(root, "run/skein"),
		configPath: filepath.Join(root, "etc/skein/skein.toml"),
	}
	msg, err := remove(context.Background(), p, (&recorder{}).system())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "not installed") {
		t.Fatalf("message = %q", msg)
	}
}

func TestUserDataDirs(t *testing.T) {
	home := t.TempDir()
	dirs, err := userDataDirs(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(home, ".config", "skein"),
		filepath.Join(home, ".config", "skein-desktop"),
		filepath.Join(home, ".local", "share", "skein-desktop"),
		filepath.Join(home, ".cache", "skein-desktop"),
	}
	if len(dirs) != len(want) {
		t.Fatalf("dirs = %q, want %q", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Fatalf("dirs = %q, want %q", dirs, want)
		}
	}
	for _, bad := range []string{"", "relative/home", "/"} {
		if _, err := userDataDirs(bad); err == nil {
			t.Errorf("userDataDirs(%q) accepted an unsafe home", bad)
		}
	}

	outside := t.TempDir()
	home2 := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home2, ".config")); err != nil {
		t.Fatal(err)
	}
	if _, err := userDataDirs(home2); err == nil {
		t.Fatal("accepted a .config symlink pointing outside home")
	}

	home3 := t.TempDir()
	inside := filepath.Join(home3, "xdg")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(home3, ".config")); err != nil {
		t.Fatal(err)
	}
	if _, err := userDataDirs(home3); err != nil {
		t.Fatalf("rejected a .config symlink inside home: %v", err)
	}
}

func TestElevateRefusesUnverifiedCLI(t *testing.T) {
	r := &recorder{}
	s := r.system()
	s.euid = func() int { return 1000 }
	s.exe = func() (string, error) { return "/tmp/skein", nil }
	withHost(t, s)

	_, err := Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sudo /tmp/skein uninstall --yes") {
		t.Fatalf("err = %v", err)
	}
	if len(r.exec) != 0 {
		t.Fatalf("elevated from a user-writable directory: %q", r.exec)
	}
}

func TestElevateFallsBackToPkexec(t *testing.T) {
	r := &recorder{}
	r.handlers = map[string]func([]string) (string, string, error){
		"sudo": func([]string) (string, string, error) {
			return "", "sudo: a password is required", errors.New("exit status 1")
		},
		"pkexec": func([]string) (string, string, error) {
			return "Skein was removed: the service.", "", nil
		},
	}
	s := r.system()
	s.euid = func() int { return 1000 }
	s.exe = func() (string, error) { return "/usr/local/bin/skein", nil }
	withHost(t, s)

	msg, err := Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if msg != "Skein was removed: the service." {
		t.Fatalf("message = %q", msg)
	}
	want := []string{
		"sudo -n -- /usr/local/bin/skein uninstall --yes",
		"pkexec /usr/local/bin/skein uninstall --yes",
	}
	if len(r.exec) != 2 || r.exec[0] != want[0] || r.exec[1] != want[1] {
		t.Fatalf("calls = %q, want %q", r.exec, want)
	}
}

func TestElevateDoesNotRetryChildFailure(t *testing.T) {
	r := &recorder{}
	r.handlers = map[string]func([]string) (string, string, error){
		"sudo": func([]string) (string, string, error) {
			return "", "uninstall incomplete: firewall rules were not removed", errors.New("exit status 1")
		},
	}
	s := r.system()
	s.euid = func() int { return 1000 }
	s.exe = func() (string, error) { return "/usr/local/bin/skein", nil }
	withHost(t, s)

	_, err := Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "firewall rules") {
		t.Fatalf("err = %v", err)
	}
	if len(r.exec) != 1 {
		t.Fatalf("re-ran a failed uninstall: %q", r.exec)
	}
}
