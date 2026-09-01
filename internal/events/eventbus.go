// Package events provides an in-memory pub/sub event bus for domain events.
// Publish is non-blocking with a drop-on-full policy (recommended for
// trading systems where the hot path must not stall).
package events

import (
	"sync"
	"time"
)

// EventType categorises domain events.
type EventType string

const (
	EventOrderPlaced    EventType = "order.placed"
	EventOrderCancelled EventType = "order.cancelled"
	EventTradeExecuted  EventType = "trade.executed"
	EventMarketSettled  EventType = "market.settled"
)

// Event is one domain event.
type Event struct {
	Type      EventType
	Timestamp time.Time
	Payload   any
}

// EventBus is a thread-safe, in-memory pub/sub bus with a drop-on-full
// policy: a slow subscriber never blocks a publisher.
//
// Lifecycle: call Close once when shutting down. Close closes all
// subscriber channels so consumers ranging over them exit cleanly.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[EventType][]chan Event
	bufferSize  int
	closed      bool
}

// NewEventBus creates an event bus with the given per-subscriber buffer.
func NewEventBus(bufferSize int) *EventBus {
	return &EventBus{
		subscribers: make(map[EventType][]chan Event),
		bufferSize:  bufferSize,
	}
}

// Subscribe creates a new buffered channel for the given event type and
// returns it. The caller must consume from the channel. When done, call
// Unsubscribe (which closes the channel) or Close the whole bus.
//
// Subscribe returns nil after Close has been called.
func (eb *EventBus) Subscribe(eventType EventType) <-chan Event {
	ch := make(chan Event, eb.bufferSize)

	eb.mu.Lock()
	defer eb.mu.Unlock()

	if eb.closed {
		close(ch)
		return ch
	}

	eb.subscribers[eventType] = append(eb.subscribers[eventType], ch)
	return ch
}

// Unsubscribe removes the channel from the subscriber list and closes it
// so the consumer's range loop exits. The channel is closed by the bus
// (the sender), not by the caller.
func (eb *EventBus) Unsubscribe(eventType EventType, ch <-chan Event) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	subs := eb.subscribers[eventType]
	for i, s := range subs {
		if s == ch {
			eb.subscribers[eventType] = append(subs[:i], subs[i+1:]...)
			close(s) // sender-side close — consumer's range exits
			return
		}
	}
}

// Publish sends e to every subscriber of e.Type. If a subscriber's
// buffer is full, the event is dropped for that subscriber (non-blocking).
//
// Publish is a no-op after Close. It never blocks and never panics.
func (eb *EventBus) Publish(e Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	if eb.closed {
		return
	}

	for _, ch := range eb.subscribers[e.Type] {
		select {
		case ch <- e:
		default:
			// Buffer full — drop (trading: drop preferred over blocking).
		}
	}
}

// Close closes all subscriber channels and marks the bus as closed.
// After Close, Publish is a no-op and Subscribe returns a pre-closed
// channel. Calling Close more than once panics.
func (eb *EventBus) Close() {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	for eventType, subs := range eb.subscribers {
		for _, ch := range subs {
			close(ch)
		}
		delete(eb.subscribers, eventType)
	}
	eb.closed = true
}

// SubscriberCount returns the number of subscribers for a given event type.
func (eb *EventBus) SubscriberCount(eventType EventType) int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.subscribers[eventType])
}
