package httprateredis_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	httprateredis "github.com/go-chi/httprate-redis"
	"golang.org/x/sync/errgroup"
)

func TestRedisCounter(t *testing.T) {
	limitCounter := httprateredis.NewCounter(&httprateredis.Config{
		Host:             "localhost",
		Port:             6379,
		MaxIdle:          0,
		MaxActive:        2,
		DBIndex:          0,
		ClientName:       "httprateredis_test",
		PrefixKey:        fmt.Sprintf("httprate:test:%v", rand.Int31n(100000)), // Unique Redis key for each test
		FallbackTimeout:  time.Second,
		FallbackDisabled: true,
	})
	defer limitCounter.Close()

	limitCounter.Config(1000, time.Minute)

	currentWindow := time.Now().UTC().Truncate(time.Minute)
	previousWindow := currentWindow.Add(-time.Minute)

	type test struct {
		name        string        // In each test do the following:
		advanceTime time.Duration // 1. advance time
		incrBy      int           // 2. increase counter
		prev        int           // 3. check previous window counter
		curr        int           //    and current window counter
	}

	tests := []test{
		{
			name: "t=0m: init",
			prev: 0,
			curr: 0,
		},
		{
			name:   "t=0m: increment by 1",
			incrBy: 1,
			prev:   0,
			curr:   1,
		},
		{
			name:   "t=0m: increment by 99",
			incrBy: 99,
			prev:   0,
			curr:   100,
		},
		{
			name:        "t=1m: move clock by 1m",
			advanceTime: time.Minute,
			prev:        100,
			curr:        0,
		},
		{
			name:   "t=1m: increment by 20",
			incrBy: 20,
			prev:   100,
			curr:   20,
		},
		{
			name:   "t=1m: increment by 20",
			incrBy: 20,
			prev:   100,
			curr:   40,
		},
		{
			name:        "t=2m: move clock by 1m",
			advanceTime: time.Minute,
			prev:        40,
			curr:        0,
		},
		{
			name:   "t=2m: incr++",
			incrBy: 1,
			prev:   40,
			curr:   1,
		},
		{
			name:   "t=2m: incr+=9",
			incrBy: 9,
			prev:   40,
			curr:   10,
		},
		{
			name:   "t=2m: incr+=20",
			incrBy: 20,
			prev:   40,
			curr:   30,
		},
		{
			name:        "t=4m: move clock by 2m",
			advanceTime: 2 * time.Minute,
			prev:        0,
			curr:        0,
		},
	}

	concurrentRequests := 1000

	for _, tt := range tests {
		if tt.advanceTime > 0 {
			currentWindow = currentWindow.Add(tt.advanceTime)
			previousWindow = previousWindow.Add(tt.advanceTime)
		}

		if tt.incrBy > 0 {
			var g errgroup.Group
			for i := 0; i < concurrentRequests; i++ {
				i := i
				g.Go(func() error {
					key := fmt.Sprintf("key:%v", i)
					return limitCounter.IncrementBy(key, currentWindow, tt.incrBy)
				})
			}
			if err := g.Wait(); err != nil {
				t.Errorf("%s: %v", tt.name, err)
			}
		}

		var g errgroup.Group
		for i := 0; i < concurrentRequests; i++ {
			i := i
			g.Go(func() error {
				key := fmt.Sprintf("key:%v", i)
				curr, prev, err := limitCounter.Get(key, currentWindow, previousWindow)
				if err != nil {
					return fmt.Errorf("%q: %w", key, err)
				}
				if curr != tt.curr {
					return fmt.Errorf("%q: unexpected curr = %v, expected %v", key, curr, tt.curr)
				}
				if prev != tt.prev {
					return fmt.Errorf("%q: unexpected prev = %v, expected %v", key, prev, tt.prev)
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func BenchmarkLocalCounter(b *testing.B) {
	limitCounter := httprateredis.NewCounter(&httprateredis.Config{
		Host:             "localhost",
		Port:             6379,
		DBIndex:          0,
		ClientName:       "httprateredis_test",
		PrefixKey:        fmt.Sprintf("httprate:test:%v", rand.Int31n(100000)), // Unique key for each test
		MaxActive:        10,
		MaxIdle:          0,
		FallbackDisabled: true,
		FallbackTimeout:  5 * time.Second,
	})
	defer limitCounter.Close()

	limitCounter.Config(1000, time.Minute)

	currentWindow := time.Now().UTC().Truncate(time.Minute)
	previousWindow := currentWindow.Add(-time.Minute)

	concurrentRequests := 100

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for i := range []int{0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 3, 0, 0, 0, 0, 1, 0} {
			// Simulate time.
			currentWindow.Add(time.Duration(i) * time.Minute)
			previousWindow.Add(time.Duration(i) * time.Minute)

			wg := sync.WaitGroup{}
			wg.Add(concurrentRequests)
			for i := 0; i < concurrentRequests; i++ {
				// Simulate concurrent requests with different rate-limit keys.
				go func(i int) {
					defer wg.Done()

					_, _, _ = limitCounter.Get(fmt.Sprintf("key:%v", i), currentWindow, previousWindow)
					_ = limitCounter.IncrementBy(fmt.Sprintf("key:%v", i), currentWindow, rand.Intn(20))
				}(i)
			}
			wg.Wait()
		}
	}
}
