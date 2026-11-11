// Package events is a small in-process publish/subscribe bus. The web UI
// receives events over Server-Sent Events.
package events

import "sync"

// Event is something that happened.
type Event struct {
	Type string `json:"type"` // e.g. "downloads"
	Data any    `json:"data"`
}

// Bus fans events out to subscribers. Slow subscribers miss events rather
// than block publishers.
type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// New returns an empty bus.
func New() *Bus { return &Bus{subs: map[chan Event]struct{}{}} }

// Subscribe returns a channel of events and a function to stop.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
}

// Publish sends e to every subscriber that has room for it.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribers returns how many are listening (to skip work when none are).
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
