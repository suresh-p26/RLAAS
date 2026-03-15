package benchmarks

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/suresh-p26/RLAAS/internal/store/counter/memory"
)

func BenchmarkMemoryStoreIncrement(b *testing.B) {
	b.Run("single_key_contention", func(b *testing.B) {
		store := memory.NewSharded(128)
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, _ = store.Increment(ctx, "bench:single", 1, 0)
			}
		})
	})

	b.Run("many_keys", func(b *testing.B) {
		store := memory.NewSharded(128)
		ctx := context.Background()
		var id atomic.Int64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			keyID := id.Add(1)
			key := "bench:key:" + strconv.FormatInt(keyID, 10)
			for pb.Next() {
				_, _ = store.Increment(ctx, key, 1, 0)
			}
		})
	})
}

func BenchmarkMemoryStoreLease(b *testing.B) {
	store := memory.NewSharded(128)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ok, _, _ := store.AcquireLease(ctx, "bench:lease", 1_000_000, 0)
			if ok {
				_ = store.ReleaseLease(ctx, "bench:lease")
			}
		}
	})
}
