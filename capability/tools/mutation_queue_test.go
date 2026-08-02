package tools

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// MQ-001 (AC-1): Per-path lock serializes concurrent writes to the same file.
func TestMutationQueue_SamePathSerialized(t *testing.T) {
	q := NewFileMutationQueue()
	var counter int64
	var maxConcurrent int64
	var currentConcurrent int64

	var wg sync.WaitGroup
	const n = 100
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := q.WithLock("/same/path/test.txt", func() error {
				cur := atomic.AddInt64(&currentConcurrent, 1)
				for {
					old := atomic.LoadInt64(&maxConcurrent)
					if cur <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, cur) {
						break
					}
				}
				atomic.AddInt64(&counter, 1)
				atomic.AddInt64(&currentConcurrent, -1)
				return nil
			})
			if err != nil {
				t.Errorf("WithLock returned error: %v", err)
			}
		}()
	}
	wg.Wait()

	if counter != n {
		t.Errorf("counter = %d, want %d", counter, n)
	}
	if maxConcurrent != 1 {
		t.Errorf("maxConcurrent = %d, want 1 (same-path writes should be serialized)", maxConcurrent)
	}
}

// MQ-002 (AC-2): Different paths can be written concurrently (no blocking).
func TestMutationQueue_DifferentPathsConcurrent(t *testing.T) {
	q := NewFileMutationQueue()
	var concurrent int64
	var maxConcurrent int64

	const paths = 5
	const n = 20 // iterations per path
	var wg sync.WaitGroup

	// Use a barrier to maximize concurrent launches.
	start := make(chan struct{})

	for p := 0; p < paths; p++ {
		for i := 0; i < n; i++ {
			wg.Add(1)
			path := fmt.Sprintf("/tmp/file%d_%d.txt", p, i)
			go func(pth string) {
				defer wg.Done()
				<-start
				err := q.WithLock(pth, func() error {
					cur := atomic.AddInt64(&concurrent, 1)
					for {
						old := atomic.LoadInt64(&maxConcurrent)
						if cur <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, cur) {
							break
						}
					}
					// Small delay to increase chance of overlap between different paths.
					time.Sleep(2 * time.Millisecond)
					atomic.AddInt64(&concurrent, -1)
					return nil
				})
				if err != nil {
					t.Errorf("WithLock returned error: %v", err)
				}
			}(path)
		}
	}

	close(start) // Release all goroutines at once.
	wg.Wait()

	if maxConcurrent < 2 {
		t.Errorf("maxConcurrent = %d, expected >= 2 (different paths should run concurrently)", maxConcurrent)
	}
}

// MQ-003: WithLock propagates fn's error.
func TestMutationQueue_PropagatesError(t *testing.T) {
	q := NewFileMutationQueue()
	testErr := errors.New("test error")
	err := q.WithLock("/some/path.txt", func() error {
		return testErr
	})
	if !errors.Is(err, testErr) {
		t.Errorf("expected %v, got %v", testErr, err)
	}
}

// MQ-004: WithLock returns nil when fn succeeds.
func TestMutationQueue_SuccessReturnsNil(t *testing.T) {
	q := NewFileMutationQueue()
	err := q.WithLock("/some/path.txt", func() error {
		return nil
	})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// MQ-005 (AC-1): Serialized writes produce correct final content.
func TestMutationQueue_SerializedWritesProduceCorrectResult(t *testing.T) {
	q := NewFileMutationQueue()
	var counter int64
	var wg sync.WaitGroup

	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := q.WithLock("/fake/counter.txt", func() error {
				// Simulate read-modify-write without a real file.
				cur := atomic.LoadInt64(&counter)
				time.Sleep(time.Microsecond) // Encourage interleaving.
				atomic.StoreInt64(&counter, cur+1)
				return nil
			})
			if err != nil {
				t.Errorf("WithLock error: %v", err)
			}
		}()
	}
	wg.Wait()

	if counter != n {
		t.Errorf("counter = %d, want %d (serialized writes should not lose increments)", counter, n)
	}
}
