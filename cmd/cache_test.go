package cmd

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemo(t *testing.T) {
	t.Run("concurrent misses share one fetch", func(t *testing.T) {
		cache := newTTLCache[int](time.Hour, 4)
		var calls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		fetch := func() (int, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			return 42, nil
		}
		var wg sync.WaitGroup
		for range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				value, err := memo(cache, "same", fetch)
				if value != 42 || err != nil {
					t.Errorf("memo = %d, %v", value, err)
				}
			}()
		}
		<-started
		close(release)
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatalf("fetched %d times", calls.Load())
		}
	})
	t.Run("failures can be retried", func(t *testing.T) {
		cache := newTTLCache[int](time.Hour, 4)
		want := errors.New("upstream unavailable")
		if _, err := memo(cache, "key", func() (int, error) { return 0, want }); !errors.Is(err, want) {
			t.Fatal(err)
		}
		value, err := memo(cache, "key", func() (int, error) { return 42, nil })
		if value != 42 || err != nil {
			t.Fatalf("memo = %d, %v", value, err)
		}
	})
	t.Run("expired results are fetched again", func(t *testing.T) {
		cache := newTTLCache[int](time.Hour, 4)
		cache.m["key"] = ttlEntry[int]{val: 1, exp: time.Now().Add(-time.Second)}
		value, err := memo(cache, "key", func() (int, error) { return 2, nil })
		if value != 2 || err != nil {
			t.Fatalf("memo = %d, %v", value, err)
		}
	})
}
