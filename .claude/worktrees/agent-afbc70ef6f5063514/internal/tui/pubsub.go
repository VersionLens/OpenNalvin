package tui

import (
	"context"
	"sync"
)

// EventType describes the kind of mutation that occurred.
type EventType int

const (
	Created EventType = iota
	Updated
	Deleted
)

// Event carries a typed payload and the kind of mutation. It satisfies tea.Msg.
type Event[T any] struct {
	Type    EventType
	Payload T
}

// Broker fans out events to all active subscribers via buffered channels.
// Publish is non-blocking: slow subscribers have events dropped.
type Broker[T any] struct {
	mu   sync.RWMutex
	subs map[*chan Event[T]]struct{}
}

// NewBroker creates a ready-to-use broker.
func NewBroker[T any]() *Broker[T] {
	return &Broker[T]{subs: make(map[*chan Event[T]]struct{})}
}

// Subscribe returns a buffered channel that receives published events.
// The subscription is automatically cleaned up when ctx is cancelled.
func (b *Broker[T]) Subscribe(ctx context.Context) <-chan Event[T] {
	ch := make(chan Event[T], 64)
	ptr := &ch

	b.mu.Lock()
	b.subs[ptr] = struct{}{}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs, ptr)
		b.mu.Unlock()
		// Drain remaining events so senders don't block.
		for range ch {
		}
	}()

	return ch
}

// Publish sends an event to all active subscribers. If a subscriber's buffer
// is full the event is dropped (non-blocking).
func (b *Broker[T]) Publish(evt Event[T]) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ptr := range b.subs {
		select {
		case *ptr <- evt:
		default:
		}
	}
}
