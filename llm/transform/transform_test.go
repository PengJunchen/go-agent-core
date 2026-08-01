package transform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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

// ========== 深拷贝测试 ==========

// VT-008: Transform 深拷贝 Content 切片，修改输出不影响输入。
func TestDefaultTransformer_DeepCopyContent(t *testing.T) {
	dt := &DefaultTransformer{} // 全部关闭，只测深拷贝
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{{Type: message.ContentText, Text: "original"}},
		},
	}
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	out[0].Content[0].Text = "modified"
	if in[0].Content[0].Text != "original" {
		t.Errorf("input content changed to %q after modifying output, want 'original'", in[0].Content[0].Text)
	}
}

// VT-009: Transform 深拷贝 ToolCalls 切片，修改输出不影响输入。
func TestDefaultTransformer_DeepCopyToolCalls(t *testing.T) {
	dt := &DefaultTransformer{} // 全部关闭，只测深拷贝
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: "call_123", Name: "search", Arguments: map[string]any{"q": "hello"}},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	out[0].ToolCalls[0].ID = "call_modified"
	if in[0].ToolCalls[0].ID != "call_123" {
		t.Errorf("input ToolCall.ID changed to %q, want 'call_123'", in[0].ToolCalls[0].ID)
	}
}

// VT-010: Transform 深拷贝 Image 指针，修改输出不影响输入。
func TestDefaultTransformer_DeepCopyImage(t *testing.T) {
	dt := &DefaultTransformer{} // 全部关闭，只测深拷贝
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
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	out[0].Content[0].Image.Data = "modified"
	if in[0].Content[0].Image.Data != "base64data" {
		t.Errorf("input Image.Data changed to %q, want 'base64data'", in[0].Content[0].Image.Data)
	}
}

// ========== ToolCallIdClamp 测试 ==========

// VT-011: ToolCallIdClamp 截断过长的 ToolCall ID。
func TestToolCallIdClamp_Truncates(t *testing.T) {
	dt := &DefaultTransformer{ToolCallIdClamp: 16}
	longID := "call_abcdefghijklmnopqrstuvwxyz_0123456789"
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: longID, Name: "search"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	clamped := out[0].ToolCalls[0].ID
	if len(clamped) > 16 {
		t.Errorf("clamped ID length = %d, want <= 16, got %q", len(clamped), clamped)
	}
	// 验证前缀保留
	expectedPrefix := longID[:8] // clamp-8 = 16-8 = 8
	if !strings.HasPrefix(clamped, expectedPrefix) {
		t.Errorf("clamped ID = %q, want prefix %q", clamped, expectedPrefix)
	}
}

// VT-012: ToolCallIdClamp 清理非法字符。
func TestToolCallIdClamp_SanitizesInvalidChars(t *testing.T) {
	dt := &DefaultTransformer{ToolCallIdClamp: 64}
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: "call_abc!@#def", Name: "search"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	clamped := out[0].ToolCalls[0].ID
	// 非法字符应该被替换为下划线
	for _, ch := range clamped {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
			t.Errorf("clamped ID contains invalid char %q in %q", ch, clamped)
		}
	}
}

// VT-013: ToolCallIdClamp 对 Tool 角色消息的 ToolCallID 做截断。
func TestToolCallIdClamp_ClampsToolCallID(t *testing.T) {
	dt := &DefaultTransformer{ToolCallIdClamp: 16}
	longID := "call_abcdefghijklmnopqrstuvwxyz_0123456789"
	in := []message.Message{
		{
			Role: message.RoleTool,
			ToolCallID: longID,
			Content: []message.Content{{Type: message.ContentText, Text: "result"}},
		},
	}
	out, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	clamped := out[0].ToolCallID
	if len(clamped) > 16 {
		t.Errorf("clamped ToolCallID length = %d, want <= 16, got %q", len(clamped), clamped)
	}
}

// VT-014: ToolCallIdClamp 不影响合规短 ID。
func TestToolCallIdClamp_ShortIDPreserved(t *testing.T) {
	dt := &DefaultTransformer{ToolCallIdClamp: 64}
	shortID := "call_abc123"
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: shortID, Name: "search"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].ToolCalls[0].ID != shortID {
		t.Errorf("short ID changed from %q to %q, should be preserved", shortID, out[0].ToolCalls[0].ID)
	}
}

// VT-015: ToolCallIdClamp 截断结果确定性（多次调用结果一致）。
func TestToolCallIdClamp_Deterministic(t *testing.T) {
	dt := &DefaultTransformer{ToolCallIdClamp: 16}
	longID := "call_abcdefghijklmnopqrstuvwxyz_0123456789"
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: longID, Name: "search"},
			},
		},
	}
	out1, _ := dt.Transform(context.Background(), in, "anthropic")
	out2, _ := dt.Transform(context.Background(), in, "anthropic")
	if out1[0].ToolCalls[0].ID != out2[0].ToolCalls[0].ID {
		t.Errorf("non-deterministic clamp: %q vs %q", out1[0].ToolCalls[0].ID, out2[0].ToolCalls[0].ID)
	}
}

// VT-016: clampID 截断格式验证：前缀 + "-" + 7 字符 hash 后缀。
func TestClampID_Format(t *testing.T) {
	longID := "call_abcdefghijklmnopqrstuvwxyz_0123456789"
	clamp := 16
	result := clampID(longID, clamp)

	// 验证长度
	if len(result) != clamp {
		t.Errorf("clampID result length = %d, want %d", len(result), clamp)
	}

	// 验证结构：prefix + "-" + 7-char-hash
	expectedPrefixLen := clamp - 8 // 8
	prefix := longID[:expectedPrefixLen]
	if !strings.HasPrefix(result, prefix+"-") {
		t.Errorf("clampID result = %q, want prefix %q + '-'", result, prefix)
	}

	// 验证 hash 后缀是 hex 编码
	suffix := result[expectedPrefixLen+1:]
	hash := sha256.Sum256([]byte(longID))
	expectedSuffix := hex.EncodeToString(hash[:])[:7]
	if suffix != expectedSuffix {
		t.Errorf("hash suffix = %q, want %q", suffix, expectedSuffix)
	}
}

// VT-017: ToolCallIdClamp=0 时不应用截断。
func TestToolCallIdClamp_DisabledWhenZero(t *testing.T) {
	dt := &DefaultTransformer{ToolCallIdClamp: 0}
	longID := strings.Repeat("a", 100)
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: longID, Name: "search"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].ToolCalls[0].ID != longID {
		t.Errorf("ID was clamped when ToolCallIdClamp=0, got %q", out[0].ToolCalls[0].ID)
	}
}

// ========== ImageFallback 测试 ==========

// VT-018: ImageFallback 对不支持视觉的 provider 替换图片为文本占位符。
func TestImageFallback_ReplacesForNonVisionProvider(t *testing.T) {
	dt := &DefaultTransformer{ImageFallback: true}
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
	out, err := dt.Transform(context.Background(), in, "text-only")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("content type = %v, want ContentText", out[0].Content[0].Type)
	}
	expected := fmt.Sprintf("[Image: %s, %d bytes]", "image/png", len("base64data"))
	if out[0].Content[0].Text != expected {
		t.Errorf("placeholder = %q, want %q", out[0].Content[0].Text, expected)
	}
}

// VT-019: ImageFallback 对空 provider 替换图片为文本占位符。
func TestImageFallback_ReplacesForEmptyProvider(t *testing.T) {
	dt := &DefaultTransformer{ImageFallback: true}
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{
					Type: message.ContentImage,
					Image: &message.Image{Data: "data123", MediaType: "image/jpeg"},
				},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("content type = %v, want ContentText", out[0].Content[0].Type)
	}
}

// VT-020: ImageFallback 不影响支持视觉的 provider（anthropic）。
func TestImageFallback_PreservesForAnthropic(t *testing.T) {
	dt := &DefaultTransformer{ImageFallback: true}
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
	out, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Type != message.ContentImage {
		t.Errorf("content type = %v, want ContentImage for anthropic", out[0].Content[0].Type)
	}
}

// VT-021: ImageFallback 不影响支持视觉的 provider（openai）。
func TestImageFallback_PreservesForOpenAI(t *testing.T) {
	dt := &DefaultTransformer{ImageFallback: true}
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
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Type != message.ContentImage {
		t.Errorf("content type = %v, want ContentImage for openai", out[0].Content[0].Type)
	}
}

// VT-022: ImageFallback=false 时不替换图片。
func TestImageFallback_Disabled(t *testing.T) {
	dt := &DefaultTransformer{ImageFallback: false}
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
	out, err := dt.Transform(context.Background(), in, "text-only")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Type != message.ContentImage {
		t.Errorf("ImageFallback disabled but image was replaced, type = %v", out[0].Content[0].Type)
	}
}

// VT-023: ImageFallback 只替换图片块，保留文本块。
func TestImageFallback_OnlyReplacesImages(t *testing.T) {
	dt := &DefaultTransformer{ImageFallback: true}
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentText, Text: "hello"},
				{Type: message.ContentImage, Image: &message.Image{Data: "data", MediaType: "image/png"}},
				{Type: message.ContentText, Text: "world"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "text-only")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Text != "hello" {
		t.Errorf("first text block changed to %q", out[0].Content[0].Text)
	}
	if out[0].Content[1].Type != message.ContentText {
		t.Errorf("image block not replaced, type = %v", out[0].Content[1].Type)
	}
	if out[0].Content[2].Text != "world" {
		t.Errorf("third text block changed to %q", out[0].Content[2].Text)
	}
}

// ========== ThinkingAdapter 测试 ==========

// VT-024: ThinkingAdapter 对 OpenAI provider 将思维块转为文本。
func TestThinkingAdapter_ConvertsForOpenAI(t *testing.T) {
	dt := &DefaultTransformer{ThinkingAdapter: true}
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "let me think about this"},
				{Type: message.ContentText, Text: "here is my answer"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("thinking block type = %v, want ContentText", out[0].Content[0].Type)
	}
	if out[0].Content[0].Text != "[Thinking] let me think about this" {
		t.Errorf("thinking block text = %q, want '[Thinking] let me think about this'", out[0].Content[0].Text)
	}
	if out[0].Content[1].Text != "here is my answer" {
		t.Errorf("text block changed to %q", out[0].Content[1].Text)
	}
}

// VT-025: ThinkingAdapter 对非 OpenAI provider 保持思维块不变。
func TestThinkingAdapter_PreservesForNonOpenAI(t *testing.T) {
	dt := &DefaultTransformer{ThinkingAdapter: true}
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "hmm"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Type != message.ContentThinking {
		t.Errorf("thinking block type = %v, want ContentThinking for anthropic", out[0].Content[0].Type)
	}
	if out[0].Content[0].Thinking != "hmm" {
		t.Errorf("thinking block content = %q, want 'hmm'", out[0].Content[0].Thinking)
	}
}

// VT-026: ThinkingAdapter=false 时不转换思维块。
func TestThinkingAdapter_Disabled(t *testing.T) {
	dt := &DefaultTransformer{ThinkingAdapter: false}
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "hmm"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out[0].Content[0].Type != message.ContentThinking {
		t.Errorf("ThinkingAdapter disabled but thinking block was converted, type = %v", out[0].Content[0].Type)
	}
}

// ========== 综合测试 ==========

// VT-027: 同时开启所有转换，验证组合效果。
func TestDefaultTransformer_AllTransformationsCombined(t *testing.T) {
	dt := &DefaultTransformer{
		ToolCallIdClamp: 16,
		ImageFallback: true,
		ThinkingAdapter: true,
	}
	longID := "call_abcdefghijklmnopqrstuvwxyz_0123456789"
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "reasoning"},
				{Type: message.ContentImage, Image: &message.Image{Data: "imgdata", MediaType: "image/png"}},
				{Type: message.ContentText, Text: "response"},
			},
			ToolCalls: []message.ToolCall{
				{ID: longID, Name: "search"},
			},
		},
		{
			Role: message.RoleTool,
			ToolCallID: longID,
			Content: []message.Content{{Type: message.ContentText, Text: "result"}},
		},
	}
	// 用 "text-only" provider 触发 image fallback
	// 但 openai 会触发 thinking adapter，用 "openai" 不能触发 image fallback
	// 用 "unknown" provider: image fallback 生效, thinking adapter 不生效
	out, err := dt.Transform(context.Background(), in, "unknown")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	// 消息0: ToolCall ID 应被截断
	if len(out[0].ToolCalls[0].ID) > 16 {
		t.Errorf("ToolCall ID not clamped: %q", out[0].ToolCalls[0].ID)
	}
	// 消息0: 思维块不应被转换（非 openai）
	if out[0].Content[0].Type != message.ContentThinking {
		t.Errorf("thinking block converted for non-openai provider, type = %v", out[0].Content[0].Type)
	}
	// 消息0: 图片应被替换
	if out[0].Content[1].Type != message.ContentText {
		t.Errorf("image block not replaced for non-vision provider, type = %v", out[0].Content[1].Type)
	}
	// 消息0: 文本应保留
	if out[0].Content[2].Text != "response" {
		t.Errorf("text block changed to %q", out[0].Content[2].Text)
	}
	// 消息1: ToolCallID 应被截断
	if len(out[1].ToolCallID) > 16 {
		t.Errorf("ToolCallID not clamped: %q", out[1].ToolCallID)
	}
}

// VT-028: 全部关闭时，Transform 等同于深拷贝直通。
func TestDefaultTransformer_AllDisabledIsDeepCopy(t *testing.T) {
	dt := &DefaultTransformer{} // 全部为零值/false
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "hmm"},
				{Type: message.ContentImage, Image: &message.Image{Data: "data", MediaType: "image/png"}},
				{Type: message.ContentText, Text: "text"},
			},
			ToolCalls: []message.ToolCall{
				{ID: strings.Repeat("x", 100), Name: "tool"},
			},
		},
	}
	out, err := dt.Transform(context.Background(), in, "unknown")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	// 思维块不变
	if out[0].Content[0].Type != message.ContentThinking {
		t.Errorf("thinking block type changed when all disabled")
	}
	// 图片不变
	if out[0].Content[1].Type != message.ContentImage {
		t.Errorf("image block type changed when all disabled")
	}
	// 长ID不变
	if out[0].ToolCalls[0].ID != strings.Repeat("x", 100) {
		t.Errorf("ToolCall ID clamped when all disabled")
	}
}

// VT-029: Transform 不修改输入切片（ToolCallID 场景）。
func TestDefaultTransformer_DoesNotModifyInput_ToolCallID(t *testing.T) {
	dt := &DefaultTransformer{ToolCallIdClamp: 16}
	longID := "call_abcdefghijklmnopqrstuvwxyz_0123456789"
	in := []message.Message{
		{
			Role: message.RoleTool,
			ToolCallID: longID,
			Content: []message.Content{{Type: message.ContentText, Text: "result"}},
		},
	}
	_, err := dt.Transform(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if in[0].ToolCallID != longID {
		t.Errorf("input ToolCallID was modified: got %q, want %q", in[0].ToolCallID, longID)
	}
}

// VT-030: Transform 不修改输入切片（Image 场景）。
func TestDefaultTransformer_DoesNotModifyInput_Image(t *testing.T) {
	dt := &DefaultTransformer{ImageFallback: true}
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
	_, err := dt.Transform(context.Background(), in, "text-only")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if in[0].Content[0].Type != message.ContentImage {
		t.Errorf("input image content type was modified")
	}
}

// VT-031: Transform 不修改输入切片（Thinking 场景）。
func TestDefaultTransformer_DoesNotModifyInput_Thinking(t *testing.T) {
	dt := &DefaultTransformer{ThinkingAdapter: true}
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "original thought"},
			},
		},
	}
	_, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if in[0].Content[0].Type != message.ContentThinking {
		t.Errorf("input thinking content type was modified")
	}
	if in[0].Content[0].Thinking != "original thought" {
		t.Errorf("input thinking content was modified: %q", in[0].Content[0].Thinking)
	}
}

// VT-032: nil 切片消息内容处理。
func TestDefaultTransformer_NilContentAndToolCalls(t *testing.T) {
	dt := NewDefaultTransformer()
	in := []message.Message{
		{Role: message.RoleUser}, // Content 和 ToolCalls 都是 nil
	}
	out, err := dt.Transform(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("output count = %d, want 1", len(out))
	}
	if out[0].Content != nil {
		t.Errorf("expected nil Content for nil input, got %v", out[0].Content)
	}
}

// ========== ToolCallIDNormalizer 测试 ==========

// VT-033: ToolCallIDNormalizer 将纯数字 ID 转为 "tc_{n}" 格式。
func TestToolCallIDNormalizer_NumericID(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: "0", Name: "search"},
				{ID: "1", Name: "lookup"},
			},
		},
	}
	out, err := ToolCallIDNormalizer(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("ToolCallIDNormalizer: %v", err)
	}
	if out[0].ToolCalls[0].ID != "tc_0" {
		t.Errorf("ID[0] = %q, want %q", out[0].ToolCalls[0].ID, "tc_0")
	}
	if out[0].ToolCalls[1].ID != "tc_1" {
		t.Errorf("ID[1] = %q, want %q", out[0].ToolCalls[1].ID, "tc_1")
	}
}

// VT-034: ToolCallIDNormalizer 保留非数字 ID 不变。
func TestToolCallIDNormalizer_NonNumericID(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: "call_abc123", Name: "search"},
			},
		},
	}
	out, err := ToolCallIDNormalizer(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ToolCallIDNormalizer: %v", err)
	}
	if out[0].ToolCalls[0].ID != "call_abc123" {
		t.Errorf("ID = %q, want %q", out[0].ToolCalls[0].ID, "call_abc123")
	}
}

// VT-035: ToolCallIDNormalizer 同步映射 Tool 角色消息的 ToolCallID。
func TestToolCallIDNormalizer_ToolRoleMapping(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
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
	out, err := ToolCallIDNormalizer(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("ToolCallIDNormalizer: %v", err)
	}
	if out[0].ToolCalls[0].ID != "tc_0" {
		t.Errorf("ToolCall ID = %q, want %q", out[0].ToolCalls[0].ID, "tc_0")
	}
	if out[1].ToolCallID != "tc_0" {
		t.Errorf("ToolCallID = %q, want %q", out[1].ToolCallID, "tc_0")
	}
}

// VT-036: ToolCallIDNormalizer 处理 Tool 角色的数字 ToolCallID（无对应 ToolCall）。
func TestToolCallIDNormalizer_ToolRoleNumericIDNoMatch(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleTool,
			ToolCallID: "42",
			Content: []message.Content{{Type: message.ContentText, Text: "result"}},
		},
	}
	out, err := ToolCallIDNormalizer(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ToolCallIDNormalizer: %v", err)
	}
	if out[0].ToolCallID != "tc_42" {
		t.Errorf("ToolCallID = %q, want %q", out[0].ToolCallID, "tc_42")
	}
}

// VT-037: ToolCallIDNormalizer 不修改输入。
func TestToolCallIDNormalizer_DoesNotModifyInput(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: "0", Name: "search"},
			},
		},
	}
	_, err := ToolCallIDNormalizer(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ToolCallIDNormalizer: %v", err)
	}
	if in[0].ToolCalls[0].ID != "0" {
		t.Errorf("input ID was modified: got %q, want %q", in[0].ToolCalls[0].ID, "0")
	}
}

// VT-038: ToolCallIDNormalizer 处理混合 ID 类型。
func TestToolCallIDNormalizer_MixedIDs(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{
				{ID: "0", Name: "search"},
				{ID: "call_abc", Name: "lookup"},
				{ID: "99", Name: "compute"},
			},
		},
	}
	out, err := ToolCallIDNormalizer(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ToolCallIDNormalizer: %v", err)
	}
	if out[0].ToolCalls[0].ID != "tc_0" {
		t.Errorf("ID[0] = %q, want %q", out[0].ToolCalls[0].ID, "tc_0")
	}
	if out[0].ToolCalls[1].ID != "call_abc" {
		t.Errorf("ID[1] = %q, want %q", out[0].ToolCalls[1].ID, "call_abc")
	}
	if out[0].ToolCalls[2].ID != "tc_99" {
		t.Errorf("ID[2] = %q, want %q", out[0].ToolCalls[2].ID, "tc_99")
	}
}

// VT-039: ToolCallIDNormalizer 处理空消息列表。
func TestToolCallIDNormalizer_EmptyMessages(t *testing.T) {
	out, err := ToolCallIDNormalizer(context.Background(), []message.Message{}, "openai")
	if err != nil {
		t.Fatalf("ToolCallIDNormalizer: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("output count = %d, want 0", len(out))
	}
}

// VT-040: isNumericID 测试。
func TestIsNumericID(t *testing.T) {
	tests := []struct {
		input string
		want bool
	}{
		{"0", true},
		{"42", true},
		{"007", true},
		{"", false},
		{"abc", false},
		{"1a", false},
		{"call_123", false},
		{"-1", false},
	}
	for _, tt := range tests {
		got := isNumericID(tt.input)
		if got != tt.want {
			t.Errorf("isNumericID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ========== ImageFormatAdapter 测试 ==========

// VT-041: ImageFormatAdapter 对不支持图片的 provider 替换图片。
func TestImageFormatAdapter_NonVisionProvider(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentImage, Image: &message.Image{Data: "abc", MediaType: "image/png"}},
			},
		},
	}
	out, err := ImageFormatAdapter(context.Background(), in, "text-only")
	if err != nil {
		t.Fatalf("ImageFormatAdapter: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("content type = %v, want ContentText", out[0].Content[0].Type)
	}
	expected := "[Image: image/png, 3 bytes]"
	if out[0].Content[0].Text != expected {
		t.Errorf("placeholder = %q, want %q", out[0].Content[0].Text, expected)
	}
}

// VT-042: ImageFormatAdapter 对支持图片的 provider 保留合规图片。
func TestImageFormatAdapter_VisionProviderAllowedType(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentImage, Image: &message.Image{Data: "abc", MediaType: "image/png"}},
			},
		},
	}
	out, err := ImageFormatAdapter(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ImageFormatAdapter: %v", err)
	}
	if out[0].Content[0].Type != message.ContentImage {
		t.Errorf("content type = %v, want ContentImage", out[0].Content[0].Type)
	}
}

// VT-043: ImageFormatAdapter 对支持图片的 provider 替换不合规 MIME。
func TestImageFormatAdapter_VisionProviderUnsupportedType(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentImage, Image: &message.Image{Data: "abc", MediaType: "image/bmp"}},
			},
		},
	}
	out, err := ImageFormatAdapter(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ImageFormatAdapter: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("content type = %v, want ContentText", out[0].Content[0].Type)
	}
	if out[0].Content[0].Text != "[Unsupported image: image/bmp]" {
		t.Errorf("placeholder = %q, want %q", out[0].Content[0].Text, "[Unsupported image: image/bmp]")
	}
}

// VT-044: ImageFormatAdapter 对 Anthropic 保留合规图片。
func TestImageFormatAdapter_AnthropicAllowedType(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentImage, Image: &message.Image{Data: "abc", MediaType: "image/jpeg"}},
			},
		},
	}
	out, err := ImageFormatAdapter(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("ImageFormatAdapter: %v", err)
	}
	if out[0].Content[0].Type != message.ContentImage {
		t.Errorf("content type = %v, want ContentImage", out[0].Content[0].Type)
	}
}

// VT-045: ImageFormatAdapter 不修改输入。
func TestImageFormatAdapter_DoesNotModifyInput(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentImage, Image: &message.Image{Data: "abc", MediaType: "image/png"}},
			},
		},
	}
	_, err := ImageFormatAdapter(context.Background(), in, "text-only")
	if err != nil {
		t.Fatalf("ImageFormatAdapter: %v", err)
	}
	if in[0].Content[0].Type != message.ContentImage {
		t.Errorf("input content type was modified")
	}
}

// VT-046: ImageFormatAdapter 混合文本和图片内容。
func TestImageFormatAdapter_MixedContent(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleUser,
			Content: []message.Content{
				{Type: message.ContentText, Text: "hello"},
				{Type: message.ContentImage, Image: &message.Image{Data: "data", MediaType: "image/bmp"}},
				{Type: message.ContentText, Text: "world"},
			},
		},
	}
	out, err := ImageFormatAdapter(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ImageFormatAdapter: %v", err)
	}
	if out[0].Content[0].Text != "hello" {
		t.Errorf("first text block = %q, want %q", out[0].Content[0].Text, "hello")
	}
	if out[0].Content[1].Type != message.ContentText {
		t.Errorf("unsupported image not replaced, type = %v", out[0].Content[1].Type)
	}
	if out[0].Content[2].Text != "world" {
		t.Errorf("second text block = %q, want %q", out[0].Content[2].Text, "world")
	}
}

// VT-047: isAllowedImageType 测试。
func TestIsAllowedImageType(t *testing.T) {
	allowed := []string{"image/png", "image/jpeg", "image/gif", "image/webp"}
	tests := []struct {
		mediaType string
		want bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"image/bmp", false},
		{"image/svg+xml", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isAllowedImageType(tt.mediaType, allowed)
		if got != tt.want {
			t.Errorf("isAllowedImageType(%q) = %v, want %v", tt.mediaType, got, tt.want)
		}
	}
}

// ========== ThinkingBlockAdapterEnhanced 测试 ==========

// VT-048: ThinkingBlockAdapterEnhanced 对 OpenAI 将思维块转为文本。
func TestThinkingBlockAdapterEnhanced_OpenAI(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "let me think about this"},
				{Type: message.ContentText, Text: "here is my answer"},
			},
		},
	}
	out, err := ThinkingBlockAdapterEnhanced(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapterEnhanced: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("thinking block type = %v, want ContentText", out[0].Content[0].Type)
	}
	if out[0].Content[0].Text != "[Thinking] let me think about this" {
		t.Errorf("thinking text = %q, want %q", out[0].Content[0].Text, "[Thinking] let me think about this")
	}
	if out[0].Content[1].Text != "here is my answer" {
		t.Errorf("text block = %q, want %q", out[0].Content[1].Text, "here is my answer")
	}
}

// VT-049: ThinkingBlockAdapterEnhanced 对 Anthropic 将文本前缀转回思维块。
func TestThinkingBlockAdapterEnhanced_Anthropic(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentText, Text: "[Thinking] let me think about this"},
				{Type: message.ContentText, Text: "here is my answer"},
			},
		},
	}
	out, err := ThinkingBlockAdapterEnhanced(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapterEnhanced: %v", err)
	}
	if out[0].Content[0].Type != message.ContentThinking {
		t.Errorf("content type = %v, want ContentThinking", out[0].Content[0].Type)
	}
	if out[0].Content[0].Thinking != "let me think about this" {
		t.Errorf("thinking = %q, want %q", out[0].Content[0].Thinking, "let me think about this")
	}
	if out[0].Content[1].Text != "here is my answer" {
		t.Errorf("text block = %q, want %q", out[0].Content[1].Text, "here is my answer")
	}
}

// VT-050: ThinkingBlockAdapterEnhanced 对其他 provider 不做转换。
func TestThinkingBlockAdapterEnhanced_Passthrough(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "hmm"},
			},
		},
	}
	out, err := ThinkingBlockAdapterEnhanced(context.Background(), in, "gemini")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapterEnhanced: %v", err)
	}
	if out[0].Content[0].Type != message.ContentThinking {
		t.Errorf("thinking block type = %v, want ContentThinking for passthrough", out[0].Content[0].Type)
	}
	if out[0].Content[0].Thinking != "hmm" {
		t.Errorf("thinking = %q, want %q", out[0].Content[0].Thinking, "hmm")
	}
}

// VT-051: ThinkingBlockAdapterEnhanced 不修改输入。
func TestThinkingBlockAdapterEnhanced_DoesNotModifyInput(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentThinking, Thinking: "original thought"},
			},
		},
	}
	_, err := ThinkingBlockAdapterEnhanced(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapterEnhanced: %v", err)
	}
	if in[0].Content[0].Type != message.ContentThinking {
		t.Errorf("input content type was modified")
	}
	if in[0].Content[0].Thinking != "original thought" {
		t.Errorf("input thinking was modified: %q", in[0].Content[0].Thinking)
	}
}

// VT-052: ThinkingBlockAdapterEnhanced Anthropic 不转换非 "[Thinking] " 前缀的文本。
func TestThinkingBlockAdapterEnhanced_AnthropicNonPrefixedText(t *testing.T) {
	in := []message.Message{
		{
			Role: message.RoleAssistant,
			Content: []message.Content{
				{Type: message.ContentText, Text: "regular text without prefix"},
			},
		},
	}
	out, err := ThinkingBlockAdapterEnhanced(context.Background(), in, "anthropic")
	if err != nil {
		t.Fatalf("ThinkingBlockAdapterEnhanced: %v", err)
	}
	if out[0].Content[0].Type != message.ContentText {
		t.Errorf("non-prefixed text was converted, type = %v", out[0].Content[0].Type)
	}
	if out[0].Content[0].Text != "regular text without prefix" {
		t.Errorf("text = %q, want %q", out[0].Content[0].Text, "regular text without prefix")
	}
}

// VT-053: classifyThinkingProvider 测试。
func TestClassifyThinkingProvider(t *testing.T) {
	tests := []struct {
		provider string
		want thinkingProviderConvention
	}{
		{"openai", thinkingOpenAI},
		{"OpenAI", thinkingOpenAI},
		{"anthropic", thinkingAnthropic},
		{"anthropic-v3", thinkingAnthropic},
		{"gemini", thinkingPassthrough},
		{"", thinkingPassthrough},
	}
	for _, tt := range tests {
		got := classifyThinkingProvider(tt.provider)
		if got != tt.want {
			t.Errorf("classifyThinkingProvider(%q) = %v, want %v", tt.provider, got, tt.want)
		}
	}
}

// ========== SystemMessageAdapter 测试 ==========

// VT-054: SystemMessageAdapter 对支持 system role 的 provider 保留系统消息。
func TestSystemMessageAdapter_SupportsSystemRole(t *testing.T) {
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "you are helpful"),
		message.NewTextMessage(message.RoleUser, "hello"),
	}
	out, err := SystemMessageAdapter(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("output count = %d, want 2", len(out))
	}
	if out[0].Role != message.RoleSystem {
		t.Errorf("first message role = %v, want RoleSystem", out[0].Role)
	}
	if out[0].Content[0].Text != "you are helpful" {
		t.Errorf("system text = %q, want %q", out[0].Content[0].Text, "you are helpful")
	}
}

// VT-055: SystemMessageAdapter 移除空系统消息（支持 system role 的 provider）。
func TestSystemMessageAdapter_RemovesEmptySystem(t *testing.T) {
	in := []message.Message{
		{Role: message.RoleSystem}, // 空 Content
		message.NewTextMessage(message.RoleUser, "hello"),
	}
	out, err := SystemMessageAdapter(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("output count = %d, want 1 (empty system removed)", len(out))
	}
	if out[0].Role != message.RoleUser {
		t.Errorf("role = %v, want RoleUser", out[0].Role)
	}
}

// VT-056: SystemMessageAdapter 移除仅含空文本的系统消息。
func TestSystemMessageAdapter_RemovesWhitespaceOnlySystem(t *testing.T) {
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, " "),
		message.NewTextMessage(message.RoleUser, "hello"),
	}
	out, err := SystemMessageAdapter(context.Background(), in, "openai")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("output count = %d, want 1", len(out))
	}
}

// VT-057: SystemMessageAdapter 对 Ollama 将系统消息前置到用户消息。
func TestSystemMessageAdapter_OllamaMerge(t *testing.T) {
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "you are helpful"),
		message.NewTextMessage(message.RoleUser, "hello"),
	}
	out, err := SystemMessageAdapter(context.Background(), in, "ollama")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("output count = %d, want 1", len(out))
	}
	if out[0].Role != message.RoleUser {
		t.Errorf("role = %v, want RoleUser", out[0].Role)
	}
	if len(out[0].Content) != 2 {
		t.Fatalf("content count = %d, want 2", len(out[0].Content))
	}
	if out[0].Content[0].Text != "you are helpful" {
		t.Errorf("system text = %q, want %q", out[0].Content[0].Text, "you are helpful")
	}
	if out[0].Content[1].Text != "hello" {
		t.Errorf("user text = %q, want %q", out[0].Content[1].Text, "hello")
	}
}

// VT-058: SystemMessageAdapter 对 Groq 将系统消息前置到用户消息。
func TestSystemMessageAdapter_GroqMerge(t *testing.T) {
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "be concise"),
		message.NewTextMessage(message.RoleUser, "explain Go"),
	}
	out, err := SystemMessageAdapter(context.Background(), in, "groq")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("output count = %d, want 1", len(out))
	}
	if out[0].Content[0].Text != "be concise" {
		t.Errorf("system text = %q, want %q", out[0].Content[0].Text, "be concise")
	}
}

// VT-059: SystemMessageAdapter 无用户消息时系统消息变为用户消息。
func TestSystemMessageAdapter_NoUserMessage(t *testing.T) {
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "you are helpful"),
		message.NewTextMessage(message.RoleAssistant, "hi"),
	}
	out, err := SystemMessageAdapter(context.Background(), in, "ollama")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("output count = %d, want 2", len(out))
	}
	if out[0].Role != message.RoleUser {
		t.Errorf("first role = %v, want RoleUser", out[0].Role)
	}
	if out[0].Content[0].Text != "you are helpful" {
		t.Errorf("system text = %q, want %q", out[0].Content[0].Text, "you are helpful")
	}
}

// VT-060: SystemMessageAdapter 不修改输入。
func TestSystemMessageAdapter_DoesNotModifyInput(t *testing.T) {
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "you are helpful"),
		message.NewTextMessage(message.RoleUser, "hello"),
	}
	_, err := SystemMessageAdapter(context.Background(), in, "ollama")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if in[0].Role != message.RoleSystem {
		t.Errorf("input system message role was modified")
	}
	if len(in) != 2 {
		t.Errorf("input count changed to %d, want 2", len(in))
	}
}

// VT-061: SystemMessageAdapter 多条系统消息合并。
func TestSystemMessageAdapter_MultipleSystemMessages(t *testing.T) {
	in := []message.Message{
		message.NewTextMessage(message.RoleSystem, "rule 1"),
		message.NewTextMessage(message.RoleSystem, "rule 2"),
		message.NewTextMessage(message.RoleUser, "hello"),
	}
	out, err := SystemMessageAdapter(context.Background(), in, "ollama")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("output count = %d, want 1", len(out))
	}
	// 两条系统消息应合并为一个内容块
	if out[0].Content[0].Text != "rule 1\n\nrule 2" {
		t.Errorf("merged system text = %q, want %q", out[0].Content[0].Text, "rule 1\n\nrule 2")
	}
	if out[0].Content[1].Text != "hello" {
		t.Errorf("user text = %q, want %q", out[0].Content[1].Text, "hello")
	}
}

// VT-062: SystemMessageAdapter 无系统消息时不影响消息列表。
func TestSystemMessageAdapter_NoSystemMessages(t *testing.T) {
	in := []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
		message.NewTextMessage(message.RoleAssistant, "hi"),
	}
	out, err := SystemMessageAdapter(context.Background(), in, "ollama")
	if err != nil {
		t.Fatalf("SystemMessageAdapter: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("output count = %d, want 2", len(out))
	}
}

// VT-063: isEmptyMessage 测试。
func TestIsEmptyMessage(t *testing.T) {
	tests := []struct {
		name string
		msg message.Message
		want bool
	}{
		{"nil content", message.Message{Role: message.RoleSystem}, true},
		{"empty content", message.Message{Role: message.RoleSystem, Content: []message.Content{}}, true},
		{"empty text", message.Message{Role: message.RoleSystem, Content: []message.Content{{Type: message.ContentText, Text: ""}}}, true},
		{"whitespace text", message.Message{Role: message.RoleSystem, Content: []message.Content{{Type: message.ContentText, Text: " "}}}, true},
		{"non-empty text", message.Message{Role: message.RoleSystem, Content: []message.Content{{Type: message.ContentText, Text: "hello"}}}, false},
		{"with image", message.Message{Role: message.RoleSystem, Content: []message.Content{{Type: message.ContentImage, Image: &message.Image{Data: "a"}}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyMessage(tt.msg)
			if got != tt.want {
				t.Errorf("isEmptyMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}
