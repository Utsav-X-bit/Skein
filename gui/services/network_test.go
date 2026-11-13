package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// stubPingCommand replaces the command factory for one test and restores it.
func stubPingCommand(t *testing.T, factory func(context.Context, string, ...string) *exec.Cmd) {
	t.Helper()
	old := newPingCommand
	newPingCommand = factory
	t.Cleanup(func() { newPingCommand = old })
}

// pingHelperCommand runs this test binary as a fake ping process.
func pingHelperCommand(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestPingHelperProcess")
	cmd.Env = append(os.Environ(), "SKEIN_PING_HELPER="+mode)
	return cmd
}

// TestPingHelperProcess is the fake ping binary. It only acts when the
// parent test sets SKEIN_PING_HELPER, so a normal run skips it.
func TestPingHelperProcess(t *testing.T) {
	mode := os.Getenv("SKEIN_PING_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		fmt.Print("64 bytes from 10.144.144.2: icmp_seq=1 ttl=64 time=0.1 ms")
	case "fail":
		fmt.Print("Request timeout for icmp_seq 0")
		os.Exit(2)
	case "flood":
		chunk := strings.Repeat("x", 1024)
		for range 64 {
			fmt.Print(chunk)
		}
	case "spawn-error":
		fmt.Fprint(os.Stderr, "ping: socket: operation not permitted")
		os.Exit(1)
	}
	os.Exit(0)
}

func TestPingRejectsNonAddresses(t *testing.T) {
	called := false
	stubPingCommand(t, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		called = true
		return pingHelperCommand(ctx, "success")
	})
	for _, in := range []string{"", "example.com", "-c", "10.0.0.1 -c 100", "999.0.0.1", "10.0.0.1; id"} {
		if _, err := (&NetworkService{}).Ping(context.Background(), in); err == nil {
			t.Fatalf("Ping(%q) = nil, want validation error", in)
		}
	}
	if called {
		t.Fatal("invalid input reached the command factory")
	}
}

func TestPingBuildsFixedArgvAndReportsReachable(t *testing.T) {
	var name string
	var args []string
	stubPingCommand(t, func(ctx context.Context, n string, a ...string) *exec.Cmd {
		name, args = n, slices.Clone(a)
		return pingHelperCommand(ctx, "success")
	})
	res, err := (&NetworkService{}).Ping(context.Background(), "10.144.144.2")
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	wantName := "ping"
	if runtime.GOOS == "android" {
		wantName = "/system/bin/ping"
	}
	if name != wantName {
		t.Fatalf("command = %q, want %q", name, wantName)
	}
	flag := "-c"
	if runtime.GOOS == "windows" {
		flag = "-n"
	}
	want := []string{flag, "4", "10.144.144.2"}
	if !slices.Equal(args, want) {
		t.Fatalf("argv = %v, want %v", args, want)
	}
	if !res.Reachable || res.ExitCode != 0 || res.Address != "10.144.144.2" {
		t.Fatalf("result = %+v, want reachable", res)
	}
	if !strings.Contains(res.Output, "64 bytes") {
		t.Fatalf("output = %q", res.Output)
	}
}

func TestPingNonZeroExitIsAResult(t *testing.T) {
	stubPingCommand(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return pingHelperCommand(ctx, "fail")
	})
	res, err := (&NetworkService{}).Ping(context.Background(), "192.0.2.7")
	if err != nil {
		t.Fatalf("non-zero ping exit returned an error: %v", err)
	}
	if res.Reachable || res.ExitCode == 0 {
		t.Fatalf("result = %+v, want unreachable with a non-zero exit", res)
	}
	if !strings.Contains(res.Output, "timeout") {
		t.Fatalf("output = %q, want ping failure text", res.Output)
	}
}

func TestPingOutputIsCapped(t *testing.T) {
	stubPingCommand(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return pingHelperCommand(ctx, "flood")
	})
	res, err := (&NetworkService{}).Ping(context.Background(), "192.0.2.8")
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if len(res.Output) != pingOutputLimit {
		t.Fatalf("output length = %d, want %d", len(res.Output), pingOutputLimit)
	}
}

func TestPingSpawnFailureIsAnError(t *testing.T) {
	stubPingCommand(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "skein-ping-binary-that-does-not-exist")
	})
	_, err := (&NetworkService{}).Ping(context.Background(), "192.0.2.9")
	if err == nil {
		t.Fatal("spawn failure = nil, want error")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("spawn failure misreported as a deadline: %v", err)
	}
}
