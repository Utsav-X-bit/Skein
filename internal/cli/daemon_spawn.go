package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// reachable reports whether a daemon answers on the local API.
func (c *client) reachable(ctx context.Context) bool {
	var st statusDTO
	return c.get(ctx, "/api/v1/status", &st) == nil
}

// daemonLogPath is where a dashboard-started daemon writes its output.
func daemonLogPath(configPath string) string {
	return configPath + ".log"
}

// spawnDaemon starts `skein run` with the given configuration.
func spawnDaemon(configPath string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logPath := daemonLogPath(configPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}
	cmd := exec.Command(self, "run", "-c", configPath)
	cmd.Stdout = f
	cmd.Stderr = f
	setParentDeath(cmd)
	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	// The child holds its own descriptor; the parent must not keep this one.
	_ = f.Close()
	return cmd, nil
}

// ensureDaemon attaches to a running daemon or starts one. The caller owns the
// child and must call closeChild.
func (d *dashboard) ensureDaemon(ctx context.Context, configPath string) error {
	d.mu.Lock()
	d.configPath = configPath
	d.mu.Unlock()
	probe, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	ok := d.client.reachable(probe)
	cancel()
	if ok {
		return nil
	}
	if !elevated() {
		return fmt.Errorf("the daemon is not running and starting it needs %s:\n%s",
			privilegeName(), elevationHint(configPath))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	stopping := d.stopping
	d.mu.Unlock()
	if stopping {
		return nil
	}

	d.mu.Lock()
	child, childDone := d.child, d.childDone
	d.mu.Unlock()
	if child != nil {
		select {
		case <-childDone:
			// The child we started has exited; fall through and start another.
			d.mu.Lock()
			if d.child == child {
				d.child, d.childDone = nil, nil
			}
			d.mu.Unlock()
		default:
			return nil
		}
	}

	cmd, err := spawnDaemon(configPath)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	d.mu.Lock()
	if d.stopping {
		d.mu.Unlock()
		terminate(cmd)
		return nil
	}
	d.child, d.childDone = cmd, done
	d.mu.Unlock()
	go func() { done <- cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			d.mu.Lock()
			if d.child == cmd {
				d.child, d.childDone = nil, nil
			}
			d.mu.Unlock()
			if err != nil {
				return fmt.Errorf("daemon exited during startup (%v); see %s", err, daemonLogPath(configPath))
			}
			return errors.New("daemon exited during startup; see " + daemonLogPath(configPath))
		default:
		}
		probe, cancel := context.WithTimeout(ctx, time.Second)
		ok := d.client.reachable(probe)
		cancel()
		if ok {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	d.mu.Lock()
	if d.child == cmd {
		d.child, d.childDone = nil, nil
	}
	d.mu.Unlock()
	terminate(cmd)
	return fmt.Errorf("daemon did not become ready; see %s", daemonLogPath(configPath))
}

// closeChild stops a daemon this dashboard started. A daemon that was already
// running is left alone.
func (d *dashboard) closeChild() {
	d.mu.Lock()
	d.stopping = true
	child, done := d.child, d.childDone
	d.mu.Unlock()
	if child == nil || child.Process == nil {
		return
	}
	terminate(child)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = child.Process.Kill()
	}
	d.mu.Lock()
	if d.child == child {
		d.child, d.childDone = nil, nil
	}
	d.mu.Unlock()
}
