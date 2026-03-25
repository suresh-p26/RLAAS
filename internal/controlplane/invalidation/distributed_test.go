package invalidation

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestNewDistributedBroker(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer client.Close()

	b := NewDistributedBroker(client, "rlaas:")
	if b == nil || b.distributed == nil {
		t.Fatal("expected distributed broker")
	}
	b.Stop()
}

func TestDistributedPublishSubscribe(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer client.Close()

	b := NewDistributedBroker(client, "test:")
	defer b.Stop()

	received := make(chan map[string]string, 1)
	b.Subscribe("policy.changed", func(event map[string]string) {
		received <- event
	})

	// Give the subscription time to set up.
	time.Sleep(50 * time.Millisecond)

	// Publish via the distributed transport.
	if err := b.Publish(context.Background(), "policy.changed", map[string]string{"policy_id": "p1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case evt := <-received:
		if evt["policy_id"] != "p1" {
			t.Fatalf("expected policy_id=p1, got %v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestDistributedMultipleTopics(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer client.Close()

	b := NewDistributedBroker(client, "mt:")
	defer b.Stop()

	ch1 := make(chan string, 1)
	ch2 := make(chan string, 1)

	b.Subscribe("topic1", func(event map[string]string) {
		ch1 <- event["k"]
	})
	b.Subscribe("topic2", func(event map[string]string) {
		ch2 <- event["k"]
	})

	time.Sleep(50 * time.Millisecond)

	_ = b.Publish(context.Background(), "topic1", map[string]string{"k": "v1"})
	_ = b.Publish(context.Background(), "topic2", map[string]string{"k": "v2"})

	select {
	case v := <-ch1:
		if v != "v1" {
			t.Fatalf("topic1: got %s", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("topic1 timeout")
	}
	select {
	case v := <-ch2:
		if v != "v2" {
			t.Fatalf("topic2: got %s", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("topic2 timeout")
	}
}

func TestDistributedStop_Idempotent(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer client.Close()

	b := NewDistributedBroker(client, "s:")
	b.Subscribe("t", func(_ map[string]string) {})
	time.Sleep(20 * time.Millisecond)

	b.Stop()
	b.Stop() // second call should not panic
}

func TestDispatchLocal_PanicRecovery(t *testing.T) {
	b := NewBroker()
	b.Subscribe("panic-topic", func(_ map[string]string) {
		panic("handler panic")
	})

	// Should not panic.
	b.dispatchLocal("panic-topic", map[string]string{"k": "v"})
}

func TestStopLocal_NilDistributed(t *testing.T) {
	b := NewBroker()
	b.Stop() // should not panic
}

func TestChannelName(t *testing.T) {
	d := &distributedTransport{prefix: "pfx:"}
	if got := d.channelName("topic"); got != "pfx:topic" {
		t.Fatalf("expected pfx:topic, got %s", got)
	}
}
