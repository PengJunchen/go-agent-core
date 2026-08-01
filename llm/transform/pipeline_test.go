package transform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
)

// TestTransformPipeline_EmptyPipeline verifies empty pipeline returns input unchanged.
func TestTransformPipeline_EmptyPipeline(t *testing.T) {
	p := NewTransformPipeline()
	in := []message.Message{message.NewTextMessage(message.RoleUser, "hello")}
	out, err := p.Execute(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("output count = %d, want 1", len(out))
	}
	if out[0].Content[0].Text != "hello" {
		t.Errorf("text = %q, want %q", out[0].Content[0].Text, "hello")
	}
}

// TestTransformPipeline_SingleStep verifies single step transforms correctly.
func TestTransformPipeline_SingleStep(t *testing.T) {
	called := false
	step := func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
		called = true
		return msgs, nil
	}

	p := NewTransformPipeline(step)
	in := []message.Message{message.NewTextMessage(message.RoleUser, "test")}
	_, err := p.Execute(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Error("step was not called")
	}
}

// TestTransformPipeline_MultipleSteps verifies multiple steps chain correctly.
func TestTransformPipeline_MultipleSteps(t *testing.T) {
	step1 := func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
		for i := range msgs {
			msgs[i].Content[0].Text = "step1:" + msgs[i].Content[0].Text
		}
		return msgs, nil
	}
	step2 := func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
		for i := range msgs {
			msgs[i].Content[0].Text = "step2:" + msgs[i].Content[0].Text
		}
		return msgs, nil
	}

	p := NewTransformPipeline(step1, step2)
	in := []message.Message{message.NewTextMessage(message.RoleUser, "original")}
	out, err := p.Execute(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "step2:step1:original"
	if out[0].Content[0].Text != want {
		t.Errorf("text = %q, want %q", out[0].Content[0].Text, want)
	}
}

// TestTransformPipeline_ErrorStopsPipeline verifies error in step stops execution.
func TestTransformPipeline_ErrorStopsPipeline(t *testing.T) {
	step2Called := false
	step1 := func(_ context.Context, _ []message.Message, _ string) ([]message.Message, error) {
		return nil, errors.New("step1 failed")
	}
	step2 := func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
		step2Called = true
		return msgs, nil
	}

	p := NewTransformPipeline(step1, step2)
	in := []message.Message{message.NewTextMessage(message.RoleUser, "test")}
	_, err := p.Execute(context.Background(), in, "openai")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "step1 failed") {
		t.Errorf("error = %q, want to contain 'step1 failed'", err.Error())
	}
	if step2Called {
		t.Error("step2 should not be called after step1 fails")
	}
}

// TestTransformPipeline_AddSteps verifies add steps after creation.
func TestTransformPipeline_AddSteps(t *testing.T) {
	p := NewTransformPipeline()
	if p.Steps() != 0 {
		t.Fatalf("initial steps = %d, want 0", p.Steps())
	}

	p.Add(func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
		return msgs, nil
	})
	if p.Steps() != 1 {
		t.Errorf("steps after add = %d, want 1", p.Steps())
	}

	in := []message.Message{message.NewTextMessage(message.RoleUser, "test")}
	out, err := p.Execute(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("output count = %d, want 1", len(out))
	}
}

// TestTransformPipeline_BuiltinToolCallIDNormalizer verifies built-in ToolCallIDNormalizer works.
func TestTransformPipeline_BuiltinToolCallIDNormalizer(t *testing.T) {
	fn := BuiltinTransforms.ToolCallIDNormalizer
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: "0", Name: "search"},
				{ID: "call_abc", Name: "lookup"},
			},
		},
		{
			Role: message.RoleTool,
			ToolCallID: "0",
			Content: []message.Content{{Type: message.ContentText, Text: "result"}},
		},
	}
	out, err := fn(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ToolCallIDNormalizer: %v", err)
	}
	if out[0].ToolCalls[0].ID != "tc_0" {
		t.Errorf("numeric ID not normalized: %q, want %q", out[0].ToolCalls[0].ID, "tc_0")
	}
	if out[0].ToolCalls[1].ID != "call_abc" {
		t.Errorf("non-numeric ID changed: %q, want %q", out[0].ToolCalls[1].ID, "call_abc")
	}
	if out[1].ToolCallID != "tc_0" {
		t.Errorf("ToolCallID not mapped: %q, want %q", out[1].ToolCallID, "tc_0")
	}
	// Original should be unchanged
	if in[0].ToolCalls[0].ID != "0" {
		t.Errorf("input was modified by ToolCallIDNormalizer")
	}
}

// TestTransformPipeline_BuiltinImageFormatAdapter verifies built-in ImageFormatAdapter works.
func TestTransformPipeline_BuiltinImageFormatAdapter(t *testing.T) {
	fn := BuiltinTransforms.ImageFormatAdapter
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentImage, Image: &message.Image{Data: "abc", MediaType: "image/png"}},
			},
		},
	}

	// Non-vision provider: image replaced
	out, err := fn(context.Background(), in, "text-only")
	if err != nil {
		t.Fatalf("ImageFormatAdapter: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("content type for text-only = %v, want ContentText", out[0].Content[0].Type)
	}

	// Vision provider with allowed type: preserved
	out2, err := fn(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ImageFormatAdapter for openai: %v", err)
	}
	if out2[0].Content[0].Type != message.ContentImage {
		t.Errorf("content type for openai = %v, want ContentImage", out2[0].Content[0].Type)
	}

	// Vision provider with unsupported type: replaced
	inBmp := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentImage, Image: &message.Image{Data: "abc", MediaType: "image/bmp"}},
			},
		},
	}
	out3, err := fn(context.Background(), inBmp, "openai")
	if err != nil {
		t.Fatalf("ImageFormatAdapter for openai/bmp: %v", err)
	}
	if out3[0].Content[0].Type != message.ContentText {
		t.Errorf("content type for openai/bmp = %v, want ContentText", out3[0].Content[0].Type)
	}
}

// TestTransformPipeline_BuiltinThinkingBlockAdapterEnhanced verifies built-in ThinkingBlockAdapterEnhanced works.
func TestTransformPipeline_BuiltinThinkingBlockAdapterEnhanced(t *testing.T) {
	fn := BuiltinTransforms.ThinkingBlockAdapterEnhanced

	// OpenAI: thinking → text
	inThinking := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "hmm"},
			},
		},
	}
	out, err := fn(context.Background(), inThinking, "openai")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapterEnhanced for openai: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("content type for openai = %v, want ContentText", out[0].Content[0].Type)
	}

	// Anthropic: text prefix → thinking
	inPrefixed := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentText, Text: "[Thinking] hmm"},
			},
		},
	}
	out2, err := fn(context.Background(), inPrefixed, "anthropic")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapterEnhanced for anthropic: %v", err)
	}
	if out2[0].Content[0].Type != message.ContentThinking {
		t.Errorf("content type for anthropic = %v, want ContentThinking", out2[0].Content[0].Type)
	}
}

// TestTransformPipeline_BuiltinSystemMessageAdapter verifies built-in SystemMessageAdapter works.
func TestTransformPipeline_BuiltinSystemMessageAdapter(t *testing.T) {
	fn := BuiltinTransforms.SystemMessageAdapter

	// OpenAI: system message preserved
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "you are helpful"),
		message.NewTextMessage(message.RoleUser, "hello"),
	}
	out, err := fn(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("output count = %d, want 2", len(out))
	}
	if out[0].Role != message.RoleSystem {
		t.Errorf("first role = %v, want RoleSystem", out[0].Role)
	}

	// Ollama: system message merged into user
	out2, err := fn(context.Background(), in, "ollama")
	if err != nil {
		t.Fatalf("SystemMessageAdapter for ollama: %v", err)
	}
	if len(out2) != 1 {
		t.Fatalf("output count for ollama = %d, want 1", len(out2))
	}
	if out2[0].Role != message.RoleUser {
		t.Errorf("role for ollama = %v, want RoleUser", out2[0].Role)
	}
}

// TestTransformPipeline_AllNewBuiltinTransformsCombined verifies pipeline with all new transforms.
func TestTransformPipeline_AllNewBuiltinTransformsCombined(t *testing.T) {
	p := NewTransformPipeline(
		BuiltinTransforms.ToolCallIDNormalizer,
		BuiltinTransforms.NormalizeToolCallIDs,
		BuiltinTransforms.ImageFormatAdapter,
		BuiltinTransforms.ThinkingBlockAdapterEnhanced,
		BuiltinTransforms.SystemMessageAdapter,
	)

	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "you are helpful"),
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentImage, Image: &message.Image{Data: "abc", MediaType: "image/bmp"}},
				{Type: message.ContentText, Text: "hello"},
			},
		},
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "hmm"},
				{Type: message.ContentText, Text: "answer"},
			},
			ToolCalls: []message.ToolCall{
				{ID: "0", Name: "search"},
			},
		},
		{
			Role: message.RoleTool,
			ToolCallID: "0",
			Content: []message.Content{{Type: message.ContentText, Text: "result"}},
		},
	}

	out, err := p.Execute(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// System message preserved for OpenAI
	if out[0].Role != message.RoleSystem {
		t.Errorf("system role = %v, want RoleSystem", out[0].Role)
	}

	// Image with unsupported MIME replaced
	if out[1].Content[0].Type != message.ContentText {
		t.Errorf("image type = %v, want ContentText (unsupported MIME)", out[1].Content[0].Type)
	}

	// Thinking converted to text for OpenAI
	if out[2].Content[0].Type != message.ContentText {
		t.Errorf("thinking type = %v, want ContentText", out[2].Content[0].Type)
	}

	// ToolCall ID normalized from numeric
	if out[2].ToolCalls[0].ID != "tc_0" {
		t.Errorf("ToolCall ID = %q, want %q", out[2].ToolCalls[0].ID, "tc_0")
	}

	// Tool role ToolCallID normalized
	if out[3].ToolCallID != "tc_0" {
		t.Errorf("ToolCallID = %q, want %q", out[3].ToolCallID, "tc_0")
	}
}

// TestTransformPipeline_ConcurrentAccess verifies concurrent Add/Execute is safe.
func TestTransformPipeline_ConcurrentAccess(t *testing.T) {
	p := NewTransformPipeline()
	var wg sync.WaitGroup

	// Concurrent adds
	for i := range 5 {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			p.Add(func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
				return msgs, nil
			})
		}(i)
	}
	wg.Wait()

	if p.Steps() != 5 {
		t.Fatalf("steps = %d, want 5", p.Steps())
	}

	// Concurrent executes
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := []message.Message{message.NewTextMessage(message.RoleUser, "test")}
			_, err := p.Execute(context.Background(), in, "openai")
			if err != nil {
				t.Errorf("Execute: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestTransformPipeline_BuiltinNormalizeToolCallIDs verifies built-in normalize works.
func TestTransformPipeline_BuiltinNormalizeToolCallIDs(t *testing.T) {
	fn := BuiltinTransforms.NormalizeToolCallIDs
	longID := "call_abcdefghijklmnopqrstuvwxyz_0123456789"
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: longID, Name: "search"},
			},
		},
	}
	out, err := fn(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("NormalizeToolCallIDs: %v", err)
	}
	clamped := out[0].ToolCalls[0].ID
	if len(clamped) > 64 {
		t.Errorf("clamped ID length = %d, want <= 64, got %q", len(clamped), clamped)
	}
	// Original should be unchanged
	if in[0].ToolCalls[0].ID != longID {
		t.Errorf("input was modified by NormalizeToolCallIDs")
	}
}

// TestTransformPipeline_BuiltinImageDowngrade verifies built-in image downgrade works.
func TestTransformPipeline_BuiltinImageDowngrade(t *testing.T) {
	fn := BuiltinTransforms.ImageDowngrade
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{
					Type: message.ContentImage,
					Image: &message.Image{Data: "base64data", MediaType: "image/png"},
				},
			},
		},
	}
	// Non-vision provider
	out, err := fn(context.Background(), in, "text-only")
	if err != nil {
		t.Fatalf("ImageDowngrade: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("content type = %v, want ContentText", out[0].Content[0].Type)
	}
	expected := fmt.Sprintf("[Image: %s, %d bytes]", "image/png", len("base64data"))
	if out[0].Content[0].Text != expected {
		t.Errorf("placeholder = %q, want %q", out[0].Content[0].Text, expected)
	}

	// Vision provider - image should be preserved
	out2, err := fn(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ImageDowngrade for openai: %v", err)
	}
	if out2[0].Content[0].Type != message.ContentImage {
		t.Errorf("content type for openai = %v, want ContentImage", out2[0].Content[0].Type)
	}
}

// TestTransformPipeline_BuiltinThinkingBlockAdapter verifies built-in thinking adapter works.
func TestTransformPipeline_BuiltinThinkingBlockAdapter(t *testing.T) {
	fn := BuiltinTransforms.ThinkingBlockAdapter
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "let me think"},
			},
		},
	}

	// OpenAI: thinking should be converted
	out, err := fn(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapter for openai: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("content type for openai = %v, want ContentText", out[0].Content[0].Type)
	}
	if out[0].Content[0].Text != "[Thinking] let me think" {
		t.Errorf("text = %q, want %q", out[0].Content[0].Text, "[Thinking] let me think")
	}

	// Non-OpenAI: thinking should be preserved
	out2, err := fn(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapter for anthropic: %v", err)
	}
	if out2[0].Content[0].Type != message.ContentThinking {
		t.Errorf("content type for anthropic = %v, want ContentThinking", out2[0].Content[0].Type)
	}
}

// TestTransformPipeline_IntegrationWithContentTypeRegistry verifies pipeline + registry integration.
func TestTransformPipeline_IntegrationWithContentTypeRegistry(t *testing.T) {
	// Create a custom transform step that uses ContentTypeRegistry
	reg := message.NewContentTypeRegistry()
	reg.Register(message.NewContentTypeHandlerFunc(
		func() string { return "audio" },
		func(c message.Content, targetProvider string) (message.Content, error) {
			return message.Content{Type: message.ContentText, Text: "[Audio for " + targetProvider + "]"}, nil
		},
		func(_ message.Content) error { return nil },
	))

	audioStep := func(_ context.Context, msgs []message.Message, targetProvider string) ([]message.Message, error) {
		for i := range msgs {
			for j := range msgs[i].Content {
				// Custom content type detection by text prefix (simplified)
				if msgs[i].Content[j].Type == message.ContentText && strings.HasPrefix(msgs[i].Content[j].Text, "audio:") {
					transformed, err := reg.Transform("audio", msgs[i].Content[j], targetProvider)
					if err != nil {
						return nil, err
					}
					msgs[i].Content[j] = transformed
				}
			}
		}
		return msgs, nil
	}

	p := NewTransformPipeline(audioStep)
	in := []message.Message{
		message.NewTextMessage(message.RoleUser, "audio:clip.mp3"),
	}
	out, err := p.Execute(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out[0].Content[0].Text != "[Audio for openai]" {
		t.Errorf("text = %q, want %q", out[0].Content[0].Text, "[Audio for openai]")
	}
}

// TestTransformPipeline_Steps verifies Steps() returns correct count.
func TestTransformPipeline_Steps(t *testing.T) {
	p := NewTransformPipeline()
	if p.Steps() != 0 {
		t.Errorf("empty pipeline steps = %d, want 0", p.Steps())
	}

	p.Add(func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
		return msgs, nil
	})
	if p.Steps() != 1 {
		t.Errorf("steps = %d, want 1", p.Steps())
	}

	p2 := NewTransformPipeline(
		func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
			return msgs, nil
		},
		func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
			return msgs, nil
		},
	)
	if p2.Steps() != 2 {
		t.Errorf("pipeline with 2 steps = %d, want 2", p2.Steps())
	}
}

// TestDefaultTransformer_WithPipeline verifies DefaultTransformer delegates to pipeline when set.
func TestDefaultTransformer_WithPipeline(t *testing.T) {
	pipeline := NewTransformPipeline(
		func(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
			for i := range msgs {
				msgs[i].Content[0].Text = "pipeline:" + msgs[i].Content[0].Text
			}
			return msgs, nil
		},
	)

	dt := &DefaultTransformer{
		ToolCallIdClamp: 64, // These should be ignored when Pipeline is set
		ImageFallback: true,
		ThinkingAdapter: true,
		Pipeline: pipeline,
	}

	in := []message.Message{message.NewTextMessage(message.RoleUser, "hello")}
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Text != "pipeline:hello" {
		t.Errorf("text = %q, want %q", out[0].Content[0].Text, "pipeline:hello")
	}
}

// TestDefaultTransformer_WithoutPipeline_BackwardCompat verifies backward compatibility when pipeline is nil.
func TestDefaultTransformer_WithoutPipeline_BackwardCompat(t *testing.T) {
	dt := NewDefaultTransformer()
	// Pipeline should be nil
	if dt.Pipeline != nil {
		t.Error("Pipeline should be nil by default")
	}

	// Existing behavior should work
	in := []message.Message{message.NewTextMessage(message.RoleUser, "hello")}
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("output count = %d, want 1", len(out))
	}
}
