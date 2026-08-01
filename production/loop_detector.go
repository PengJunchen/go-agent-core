package production

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultLoopDetector detects tool call loops using sliding window analysis.
// It implements the LoopDetector interface with configurable thresholds
// and optional argument comparison.
type DefaultLoopDetector struct {
	mu sync.RWMutex
	config LoopDetectorConfig
	window []ToolCallRecord
}

// NewDefaultLoopDetector creates a DefaultLoopDetector with the given config.
// Zero-valued fields are replaced with defaults (ConsecutiveThreshold=3, WindowSize=10).
func NewDefaultLoopDetector(cfg LoopDetectorConfig) *DefaultLoopDetector {
	if cfg.ConsecutiveThreshold <= 0 {
		cfg.ConsecutiveThreshold = 3
	}
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 10
	}
	return &DefaultLoopDetector{
		config: cfg,
		window: make([]ToolCallRecord, 0, cfg.WindowSize),
	}
}

// Record records a tool call for loop analysis.
// If the call's Timestamp is zero, it is set to time.Now().
// When the sliding window exceeds WindowSize, the oldest record is evicted.
func (d *DefaultLoopDetector) Record(_ context.Context, call ToolCallRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if call.Timestamp.IsZero() {
		call.Timestamp = time.Now()
	}

	d.window = append(d.window, call)
	if len(d.window) > d.config.WindowSize {
		d.window = d.window[len(d.window)-d.config.WindowSize:]
	}
	return nil
}

// IsLoop returns true if the last ConsecutiveThreshold records are identical
// tool calls (same tool name, and same arguments when ArgumentComparison is enabled).
func (d *DefaultLoopDetector) IsLoop(_ context.Context) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.window) < d.config.ConsecutiveThreshold {
		return false
	}

	// Check consecutive identical calls from the end of the window.
	last := d.window[len(d.window)-1]
	count := 1
	for i := len(d.window) - 2; i >= 0; i-- {
		if d.callsMatch(d.window[i], last) {
			count++
			if count >= d.config.ConsecutiveThreshold {
				return true
			}
		} else {
			break
		}
	}
	return false
}

// Reset clears the detection history.
func (d *DefaultLoopDetector) Reset(_ context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = d.window[:0]
	return nil
}

// ConsecutiveCount returns how many consecutive calls at the end of the window
// match the given toolName.
func (d *DefaultLoopDetector) ConsecutiveCount(_ context.Context, toolName string) int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.window) == 0 {
		return 0
	}

	count := 0
	for i := len(d.window) - 1; i >= 0; i-- {
		if d.window[i].ToolName == toolName {
			count++
		} else {
			break
		}
	}
	return count
}

// callsMatch checks if two tool call records match based on the detector config.
// When ArgumentComparison is true, both tool name and arguments must match.
// When ArgumentComparison is false, only tool name is compared.
func (d *DefaultLoopDetector) callsMatch(a, b ToolCallRecord) bool {
	if a.ToolName != b.ToolName {
		return false
	}
	if !d.config.ArgumentComparison {
		return true
	}
	return argumentsEqual(a.Arguments, b.Arguments)
}

// argumentsEqual compares two argument maps deterministically.
func argumentsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", va) != fmt.Sprintf("%v", vb) {
			return false
		}
	}
	return true
}
