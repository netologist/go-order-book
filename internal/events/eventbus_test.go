package events

import (
	"sync"
	"testing"
	"time"
)

func TestSubscribeThenPublish(t *testing.T) {
	eb := NewEventBus(10)
	defer eb.Close()
	ch := eb.Subscribe(EventTradeExecuted)

	eb.Publish(Event{
		Type:      EventTradeExecuted,
		Timestamp: time.Now(),
		Payload:   "trade-1",
	})

	select {
	case e := <-ch:
		if e.Type != EventTradeExecuted {
			t.Errorf("got type %s, want %s", e.Type, EventTradeExecuted)
		}
		if p, ok := e.Payload.(string); !ok || p != "trade-1" {
			t.Errorf("got payload %v, want trade-1", e.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestAllSubscribersReceive(t *testing.T) {
	eb := NewEventBus(10)
	defer eb.Close()
	const N = 100

	chans := make([]<-chan Event, N)
	for i := range N {
		chans[i] = eb.Subscribe(EventOrderPlaced)
	}

	eb.Publish(Event{
		Type:      EventOrderPlaced,
		Timestamp: time.Now(),
		Payload:   "bulk",
	})

	for i, ch := range chans {
		select {
		case <-ch:
			// received — good
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive event", i)
		}
	}
}

func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	eb := NewEventBus(1) // tiny buffer
	defer eb.Close()

	_ = eb.Subscribe(EventTradeExecuted)

	// Fill the buffer, then publish more — publisher should not block.
	for i := 0; i < 100; i++ {
		eb.Publish(Event{
			Type:      EventTradeExecuted,
			Timestamp: time.Now(),
			Payload:   i,
		})
	}
	// If we got here, the publisher didn't block.
}

func TestUnsubscribeRemovesAndClosesChannel(t *testing.T) {
	eb := NewEventBus(10)
	defer eb.Close()
	ch := eb.Subscribe(EventOrderPlaced)

	eb.Unsubscribe(EventOrderPlaced, ch)

	if n := eb.SubscriberCount(EventOrderPlaced); n != 0 {
		t.Errorf("subscriber count = %d, want 0", n)
	}

	// Channel must be closed so consumer range loops exit.
	if _, ok := <-ch; ok {
		t.Error("expected channel to be closed after Unsubscribe")
	}
}

func TestDifferentEventTypesIndependent(t *testing.T) {
	eb := NewEventBus(10)
	defer eb.Close()

	placedCh := eb.Subscribe(EventOrderPlaced)
	cancelledCh := eb.Subscribe(EventOrderCancelled)

	eb.Publish(Event{Type: EventOrderPlaced, Timestamp: time.Now(), Payload: "placed"})

	select {
	case e := <-placedCh:
		if e.Payload != "placed" {
			t.Errorf("placed payload = %v", e.Payload)
		}
	default:
		t.Fatal("placed subscriber should have received event")
	}

	// Cancelled subscriber should not have received the placed event.
	select {
	case <-cancelledCh:
		t.Fatal("cancelled subscriber should NOT receive placed events")
	default:
		// correct — nothing arrived
	}
}

func TestCloseClosesAllSubscriberChannels(t *testing.T) {
	eb := NewEventBus(10)

	ch1 := eb.Subscribe(EventOrderPlaced)
	ch2 := eb.Subscribe(EventTradeExecuted)

	eb.Close()

	// Both channels should be closed.
	for i, ch := range []<-chan Event{ch1, ch2} {
		if _, ok := <-ch; ok {
			t.Errorf("channel %d not closed after Close", i)
		}
	}

	// No subscribers left.
	if n := eb.SubscriberCount(EventOrderPlaced); n != 0 {
		t.Errorf("subscriber count after Close = %d, want 0", n)
	}
}

func TestPublishAfterCloseIsNoop(t *testing.T) {
	eb := NewEventBus(10)
	ch := eb.Subscribe(EventOrderPlaced)

	eb.Close()

	// Publish must not panic.
	eb.Publish(Event{Type: EventOrderPlaced, Timestamp: time.Now(), Payload: "late"})

	// Channel should remain closed and empty.
	select {
	case e, ok := <-ch:
		if ok {
			t.Errorf("received unexpected event after Close: %v", e)
		}
	default:
		t.Error("expected channel to be closed, not empty-but-open")
	}
}

func TestSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	eb := NewEventBus(10)
	eb.Close()

	ch := eb.Subscribe(EventOrderPlaced)

	// Should get a pre-closed channel, not a live one.
	if _, ok := <-ch; ok {
		t.Error("expected pre-closed channel after Subscribe post-Close")
	}
}

func TestConcurrentPublish(t *testing.T) {
	eb := NewEventBus(100)
	defer eb.Close()
	ch := eb.Subscribe(EventTradeExecuted)

	const N = 1000
	var wg sync.WaitGroup
	wg.Add(N)

	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			eb.Publish(Event{
				Type: EventTradeExecuted, Timestamp: time.Now(), Payload: i,
			})
		}(i)
	}

	wg.Wait()

	// Drain with a timeout. Some events may be dropped (buffer=100, 1000 publishers)
	// but we should receive at least the buffer capacity.
	received := 0
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case <-ch:
			received++
		case <-deadline:
			break loop
		}
	}

	if received == 0 {
		t.Fatal("received 0 events, expected at least some from buffer")
	}
	t.Logf("received %d/%d events (rest dropped due to buffer full)", received, N)
}

func TestConcurrentSubscribeUnsubscribePublish(t *testing.T) {
	// Run with -race to detect data races.
	eb := NewEventBus(10)
	defer eb.Close()

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N * 3)

	// Subscribers
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			ch := eb.Subscribe(EventOrderPlaced)
			time.Sleep(time.Millisecond)
			eb.Unsubscribe(EventOrderPlaced, ch)
		}()
	}

	// Publishers
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			eb.Publish(Event{Type: EventOrderPlaced, Timestamp: time.Now()})
		}()
	}

	// Closer-ish: just stress concurrent access
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = eb.SubscriberCount(EventOrderPlaced)
		}()
	}

	wg.Wait()
}
