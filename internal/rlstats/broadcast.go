package rlstats

import "sync"

// Broadcaster hands each event it's given to every current subscriber. It
// lets a window opened mid-session listen to the same Stats API connection
// as the rest of the app, rather than opening a second one.
type Broadcaster struct {
	mu   sync.Mutex
	next int
	subs map[int]func(Event)
}

// Subscribe registers f and returns a func that unregisters it. f is called
// from the Stats API goroutine.
func (b *Broadcaster) Subscribe(f func(Event)) (unsubscribe func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[int]func(Event){}
	}
	id := b.next
	b.next++
	b.subs[id] = f
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}

// Publish sends e to every subscriber.
func (b *Broadcaster) Publish(e Event) {
	b.mu.Lock()
	subs := make([]func(Event), 0, len(b.subs))
	for _, f := range b.subs {
		subs = append(subs, f)
	}
	b.mu.Unlock()
	for _, f := range subs {
		f(e)
	}
}
