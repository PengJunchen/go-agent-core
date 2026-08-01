package production

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// StdoutTelemetryCollector writes metrics and spans to stderr.
type StdoutTelemetryCollector struct {
	mu sync.Mutex
	w io.Writer
}

// NewStdoutTelemetryCollector creates a collector that writes to w.
// If w is nil, uses os.Stderr.
func NewStdoutTelemetryCollector(w io.Writer) *StdoutTelemetryCollector {
	if w == nil {
		w = os.Stderr
	}
	return &StdoutTelemetryCollector{w: w}
}

func (c *StdoutTelemetryCollector) RecordMetric(_ context.Context, metric MetricPoint) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := fmt.Fprintf(c.w, "[METRIC] %s=%v tags=%v ts=%s\n", metric.Name, metric.Value, metric.Tags, metric.Timestamp.Format(time.RFC3339))
	return err
}

func (c *StdoutTelemetryCollector) StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	cfg := &SpanConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	span := &stdoutSpan{
		name: name,
		ctx: ctx,
		w: c.w,
		mu: &c.mu,
		start: time.Now(),
		tags: cfg.Tags,
	}
	return ctx, span
}

func (c *StdoutTelemetryCollector) RecordError(_ context.Context, err error, opts ...ErrorOption) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg := &ErrorConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	_, _ = fmt.Fprintf(c.w, "[ERROR] %v tags=%v\n", err, cfg.Tags) // 写入失败无法处理
}

type stdoutSpan struct {
	name string
	ctx context.Context
	w io.Writer
	mu *sync.Mutex
	start time.Time
	tags map[string]any
}

func (s *stdoutSpan) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	duration := time.Since(s.start)
	_, _ = fmt.Fprintf(s.w, "[SPAN] %s duration=%v tags=%v\n", s.name, duration, s.tags) // 写入失败无法处理
}

func (s *stdoutSpan) SetTag(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags == nil {
		s.tags = make(map[string]any)
	}
	s.tags[key] = value
}

func (s *stdoutSpan) RecordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.w, "[SPAN-ERROR] %s error=%v\n", s.name, err) // 写入失败无法处理
}

func (s *stdoutSpan) Context() context.Context { return s.ctx }
