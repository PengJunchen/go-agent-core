package transform

import (
	"context"
	"fmt"
	"sync"

	"github.com/pengjunchen/go-agent-core/llm/message"
)

// TransformFunc is a function that transforms messages.
type TransformFunc func(ctx context.Context, msgs []message.Message, targetProvider string) ([]message.Message, error)

// TransformPipeline chains multiple TransformFuncs into a single transform.
type TransformPipeline struct {
	mu sync.RWMutex
	steps []TransformFunc
}

// NewTransformPipeline creates a new pipeline with the given steps.
func NewTransformPipeline(steps ...TransformFunc) *TransformPipeline {
	return &TransformPipeline{
		steps: append([]TransformFunc{}, steps...),
	}
}

// Add appends a transform step to the pipeline.
func (p *TransformPipeline) Add(fn TransformFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps = append(p.steps, fn)
}

// Execute runs all transform steps in order.
// If any step fails, the pipeline stops and returns the error.
func (p *TransformPipeline) Execute(ctx context.Context, msgs []message.Message, targetProvider string) ([]message.Message, error) {
	p.mu.RLock()
	steps := make([]TransformFunc, len(p.steps))
	copy(steps, p.steps)
	p.mu.RUnlock()

	out := msgs
	for i, step := range steps {
		var err error
		out, err = step(ctx, out, targetProvider)
		if err != nil {
			return nil, fmt.Errorf("pipeline step %d failed: %w", i, err)
		}
	}
	return out, nil
}

// Steps returns the number of transform steps.
func (p *TransformPipeline) Steps() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.steps)
}

// BuiltinTransforms contains built-in transform functions.
var BuiltinTransforms = struct {
	// NormalizeToolCallIDs cleans up tool call IDs for the target provider.
	NormalizeToolCallIDs TransformFunc
	// ImageDowngrade replaces images with text for non-vision providers.
	ImageDowngrade TransformFunc
	// ThinkingBlockAdapter adapts thinking blocks for the target provider.
	ThinkingBlockAdapter TransformFunc
	// ToolCallIDNormalizer normalizes numeric tool call IDs to string format.
	ToolCallIDNormalizer TransformFunc
	// ImageFormatAdapter handles image format differences across providers.
	ImageFormatAdapter TransformFunc
	// ThinkingBlockAdapterEnhanced adapts thinking blocks bidirectionally.
	ThinkingBlockAdapterEnhanced TransformFunc
	// SystemMessageAdapter normalizes system message handling.
	SystemMessageAdapter TransformFunc
}{
	NormalizeToolCallIDs: normalizeToolCallIDsTransform,
	ImageDowngrade: imageDowngradeTransform,
	ThinkingBlockAdapter: thinkingBlockAdapterTransform,
	ToolCallIDNormalizer: ToolCallIDNormalizer,
	ImageFormatAdapter: ImageFormatAdapter,
	ThinkingBlockAdapterEnhanced: ThinkingBlockAdapterEnhanced,
	SystemMessageAdapter: SystemMessageAdapter,
}

// normalizeToolCallIDsTransform normalizes tool call IDs for the target provider.
// Applies deep copy, then clamps IDs to 64 characters with valid character set.
func normalizeToolCallIDsTransform(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
	out := deepCopyMessages(msgs)
	applyToolCallIdClamp(out, 64)
	return out, nil
}

// imageDowngradeTransform replaces images with text placeholders
// for providers that don't support vision.
func imageDowngradeTransform(_ context.Context, msgs []message.Message, targetProvider string) ([]message.Message, error) {
	out := deepCopyMessages(msgs)
	applyImageFallback(out, targetProvider)
	return out, nil
}

// thinkingBlockAdapterTransform adapts thinking blocks for the target provider.
// For OpenAI, converts thinking blocks to text with "[Thinking] " prefix.
func thinkingBlockAdapterTransform(_ context.Context, msgs []message.Message, targetProvider string) ([]message.Message, error) {
	out := deepCopyMessages(msgs)
	applyThinkingAdapter(out, targetProvider)
	return out, nil
}
