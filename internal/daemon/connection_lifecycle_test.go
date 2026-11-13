package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTerminalEngineErrorReachesTeardown(t *testing.T) {
	want := errors.New("TUN read failed")
	engineDone := make(chan error, 1)
	engineDone <- want
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlStopped := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- waitDataPlane(ctx, make(chan struct{}), engineDone, cancel, func() { close(controlStopped) })
	}()
	select {
	case got := <-result:
		if !errors.Is(got, want) {
			t.Fatalf("terminal result = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal engine failure hung before teardown")
	}
	select {
	case <-controlStopped:
	default:
		t.Fatal("control loop still running after engine failure")
	}
}

func TestPauseCancelsAndJoinsEngine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pause := make(chan struct{})
	close(pause)
	done := make(chan error)
	go func() {
		<-ctx.Done()
		done <- nil
	}()
	if err := waitDataPlane(context.Background(), pause, done, cancel, func() {}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectBeforeRunHonorsCancellation(t *testing.T) {
	d := &Daemon{conn: &conn{state: ConnPaused, ready: make(chan struct{})}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Connect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect before generation ready = %v, want cancellation", err)
	}
	if got := d.Connection().State; got != ConnPaused {
		t.Fatalf("cancelled Connect changed state to %q", got)
	}
}

func TestImmediateReconnectReusesGenerationSafely(t *testing.T) {
	signal := make(chan struct{}, 1)
	d := &Daemon{conn: &conn{state: ConnPaused, resumeCh: signal}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next := make(chan struct{})
	go func() {
		for range 2 {
			select {
			case <-ctx.Done():
				return
			case <-signal:
				d.conn.set(ConnConnected, "", nil)
			}
			select {
			case <-ctx.Done():
				return
			case <-next:
			}
		}
	}()
	for range 2 {
		st, err := d.Connect(ctx)
		if err != nil || st.State != ConnConnected {
			t.Fatalf("reconnect = %+v, %v", st, err)
		}
		// A pause settles before connLoop necessarily installs a new generation.
		d.conn.set(ConnPaused, "user", nil)
		select {
		case next <- struct{}{}:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
