package services

import (
	"sync"
	"testing"
)

func TestConcurrentControlClientsShareTransport(t *testing.T) {
	old := defaultClient
	defaultClient = nil
	t.Cleanup(func() { defaultClient = old })
	const count = 32
	clients := make(chan *Client, count)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			<-start
			c, err := ControlClient()
			if err != nil {
				t.Errorf("ControlClient: %v", err)
			}
			clients <- c
		})
	}
	close(start)
	group.Wait()
	close(clients)
	var first *Client
	for c := range clients {
		if c == nil {
			t.Fatal("ControlClient returned no client")
		}
		if first == nil {
			first = c
		} else if c != first {
			t.Fatal("concurrent callers received different control transports")
		}
	}
}
