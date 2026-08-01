package production

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestStdoutTelemetryCollector_Interface(t *testing.T) {
	var _ TelemetryCollector = (*StdoutTelemetryCollector)(nil)
}

func TestStdoutTelemetryCollector_RecordMetric(t *testing.T) {
	var buf bytes.Buffer
	tc := NewStdoutTelemetryCollector(&buf)
	err := tc.RecordMetric(context.Background(), MetricPoint{
		Name: "test.metric", Value: 42.0, Timestamp: time.Now(),
	})
	if err != nil {
		t.Errorf("RecordMetric err = %v", err)
	}
	if !strings.Contains(buf.String(), "test.metric") {
		t.Errorf("output should contain metric name, got %q", buf.String())
	}
}

func TestStdoutTelemetryCollector_StartSpan(t *testing.T) {
	var buf bytes.Buffer
	tc := NewStdoutTelemetryCollector(&buf)
	_, span := tc.StartSpan(context.Background(), "test.span")
	if span == nil {
		t.Error("span should not be nil")
	}
	span.End()
	if !strings.Contains(buf.String(), "test.span") {
		t.Errorf("output should contain span name, got %q", buf.String())
	}
}

func TestStdoutTelemetryCollector_RecordError(t *testing.T) {
	var buf bytes.Buffer
	tc := NewStdoutTelemetryCollector(&buf)
	tc.RecordError(context.Background(), fmt.Errorf("test error"))
	if !strings.Contains(buf.String(), "test error") {
		t.Errorf("output should contain error, got %q", buf.String())
	}
}

func TestStdoutTelemetryCollector_NilWriter(t *testing.T) {
	tc := NewStdoutTelemetryCollector(nil)
	if tc == nil {
		t.Error("should not be nil")
	}
}

func TestStdoutTelemetryCollector_SpanWithTags(t *testing.T) {
	var buf bytes.Buffer
	tc := NewStdoutTelemetryCollector(&buf)
	_, span := tc.StartSpan(context.Background(), "test", SpanOption(func(cfg *SpanConfig) {
		cfg.Tags = map[string]any{"key": "value"}
	}))
	span.SetTag("extra", "tag")
	span.End()
	output := buf.String()
	if !strings.Contains(output, "test") {
		t.Errorf("output should contain span name, got %q", output)
	}
}

func TestStdoutTelemetryCollector_SpanRecordError(t *testing.T) {
	var buf bytes.Buffer
	tc := NewStdoutTelemetryCollector(&buf)
	_, span := tc.StartSpan(context.Background(), "test")
	span.RecordError(fmt.Errorf("span error"))
	span.End()
	output := buf.String()
	if !strings.Contains(output, "span error") {
		t.Errorf("output should contain span error, got %q", output)
	}
}

func TestStdoutTelemetryCollector_SpanContext(t *testing.T) {
	tc := NewStdoutTelemetryCollector(nil)
	ctx := context.Background()
	_, span := tc.StartSpan(ctx, "test")
	if span.Context() != ctx {
		t.Error("span should return original context")
	}
}
