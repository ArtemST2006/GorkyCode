// Package reminder keeps reminder notifications: it derives the upcoming
// moments from the tasks table, runs the central dispatch loop and fans
// every fired notification out to the connected consumers.
package reminder

import (
	"context"
	"fmt"
	"gorkycode/backend/internal/model"
	"sync"
)

// Bus fans a published notification out to every current in-process
// subscriber: the WebSocket hub and the email worker. Publishing never
// blocks; a slow subscriber gets the next delivery when the state is
// requested again (the hub) or after the expiry of the scheduler claim.
type Bus struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
	buf  int
}

type subscriber struct {
	ch chan model.Notification
}

// NewBus creates a fan-out bus with a per-subscriber buffer of size buf.
func NewBus(buf int) *Bus {
	if buf <= 0 {
		buf = 128
	}
	return &Bus{subs: map[*subscriber]struct{}{}, buf: buf}
}

// Publisher is what the scheduler uses to deliver fired notifications.
type Publisher interface {
	Publish(ctx context.Context, n model.Notification) error
}

// Publish forwards the notification to every subscriber without blocking.
// An error is returned only when at least one subscriber queue overflowed:
// the message stays reachable because it remains pending in storage.
func (b *Bus) Publish(_ context.Context, n model.Notification) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs {
		select {
		case sub.ch <- n:
		default:
			return fmt.Errorf("subscriber queue is full: notification %s dropped", n.ID)
		}
	}
	return nil
}

// Subscribe registers a new buffered channel; the returned function removes
// it again. The channel is closed at most once and only after the bus lock
// has excluded concurrent publishes from the current subscriber.
func (b *Bus) Subscribe() (<-chan model.Notification, func()) {
	sub := &subscriber{ch: make(chan model.Notification, b.buf)}

	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()

	return sub.ch, func() {
		b.mu.Lock()
		_, kept := b.subs[sub]
		delete(b.subs, sub)
		b.mu.Unlock()
		if kept {
			close(sub.ch)
		}
	}
}
