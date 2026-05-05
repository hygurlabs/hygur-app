package events

import (
	"context"
	"sync"
	"time"
)

// Broker provides a pub/sub mechanism for background events.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string][]chan Event
	allSubs     []chan Event
}

// NewBroker creates a new Broker instance.
func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[string][]chan Event),
		allSubs:     make([]chan Event, 0),
	}
}

// Subscribe returns a channel that receives all events.
// The caller should drain the channel and close it when done.
func (b *Broker) Subscribe() <-chan Event {
	ch := make(chan Event, 100)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allSubs = append(b.allSubs, ch)
	return ch
}

// SubscribeFor filters events by type.
func (b *Broker) SubscribeFor(eventType EventType) <-chan Event {
	ch := make(chan Event, 100)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[string(eventType)] = append(b.subscribers[string(eventType)], ch)
	return ch
}

// Publish sends an event to all subscribers.
func (b *Broker) Publish(evt Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Send to all subscribers
	for _, ch := range b.allSubs {
		select {
		case ch <- evt:
		default:
			// Drop if channel is full
		}
	}

	// Send to type-specific subscribers
	for _, ch := range b.subscribers[string(evt.Type)] {
		select {
		case ch <- evt:
		default:
		}
	}
}

// PublishWithType sends an event with a specific type.
func (b *Broker) PublishWithType(eventType EventType, status Status, source string, message string, data map[string]any) {
	b.Publish(Event{
		Type:      eventType,
		Source:    source,
		Status:    status,
		Message:   message,
		Data:      data,
		CreatedAt: time.Now(),
	})
}

// Close all subscriber channels.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.allSubs {
		close(ch)
	}
	for _, chs := range b.subscribers {
		for _, ch := range chs {
			close(ch)
		}
	}
}

// Subscriber represents a client that can send events.
type Subscriber struct {
	broker *Broker
	ch     <-chan Event
}

// NewSubscriber creates a new subscriber for a specific event type.
func (b *Broker) NewSubscriber(eventType EventType) *Subscriber {
	return &Subscriber{
		broker: b,
		ch:     b.SubscribeFor(eventType),
	}
}

// Channel returns the event channel.
func (s *Subscriber) Channel() <-chan Event {
	return s.ch
}

// Broadcast sends an event through the broker.
func (s *Broker) Broadcast(evt Event) {
	s.Publish(evt)
}

// Drain drains all subscriber channels.
func (b *Broker) Drain() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.allSubs {
		for len(ch) > 0 {
			<-ch
		}
	}
}

// Start starts the broker's event loop.
func (b *Broker) Start(ctx context.Context) {
	// Simple event loop that can be extended for periodic tasks
	<-ctx.Done()
}
