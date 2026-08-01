package production

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ─── DefaultLoopDetector tests ─────────────────────────────────────

func TestDefaultLoopDetector_Interface(t *testing.T) {
	t.Parallel()
	// Compile-time interface compliance check.
	var _ LoopDetector = (*DefaultLoopDetector)(nil)
}

func TestDefaultLoopDetector_NoLoopOnEmpty(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(DefaultLoopDetectorConfig())
	if d.IsLoop(context.Background()) {
		t.Error("IsLoop = true on empty detector, want false")
	}
}

func TestDefaultLoopDetector_NoLoopBelowThreshold(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	})
	args := map[string]any{"q": "test"}
	for i := 0; i < 2; i++ {
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: "search",
			Arguments: args,
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if d.IsLoop(context.Background()) {
		t.Error("IsLoop = true with 2 consecutive calls (threshold 3), want false")
	}
}

func TestDefaultLoopDetector_DetectsConsecutiveLoop(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	})
	args := map[string]any{"q": "test"}
	for i := 0; i < 3; i++ {
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: "search",
			Arguments: args,
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if !d.IsLoop(context.Background()) {
		t.Error("IsLoop = false with 3 consecutive identical calls (threshold 3), want true")
	}
}

func TestDefaultLoopDetector_DetectsLoopWithDifferentArgs(t *testing.T) {
	t.Parallel()
	// When ArgumentComparison=false, different args still count as same call.
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: false,
	})
	for i := 0; i < 3; i++ {
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: "search",
			Arguments: map[string]any{"q": i}, // different args each time
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if !d.IsLoop(context.Background()) {
		t.Error("IsLoop = false with 3 same-tool calls and ArgumentComparison=false, want true")
	}
}

func TestDefaultLoopDetector_NoLoopWithDifferentArgs(t *testing.T) {
	t.Parallel()
	// When ArgumentComparison=true, different args break the streak.
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	})
	for i := 0; i < 3; i++ {
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: "search",
			Arguments: map[string]any{"q": i}, // different args each time
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if d.IsLoop(context.Background()) {
		t.Error("IsLoop = true with different args and ArgumentComparison=true, want false")
	}
}

func TestDefaultLoopDetector_SlidingWindowEviction(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 3,
		ArgumentComparison: true,
	})
	args := map[string]any{"q": "test"}

	// Fill window with 3 "search" calls.
	for i := 0; i < 3; i++ {
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: "search",
			Arguments: args,
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if !d.IsLoop(context.Background()) {
		t.Fatal("expected loop after 3 identical calls")
	}

	// Record one "other" call — this evicts the oldest "search" from the window.
	if err := d.Record(context.Background(), ToolCallRecord{
		ToolName: "other",
		Arguments: args,
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("Record err = %v", err)
	}
	// Window now: [search, search, other] — loop broken.
	if d.IsLoop(context.Background()) {
		t.Error("IsLoop = true after eviction broke the streak, want false")
	}

	// ConsecutiveCount for "other" should be 1.
	if n := d.ConsecutiveCount(context.Background(), "other"); n != 1 {
		t.Errorf("ConsecutiveCount(other) = %d, want 1", n)
	}
}

func TestDefaultLoopDetector_Reset(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	})
	args := map[string]any{"q": "test"}
	for i := 0; i < 3; i++ {
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: "search",
			Arguments: args,
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if !d.IsLoop(context.Background()) {
		t.Fatal("expected loop before reset")
	}

	if err := d.Reset(context.Background()); err != nil {
		t.Fatalf("Reset err = %v", err)
	}
	if d.IsLoop(context.Background()) {
		t.Error("IsLoop = true after reset, want false")
	}
	if n := d.ConsecutiveCount(context.Background(), "search"); n != 0 {
		t.Errorf("ConsecutiveCount(search) = %d after reset, want 0", n)
	}
}

func TestDefaultLoopDetector_ConsecutiveCount(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 5,
		WindowSize: 10,
		ArgumentComparison: true,
	})
	args := map[string]any{"q": "test"}

	// Record: search, search, other, search
	if err := d.Record(context.Background(), ToolCallRecord{ToolName: "search", Arguments: args, Timestamp: time.Now()}); err != nil {
		t.Fatalf("Record err = %v", err)
	}
	if err := d.Record(context.Background(), ToolCallRecord{ToolName: "search", Arguments: args, Timestamp: time.Now()}); err != nil {
		t.Fatalf("Record err = %v", err)
	}
	if err := d.Record(context.Background(), ToolCallRecord{ToolName: "other", Arguments: args, Timestamp: time.Now()}); err != nil {
		t.Fatalf("Record err = %v", err)
	}
	if err := d.Record(context.Background(), ToolCallRecord{ToolName: "search", Arguments: args, Timestamp: time.Now()}); err != nil {
		t.Fatalf("Record err = %v", err)
	}

	if n := d.ConsecutiveCount(context.Background(), "search"); n != 1 {
		t.Errorf("ConsecutiveCount(search) = %d, want 1", n)
	}
	if n := d.ConsecutiveCount(context.Background(), "other"); n != 0 {
		t.Errorf("ConsecutiveCount(other) = %d, want 0", n)
	}
	if n := d.ConsecutiveCount(context.Background(), "nonexistent"); n != 0 {
		t.Errorf("ConsecutiveCount(nonexistent) = %d, want 0", n)
	}
}

func TestDefaultLoopDetector_ConcurrentSafety(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 100,
		WindowSize: 200,
		ArgumentComparison: true,
	})
	args := map[string]any{"q": "test"}
	ctx := context.Background()

	var wg sync.WaitGroup
	// Concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = d.Record(ctx, ToolCallRecord{
					ToolName: "search",
					Arguments: args,
					Timestamp: time.Now(),
				})
			}
		}()
	}
	// Concurrent readers.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = d.IsLoop(ctx)
				_ = d.ConsecutiveCount(ctx, "search")
			}
		}()
	}
	wg.Wait()
	// If we get here without panics or race conditions, the test passes.
}

func TestDefaultLoopDetector_DefaultConfig(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{})
	if d.config.ConsecutiveThreshold != 3 {
		t.Errorf("ConsecutiveThreshold = %d, want 3", d.config.ConsecutiveThreshold)
	}
	if d.config.WindowSize != 10 {
		t.Errorf("WindowSize = %d, want 10", d.config.WindowSize)
	}
	// ArgumentComparison defaults to false (zero value) since the zero-config
	// path only patches ConsecutiveThreshold and WindowSize.
}

func TestDefaultLoopDetector_MixedToolCalls(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	})
	args := map[string]any{"q": "test"}

	// Record alternating calls: search, read, search, read, search, read
	for i := 0; i < 6; i++ {
		name := "search"
		if i%2 == 1 {
			name = "read"
		}
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: name,
			Arguments: args,
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if d.IsLoop(context.Background()) {
		t.Error("IsLoop = true with alternating calls, want false")
	}
}

func TestDefaultLoopDetector_LoopAfterReset(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	})
	args := map[string]any{"q": "test"}

	// Trigger a loop.
	for i := 0; i < 3; i++ {
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: "search",
			Arguments: args,
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if !d.IsLoop(context.Background()) {
		t.Fatal("expected loop before reset")
	}

	// Reset.
	if err := d.Reset(context.Background()); err != nil {
		t.Fatalf("Reset err = %v", err)
	}

	// Record same calls again — loop should be detected again.
	for i := 0; i < 3; i++ {
		if err := d.Record(context.Background(), ToolCallRecord{
			ToolName: "search",
			Arguments: args,
			Timestamp: time.Now(),
		}); err != nil {
			t.Fatalf("Record err = %v", err)
		}
	}
	if !d.IsLoop(context.Background()) {
		t.Error("IsLoop = false after reset and re-recording, want true")
	}
}

func TestDefaultLoopDetector_AutoTimestamp(t *testing.T) {
	t.Parallel()
	d := NewDefaultLoopDetector(DefaultLoopDetectorConfig())
	before := time.Now()
	if err := d.Record(context.Background(), ToolCallRecord{
		ToolName: "search",
		Arguments: map[string]any{"q": "test"},
		// Timestamp is zero — should be auto-filled.
	}); err != nil {
		t.Fatalf("Record err = %v", err)
	}
	after := time.Now()

	// Verify the auto-timestamp by checking ConsecutiveCount works
	// (which proves the record was stored with a valid timestamp).
	if n := d.ConsecutiveCount(context.Background(), "search"); n != 1 {
		t.Errorf("ConsecutiveCount = %d, want 1", n)
	}

	// Check the stored record's timestamp is within the expected range.
	d.mu.RLock()
	ts := d.window[0].Timestamp
	d.mu.RUnlock()
	if ts.Before(before) || ts.After(after) {
		t.Errorf("auto-filled Timestamp = %v, want between %v and %v", ts, before, after)
	}
}
