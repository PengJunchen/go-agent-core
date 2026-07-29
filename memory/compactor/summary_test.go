package compactor

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// ---------------------------------------------------------------------------
// mockModelProvider — 用于测试的 ModelProvider mock
//
// 支持捕获最近一次调用的入参（lastMessages / lastOpts），用于
// AC-3.3/3.4/3.5 的参数断言。
// ---------------------------------------------------------------------------

type mockModelProvider struct {
	genResp *message.Message
	genErr error
	genCalls int
	lastMessages []message.Message // 最近一次 Generate 调用的 messages 入参
	lastOpts *provider.ChatOptions // 最近一次 Generate 调用的 opts 入参
	mu sync.Mutex
}

func (m *mockModelProvider) StreamChat(ctx context.Context, messages []message.Message, opts *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, nil
}

func (m *mockModelProvider) Generate(ctx context.Context, messages []message.Message, opts *provider.ChatOptions) (*message.Message, error) {
	m.mu.Lock()
	m.genCalls++
	m.lastMessages = messages
	m.lastOpts = opts
	m.mu.Unlock()
	if m.genErr != nil {
		return nil, m.genErr
	}
	return m.genResp, nil
}

func (m *mockModelProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{
		Provider: "mock",
		ModelName: "mock-model",
	}
}

// newSummaryItems 构造一个测试用对话历史，token 数足够触发压缩。
func newSummaryItems() []memctx.TurnItem {
	return []memctx.TurnItem{
		{Role: "system", Content: "system instruction"},
		{Role: "user", Content: strings.Repeat("user-question-part-", 50)},
		{Role: "assistant", Content: strings.Repeat("assistant-answer-part-", 50)},
		{Role: "user", Content: "short follow-up"},
		{Role: "assistant", Content: "short reply"},
	}
}

// ---------------------------------------------------------------------------
// AC: SummaryCompactor 实现 Compactor 接口
// ---------------------------------------------------------------------------

func TestSummaryCompactor_Interface(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "summary"},
		},
	}}
	var _ memctx.Compactor = NewSummaryCompactor(mock, nil)
}

// ---------------------------------------------------------------------------
// AC: 基本压缩 —— LLM 生成摘要，items 减少，token 数下降
// Also covers AC-3.3/3.4/3.5 (mock param capture),
// AC-3.6 (BeforeTokens > AfterTokens), AC-3.7 (Summary == item content)
// ---------------------------------------------------------------------------

func TestSummaryCompactor_BasicCompact(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "summary of the conversation so far"},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil)
	est := &HeuristicTokenEstimator{}
	items := newSummaryItems()

	maxTokens := est.EstimateFromItems(items) / 2 // 强制压缩

	result, err := compactor.Compact(context.Background(), items, maxTokens)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// 策略应是 summary。
	if result.Strategy != memctx.CompactSummary {
		t.Errorf("expected strategy %s, got %s", memctx.CompactSummary, result.Strategy)
	}

	// AC-3.6: BeforeTokens > AfterTokens（非退化场景）。
	if result.BeforeTokens <= result.AfterTokens {
		t.Errorf("BeforeTokens (%d) should be > AfterTokens (%d) in non-degenerate case",
			result.BeforeTokens, result.AfterTokens)
	}

	// after tokens 不应超过 maxTokens。
	if result.AfterTokens > maxTokens {
		t.Errorf("after tokens %d > maxTokens %d", result.AfterTokens, maxTokens)
	}

	// retained items 数量应少于原 items（有压缩）。
	if len(result.RetainedItems) >= len(items) {
		t.Errorf("retained %d items, expected fewer than %d", len(result.RetainedItems), len(items))
	}

	// AC-3.7: Result.Summary == summaryItem.Content（审计可追溯）。
	summaryText := "summary of the conversation so far"
	if result.Summary != summaryText {
		t.Errorf("Result.Summary = %q, want %q", result.Summary, summaryText)
	}

	// 摘要文本应出现在 RetainedItems 中。
	foundSummary := false
	for _, item := range result.RetainedItems {
		if item.Role == "system" && item.Content == summaryText {
			foundSummary = true
			// 验证 Metadata.compacted
			if item.Metadata == nil || item.Metadata["compacted"] != true {
				t.Error("summary item Metadata.compacted should be true")
			}
			break
		}
	}
	if !foundSummary {
		t.Error("expected summary text in retained items")
	}

	if mock.genCalls != 1 {
		t.Errorf("mock genCalls = %d, want 1", mock.genCalls)
	}

	// AC-3.3: 验证 mock 捕获的入参 message 是 RoleUser 且含格式化对话文本。
	if len(mock.lastMessages) < 1 {
		t.Fatal("expected at least 1 message sent to Generate")
	}
	if mock.lastMessages[0].Role != message.RoleUser {
		t.Errorf("LLM input message role = %s, want %s", mock.lastMessages[0].Role, message.RoleUser)
	}
	if len(mock.lastMessages[0].Content) == 0 {
		t.Error("LLM input message has no content")
	}
	// 验证 content 包含格式化对话文本（基于 head items 的内容）。
	// head 包含系统项之前的对话项（tail 之后的不会被摘要）。
	firstMsgContent := ""
	for _, c := range mock.lastMessages[0].Content {
		if c.Type == message.ContentText {
			firstMsgContent = c.Text
			break
		}
	}
	if !strings.Contains(firstMsgContent, "system instruction") {
		t.Error("LLM prompt should contain conversation text from items")
	}
	if !strings.Contains(firstMsgContent, "user-question-part-") {
		t.Error("LLM prompt should contain head items that are summarized")
	}

	// AC-3.4: Temperature <= 0.3。
	if mock.lastOpts == nil {
		t.Fatal("expected non-nil ChatOptions")
	}
	if mock.lastOpts.Temperature == nil {
		t.Fatal("expected non-nil Temperature")
	}
	if *mock.lastOpts.Temperature > 0.3 {
		t.Errorf("Temperature = %f, want <= 0.3", *mock.lastOpts.Temperature)
	}

	// AC-3.5: Tools 为空。
	if len(mock.lastOpts.Tools) > 0 {
		t.Errorf("expected empty Tools, got %d tools", len(mock.lastOpts.Tools))
	}
}

// ---------------------------------------------------------------------------
// AC: 文件操作被追踪并注入摘要元数据
// ---------------------------------------------------------------------------

func TestSummaryCompactor_FileOps(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "summary with file ops"},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil)
	est := &HeuristicTokenEstimator{}

	items := []memctx.TurnItem{
		{Role: "system", Content: "system instruction"},
		{Role: "user", Content: strings.Repeat("question ", 40)},
		{Role: "assistant", Content: strings.Repeat("answer ", 40), ToolCalls: []memctx.ToolCallRef{
			{ID: "call_1", Name: "read_file", Arguments: map[string]any{"path": "/tmp/a.go"}},
		}},
		{Role: "tool", Content: "file content", ToolCallID: "call_1", ToolName: "read_file", Metadata: map[string]any{
			"args": `{"path":"/tmp/a.go"}`,
		}},
		{Role: "user", Content: "short follow-up"},
		{Role: "assistant", Content: "short reply"},
	}

	maxTokens := est.EstimateFromItems(items) / 2

	result, err := compactor.Compact(context.Background(), items, maxTokens)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// 找摘要项中的 file_ops 元数据。
	var fileOps any
	for _, item := range result.RetainedItems {
		if item.Metadata != nil {
			if v, ok := item.Metadata["file_ops"]; ok {
				fileOps = v
			}
		}
	}
	if fileOps == nil {
		t.Fatal("expected file_ops metadata in summary item")
	}
}

// ---------------------------------------------------------------------------
// AC: 分裂轮次处理 —— 无孤立的工具调用/结果对（VC-002）
// ---------------------------------------------------------------------------

func TestSummaryCompactor_SplitTurn(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "summary"},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil)
	est := &HeuristicTokenEstimator{}

	items := []memctx.TurnItem{
		{Role: "system", Content: "system instruction"},
		{Role: "user", Content: strings.Repeat("long-question ", 30)},
		{Role: "assistant", Content: strings.Repeat("long-answer ", 30)},
		// 尾部若从这里开始，需包含其后 tool result。
		{Role: "assistant", Content: "calling tool now", ToolCalls: []memctx.ToolCallRef{
			{ID: "call_tail", Name: "read_file", Arguments: map[string]any{"path": "/tmp/x.go"}},
		}},
		{Role: "tool", Content: "tool result", ToolCallID: "call_tail", ToolName: "read_file"},
		{Role: "user", Content: "final question"},
		{Role: "assistant", Content: "final answer"},
	}

	maxTokens := est.EstimateFromItems(items) / 2

	result, err := compactor.Compact(context.Background(), items, maxTokens)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// 验证无孤立的 tool result：每个 RoleTool 必须有前驱 RoleAssistant
	// 包含匹配的 ToolCall。
	for i, item := range result.RetainedItems {
		if item.Role != "tool" {
			continue
		}
		found := false
		for j := i - 1; j >= 0; j-- {
			if result.RetainedItems[j].Role != "assistant" {
				continue
			}
			for _, tc := range result.RetainedItems[j].ToolCalls {
				if tc.ID == item.ToolCallID {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("orphaned tool result at index %d: ToolCallID=%q has no matching assistant ToolCall",
				i, item.ToolCallID)
		}
	}
}

// ---------------------------------------------------------------------------
// AC: LLM 调用失败时返回错误
// ---------------------------------------------------------------------------

func TestSummaryCompactor_LLMFailure(t *testing.T) {
	mock := &mockModelProvider{
		genErr: context.DeadlineExceeded,
	}

	compactor := NewSummaryCompactor(mock, nil)
	est := &HeuristicTokenEstimator{}
	items := newSummaryItems()
	maxTokens := est.EstimateFromItems(items) / 2

	_, err := compactor.Compact(context.Background(), items, maxTokens)
	if err == nil {
		t.Fatal("expected error when LLM fails")
	}
}

// ---------------------------------------------------------------------------
// AC: 上下文取消传播
// ---------------------------------------------------------------------------

func TestSummaryCompactor_ContextCancel(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "summary"},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil)
	est := &HeuristicTokenEstimator{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调用前取消

	items := newSummaryItems()
	maxTokens := est.EstimateFromItems(items) / 2

	_, err := compactor.Compact(ctx, items, maxTokens)
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

// ---------------------------------------------------------------------------
// AC: 并发调用无竞态（VS-001）
// ---------------------------------------------------------------------------

func TestSummaryCompactor_Concurrent(t *testing.T) {
	est := &HeuristicTokenEstimator{}
	items := newSummaryItems()
	maxTokens := est.EstimateFromItems(items) / 2

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			// 每个 goroutine 用自己的 mock，避免 genCalls 竞态。
			mock := &mockModelProvider{genResp: &message.Message{
				Role: message.RoleAssistant,
				Content: []message.Content{
					{Type: message.ContentText, Text: "summary"},
				},
			}}
			compactor := NewSummaryCompactor(mock, nil)
			_, errs[i] = compactor.Compact(context.Background(), items, maxTokens)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d error: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// AC: 空 items 或 maxTokens ≤ 0 不崩溃
// ---------------------------------------------------------------------------

func TestSummaryCompactor_EdgeCases(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "summary"},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil)

	// 空 items。
	result, err := compactor.Compact(context.Background(), nil, 100)
	if err != nil {
		t.Fatalf("Compact empty: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for empty items")
	}

	// maxTokens ≤ 0。
	items := newSummaryItems()
	result, err = compactor.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("Compact maxTokens=0: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for maxTokens=0")
	}

	// 仅系统项。
	sysOnly := []memctx.TurnItem{
		{Role: "system", Content: "only system"},
	}
	result, err = compactor.Compact(context.Background(), sysOnly, 10)
	if err != nil {
		t.Fatalf("Compact system only: %v", err)
	}
	if result.BeforeTokens != result.AfterTokens {
		t.Errorf("system-only: before=%d after=%d, should be equal", result.BeforeTokens, result.AfterTokens)
	}
}

// ---------------------------------------------------------------------------
// AC: SummaryCompactor 使用默认估算器
// ---------------------------------------------------------------------------

func TestSummaryCompactor_DefaultEstimator(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "summary"},
		},
	}}
	// 不传 est，应使用默认 HeuristicTokenEstimator。
	compactor := NewSummaryCompactor(mock, nil)
	items := newSummaryItems()

	// 极小的 maxTokens，强制压缩。
	result, err := compactor.Compact(context.Background(), items, 5)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// 至少应有系统项 + 摘要项 + 尾部。
	if len(result.RetainedItems) < 2 {
		t.Errorf("expected at least 2 retained items (system+summary), got %d", len(result.RetainedItems))
	}
}

// ---------------------------------------------------------------------------
// AC-6.6: 端到端集成测试 — CompactResult.Summary == mockReturnText
// ---------------------------------------------------------------------------

func TestSummaryCompactor_CompactResultSummaryEqualsMockText(t *testing.T) {
	mockSummary := "this is my mock summary text for verification"
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: mockSummary},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil)
	est := &HeuristicTokenEstimator{}
	items := newSummaryItems()

	maxTokens := est.EstimateFromItems(items) / 2

	result, err := compactor.Compact(context.Background(), items, maxTokens)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// AC-6.6: CompactResult.Summary == mockReturnText
	if result.Summary != mockSummary {
		t.Errorf("Result.Summary = %q, want %q", result.Summary, mockSummary)
	}

	// AC-3.7: 审计可追溯 — Summary 字段 = 摘要 item 的 Content
	foundSummaryItem := false
	for _, item := range result.RetainedItems {
		if item.Role == "system" && item.Content == mockSummary {
			foundSummaryItem = true
			break
		}
	}
	if !foundSummaryItem {
		t.Error("expected summary text in RetainedItems as system item")
	}
}

// ---------------------------------------------------------------------------
// AC-6.4: RecordItem → GetMessages 返回完整历史（端到端闭环）
// AC-6.5: RecordItem → Compact(Summary) → GetMessages 返回摘要 + tail
// ---------------------------------------------------------------------------

func TestHeuristicContextManager_E2E_RecordCompactGet(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "e2e summary text"},
		},
	}}
	// 通过 Compactor 接口注入 SummaryCompactor
	summaryCompactor := NewSummaryCompactor(mock, &HeuristicTokenEstimator{})

	ctx := context.Background()
	// 用较小的 maxTokens 确保 Compact 实际执行
	m := memctx.NewHeuristicContextManager(
		memctx.WithSummaryCompactor(summaryCompactor),
		memctx.WithTruncatingCompactor(summaryCompactor), // fallback
		memctx.WithMaxTokens(100),
	)

	// 设置初始上下文
	_ = m.SetInitialContext(ctx, []memctx.TurnItem{
		{Role: "system", Content: "You are a test assistant"},
	})

	// Record 大量条目，确保总 token 超过 maxTokens（100），触发压缩
	for i := 0; i < 5; i++ {
		_ = m.RecordItem(ctx, memctx.TurnItem{Role: "user",
			Content: strings.Repeat("long question content here ", 10)})
		_ = m.RecordItem(ctx, memctx.TurnItem{Role: "assistant",
			Content: strings.Repeat("long answer content here ", 10)})
	}

	// AC-6.4: RecordItem → GetMessages 返回完整历史
	msgs, err := m.GetMessages(ctx, nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	// system + 10 recorded
	if len(msgs) != 11 {
		t.Errorf("expected 11 items before compact, got %d", len(msgs))
	}

	// AC-6.5: RecordItem → Compact(Summary) → GetMessages 返回摘要 + tail
	result, err := m.Compact(ctx, memctx.CompactSummary)
	if err != nil {
		t.Fatalf("Compact(Summary): %v", err)
	}

	if result.Strategy != memctx.CompactSummary {
		t.Errorf("expected CompactSummary strategy, got %s", result.Strategy)
	}
	if result.Summary != "e2e summary text" {
		t.Errorf("Result.Summary = %q, want %q", result.Summary, "e2e summary text")
	}

	// GetMessages 返回压缩后的结果
	afterMsgs, err := m.GetMessages(ctx, nil)
	if err != nil {
		t.Fatalf("GetMessages after compact: %v", err)
	}
	if len(afterMsgs) == 0 {
		t.Fatal("expected non-empty messages after compact")
	}
	if len(afterMsgs) >= len(msgs) {
		t.Errorf("after compact: got %d items, should be fewer than before (%d)",
			len(afterMsgs), len(msgs))
	}

	// 摘要项的内容应包含 mock 返回文本
	found := false
	for _, item := range afterMsgs {
		if item.Content == "e2e summary text" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected summary text in messages after compact")
	}
}

// ---------------------------------------------------------------------------
// AC: WithTemperature / WithSummaryPrompt / WithMaxTokens builder 方法
// ---------------------------------------------------------------------------

func TestSummaryCompactor_BuilderMethods(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "builder test"},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil).
		WithTemperature(0.1).
		WithSummaryPrompt("Custom prompt: {conversation_text}").
		WithMaxTokens(512)

	items := newSummaryItems()
	est := &HeuristicTokenEstimator{}
	maxTokens := est.EstimateFromItems(items) / 2

	_, err := compactor.Compact(context.Background(), items, maxTokens)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// 验证 builder 设置的值通过 mock 入参反映
	if mock.lastOpts == nil || mock.lastOpts.Temperature == nil {
		t.Fatal("expected Temperature in ChatOptions")
	}
	if *mock.lastOpts.Temperature != 0.1 {
		t.Errorf("Temperature = %f, want 0.1", *mock.lastOpts.Temperature)
	}
	if mock.lastOpts.MaxTokens == nil || *mock.lastOpts.MaxTokens != 512 {
		t.Errorf("MaxTokens = %v, want 512", mock.lastOpts.MaxTokens)
	}
}
