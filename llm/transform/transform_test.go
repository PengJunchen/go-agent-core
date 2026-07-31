package transform

import (
	"context"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
)

// mockTransformer 实现MessageTransformer接口，用于编译契约验证。
type mockTransformer struct {
	calls int
}

func (m *mockTransformer) Transform(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
	m.calls++
	out := make([]message.Message, len(msgs))
	copy(out, msgs)
	return out, nil
}

// Interface-001: MessageTransformer 接口可被 mock 实现。
func TestMessageTransformer_InterfaceContract(t *testing.T) {
	var _ MessageTransformer = (*mockTransformer)(nil)
}

// VT-001: NewDefaultTransformer 设置合理默认值。
func TestNewDefaultTransformer_Defaults(t *testing.T) {
	dt := NewDefaultTransformer()
	if dt.ToolCallIdClamp != 64 {
		t.Errorf("ToolCallIdClamp = %d, want 64", dt.ToolCallIdClamp)
	}
	if !dt.ImageFallback {
		t.Error("ImageFallback = false, want true")
	}
	if !dt.ThinkingAdapter {
		t.Error("ThinkingAdapter = false, want true")
	}
}

// VT-002: DefaultTransformer 实现MessageTransformer接口。
func TestDefaultTransformer_Interface(t *testing.T) {
	var _ MessageTransformer = (*DefaultTransformer)(nil)
}

// VT-003: Transform 返回与输入数量相同的消息。
func TestDefaultTransformer_TransformCountPreserved(t *testing.T) {
	dt := NewDefaultTransformer()
	in := []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
		message.NewTextMessage(message.RoleAssistant, "hi"),
		message.NewTextMessage(message.RoleUser, "bye"),
	}
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(out) != len(in) {
		t.Errorf("output count = %d, want %d", len(out), len(in))
	}
}

// VT-004: Transform 返回新的顶层切片，追加输出不影响输入。
func TestDefaultTransformer_TransformReturnsCopy(t *testing.T) {
	dt := NewDefaultTransformer()
	in := []message.Message{message.NewTextMessage(message.RoleUser, "original")}
	out, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	// 追加到输出不应影响输入切片长度
	out = append(out, message.NewTextMessage(message.RoleAssistant, "extra"))
	if len(in) != 1 {
		t.Errorf("input length changed to %d after appending to output, want 1", len(in))
	}
	if len(out) != 2 {
		t.Errorf("output length = %d, want 2", len(out))
	}
}

// VT-005: Transform 保留消息内容与角色。
func TestDefaultTransformer_TransformPreservesContent(t *testing.T) {
	dt := NewDefaultTransformer()
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "you are helpful"),
		message.NewTextMessage(message.RoleUser, "ping"),
	}
	out, err := dt.Transform(context.Background(), in, "gemini")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Role != message.RoleSystem || out[0].Content[0].Text != "you are helpful" {
		t.Errorf("out[0] = %+v, want system/you are helpful", out[0])
	}
	if out[1].Role != message.RoleUser || out[1].Content[0].Text != "ping" {
		t.Errorf("out[1] = %+v, want user/ping", out[1])
	}
}

// VT-006: Transform 处理空切片返回空切片而非nil。
func TestDefaultTransformer_TransformEmpty(t *testing.T) {
	dt := NewDefaultTransformer()
	out, err := dt.Transform(context.Background(), []message.Message{}, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out == nil {
		t.Error("output is nil, want empty slice")
	}
	if len(out) != 0 {
		t.Errorf("output count = %d, want 0", len(out))
	}
}

// VT-007: Transform 对任意 provider 名均能直通（骨架行为）。
func TestDefaultTransformer_TransformAnyProvider(t *testing.T) {
	dt := NewDefaultTransformer()
	in := []message.Message{message.NewTextMessage(message.RoleUser, "x")}
	providers := []string{"openai", "anthropic", "gemini", "unknown"}
	for _, p := range providers {
		out, err := dt.Transform(context.Background(), in, p)
		if err != nil {
			t.Errorf("Transform provider %q: %v", p, err)
			continue
		}
		if len(out) != 1 {
			t.Errorf("provider %q: output count = %d, want 1", p, len(out))
		}
	}
}
