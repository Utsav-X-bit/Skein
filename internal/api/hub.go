package api

import (
	"encoding/json"
	"sync"
)

const (
	// HubReplaySize is how many recent events are kept for Last-Event-ID replay.
	HubReplaySize = 1024
	// HubQueueSize is the per-subscriber buffer; a subscriber that cannot keep
	// up is disconnected rather than stalling the broadcaster.
	HubQueueSize = 256
)

// hubEvent is one broadcast event with its assigned monotonic ID.
type hubEvent struct {
	ID   uint64
	Type string
	Data json.RawMessage
}

type hubSub struct {
	q chan hubEvent
}

// Hub fans events out to SSE subscribers. Every subscriber sees every event;
// IDs are monotonic, a bounded ring serves Last-Event-ID replay, and slow
// consumers whose queue backs up are disconnected.
type Hub struct {
	mu     sync.Mutex
	nextID uint64
	ring   []hubEvent
	subs   map[int]*hubSub
	subSeq int
}

// NewHub creates an empty broadcast hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]*hubSub)}
}

// Publish assigns the next ID, retains the event for replay, and fans it out.
func (h *Hub) Publish(typ string, data any) uint64 {
	raw, err := json.Marshal(data)
	if err != nil {
		raw = json.RawMessage("null")
	}
	h.mu.Lock()
	h.nextID++
	ev := hubEvent{ID: h.nextID, Type: typ, Data: raw}
	h.ring = append(h.ring, ev)
	if len(h.ring) > HubReplaySize {
		h.ring = h.ring[len(h.ring)-HubReplaySize:]
	}
	for id, sub := range h.subs {
		select {
		case sub.q <- ev:
		default:
			delete(h.subs, id)
			close(sub.q)
		}
	}
	h.mu.Unlock()
	return h.nextID
}

// Subscribe replays events after lastID from the ring, then streams live
// events. The returned cancel must be called; the channel closes when the
// subscriber is dropped as a slow consumer.
func (h *Hub) Subscribe(lastID uint64) (<-chan hubEvent, func()) {
	h.mu.Lock()
	sub := &hubSub{q: make(chan hubEvent, HubQueueSize)}
	h.subSeq++
	id := h.subSeq
	h.subs[id] = sub
	for _, ev := range h.ring {
		if ev.ID <= lastID {
			continue
		}
		select {
		case sub.q <- ev:
		default:
			// The ring alone overflows the queue: the subscriber is already
			// slow, so it gets only what fits.
			delete(h.subs, id)
			h.mu.Unlock()
			close(sub.q)
			return sub.q, func() {}
		}
	}
	h.mu.Unlock()
	return sub.q, func() {
		h.mu.Lock()
		if _, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(sub.q)
		}
		h.mu.Unlock()
	}
}
