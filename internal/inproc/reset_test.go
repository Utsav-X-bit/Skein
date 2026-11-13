package inproc_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Utsav-X-bit/skein/internal/inproc"
)

func TestResetReleasesOldDialsAndServesNewGeneration(t *testing.T) {
	inproc.Reset()
	t.Cleanup(func() { _ = inproc.Current().Close() })
	old := inproc.Current()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pending := make(chan error, 1)
	go func() {
		c, err := old.Dial(ctx)
		if c != nil {
			c.Close()
		}
		pending <- err
	}()
	inproc.Reset()
	if err := <-pending; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("old dial = %v, want listener closed, not a request timeout", err)
	}
	accepted := make(chan error, 1)
	go func() {
		c, err := inproc.Current().Accept()
		if err == nil {
			defer c.Close()
			_, err = c.Write([]byte("new generation"))
		}
		accepted <- err
	}()
	c, err := inproc.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	raw, err := io.ReadAll(c)
	if err != nil || string(raw) != "new generation" {
		t.Fatalf("new listener response = %q, %v", raw, err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentListenerResetAndDial(t *testing.T) {
	inproc.Reset()
	t.Cleanup(func() { _ = inproc.Current().Close() })
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for range 10 {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if c, err := inproc.Dial(ctx); err == nil {
					c.Close()
					t.Error("cancelled request unexpectedly connected without a server")
				}
				inproc.Reset()
			}
		})
	}
	group.Wait()
}
