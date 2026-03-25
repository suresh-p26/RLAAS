package invalidation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	goredis "github.com/redis/go-redis/v9"
)

// Broker is a lightweight in-process event bus for phase 3 cache invalidation.
// It supports both local-only dispatch and distributed dispatch via Redis Pub/Sub.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string][]func(event map[string]string)
	distributed *distributedTransport
}

// NewBroker creates an empty invalidation broker (local-only).
func NewBroker() *Broker {
	return &Broker{subscribers: map[string][]func(event map[string]string){}}
}

// NewDistributedBroker creates a broker backed by Redis Pub/Sub for
// multi-instance deployments.  All published events are broadcast to
// every instance; local subscribers are called when a message arrives
// from any instance (including self).
func NewDistributedBroker(client goredis.UniversalClient, channelPrefix string) *Broker {
	b := NewBroker()
	b.distributed = &distributedTransport{
		client: client,
		prefix: channelPrefix,
		broker: b,
	}
	return b
}

// Subscribe registers a callback for a topic.
func (b *Broker) Subscribe(topic string, fn func(event map[string]string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[topic] = append(b.subscribers[topic], fn)

	// If distributed, start listening for this topic.
	if b.distributed != nil {
		b.distributed.ensureSubscription(topic)
	}
}

// Publish sends one event to all subscribers of the topic.
// When distributed transport is configured the event is published to Redis
// Pub/Sub which fans out to all instances (including this one).
func (b *Broker) Publish(ctx context.Context, topic string, event map[string]string) error {
	if b.distributed != nil {
		return b.distributed.publish(ctx, topic, event)
	}
	b.dispatchLocal(topic, event)
	return nil
}

func (b *Broker) dispatchLocal(topic string, event map[string]string) {
	b.mu.RLock()
	handlers := append([]func(event map[string]string){}, b.subscribers[topic]...)
	b.mu.RUnlock()
	for _, h := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("invalidation handler panic", "topic", topic, "error", r)
				}
			}()
			h(event)
		}()
	}
}

// Stop shuts down the distributed transport if active.
func (b *Broker) Stop() {
	if b.distributed != nil {
		b.distributed.stop()
	}
}

// ------------------------------------------------
// Distributed transport (Redis Pub/Sub)
// ------------------------------------------------

type distributedTransport struct {
	client goredis.UniversalClient
	prefix string
	broker *Broker

	mu      sync.Mutex
	subs    map[string]struct{}
	pubsub  *goredis.PubSub
	stopCh  chan struct{}
	stopped bool
}

func (d *distributedTransport) channelName(topic string) string {
	return d.prefix + topic
}

func (d *distributedTransport) publish(ctx context.Context, topic string, event map[string]string) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("invalidation: marshal event: %w", err)
	}
	return d.client.Publish(ctx, d.channelName(topic), payload).Err()
}

func (d *distributedTransport) ensureSubscription(topic string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.subs == nil {
		d.subs = map[string]struct{}{}
	}
	if _, ok := d.subs[topic]; ok {
		return
	}
	d.subs[topic] = struct{}{}

	if d.pubsub == nil {
		d.stopCh = make(chan struct{})
		d.pubsub = d.client.Subscribe(context.Background(), d.channelName(topic))
		go d.receiveLoop()
	} else {
		_ = d.pubsub.Subscribe(context.Background(), d.channelName(topic))
	}
}

func (d *distributedTransport) receiveLoop() {
	ch := d.pubsub.Channel()
	for {
		select {
		case <-d.stopCh:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// Extract topic from channel name.
			topic := msg.Channel
			if len(topic) > len(d.prefix) {
				topic = topic[len(d.prefix):]
			}
			var event map[string]string
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				slog.Warn("invalidation: unmarshal event", "error", err, "channel", msg.Channel)
				continue
			}
			d.broker.dispatchLocal(topic, event)
		}
	}
}

func (d *distributedTransport) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.stopped = true
	if d.stopCh != nil {
		close(d.stopCh)
	}
	if d.pubsub != nil {
		_ = d.pubsub.Close()
	}
}
