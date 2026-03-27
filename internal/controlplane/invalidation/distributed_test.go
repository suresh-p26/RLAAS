package invalidation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestNewDistributedBroker(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err, "miniredis")
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer client.Close()

	b := NewDistributedBroker(client, "rlaas:")
	require.NotNil(t, b)
	require.NotNil(t, b.distributed)
	b.Stop()
}

func TestDistributedPublishSubscribe(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err, "miniredis")
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
	err = b.Publish(context.Background(), "policy.changed", map[string]string{"policy_id": "p1"})
	require.NoError(t, err, "publish")

	select {
	case evt := <-received:
		assert.Equal(t, "p1", evt["policy_id"], "expected policy_id=p1")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestDistributedMultipleTopics(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err, "miniredis")
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
		assert.Equal(t, "v1", v, "topic1")
	case <-time.After(2 * time.Second):
		t.Fatal("topic1 timeout")
	}
	select {
	case v := <-ch2:
		assert.Equal(t, "v2", v, "topic2")
	case <-time.After(2 * time.Second):
		t.Fatal("topic2 timeout")
	}
}

func TestDistributedStop_Idempotent(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err, "miniredis")
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
	got := d.channelName("topic")
	assert.Equal(t, "pfx:topic", got, "expected pfx:topic")
}
