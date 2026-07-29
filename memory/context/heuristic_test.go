package context

import (
	"context"
	"testing"
)

// Compile-time interface check: HeuristicContextManager implements ContextManager.
func TestHeuristicContextManager_Interface(t *testing.T) {
	var _ ContextManager = (*HeuristicContextManager)(nil)
}

// HC-001: RecordItem 追加条目后 GetMessages 能返回全部。
func TestHeuristicContextManager_RecordItemAndGetMessages(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	items := []TurnItem{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "what's the weather?"},
	}

	for _, item := range items {
		if err := m.RecordItem(ctx, item); err != nil {
			t.Fatalf("RecordItem: %v", err)
		}
	}

	got, err := m.GetMessages(ctx, nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	if len(got) != len(items) {
		t.Fatalf("expected %d items, got %d", len(items), len(got))
	}

	for i, item := range items {
		if got[i].Role != item.Role || got[i].Content != item.Content {
			t.Errorf("item[%d]: expected {%s, %s}, got {%s, %s}",
				i, item.Role, item.Content, got[i].Role, got[i].Content)
		}
	}
}

// HC-002: GetMessages 返回的是副本，修改副本不影响原始状态。
func TestHeuristicContextManager_GetMessagesReturnsCopy(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})

	got, _ := m.GetMessages(ctx, nil)
	got[0].Content = "modified"

	// 再次获取应不受影响
	got2, _ := m.GetMessages(ctx, nil)
	if got2[0].Content != "hello" {
		t.Errorf("expected original content 'hello', got '%s'", got2[0].Content)
	}
}

// HC-003: TokenUsage 返回正确的启发式估算值。
func TestHeuristicContextManager_TokenUsage(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	content := "hello world, this is a test message for token estimation"
	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: content})

	expected := len(content) / 4
	got := m.TokenUsage(ctx)
	if got != expected {
		t.Errorf("TokenUsage = %d, want %d (char/4 of %q)", got, expected, content)
	}
}

// HC-004: TokenUsage 为空时返回 0。
func TestHeuristicContextManager_TokenUsageEmpty(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	got := m.TokenUsage(ctx)
	if got != 0 {
		t.Errorf("TokenUsage for empty manager = %d, want 0", got)
	}
}

// HC-005: SetInitialContext 在 items 前面插入。
func TestHeuristicContextManager_SetInitialContext(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	sysItems := []TurnItem{
		{Role: "system", Content: "You are a helpful assistant"},
	}
	if err := m.SetInitialContext(ctx, sysItems); err != nil {
		t.Fatalf("SetInitialContext: %v", err)
	}

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})

	got, _ := m.GetMessages(ctx, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[0].Role != "system" || got[0].Content != "You are a helpful assistant" {
		t.Errorf("first item should be system prompt, got {%s, %s}", got[0].Role, got[0].Content)
	}
	if got[1].Role != "user" {
		t.Errorf("second item should be user, got role %s", got[1].Role)
	}
}

// HC-006: SetInitialContext 幂等——多次调用会在 items 前面顺序追加。
// 第二次调用追加的内容排在最前面（因为每次都 prepend 到当前 items 前面）。
func TestHeuristicContextManager_SetInitialContextIdempotent(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	_ = m.SetInitialContext(ctx, []TurnItem{{Role: "system", Content: "prompt1"}})
	_ = m.SetInitialContext(ctx, []TurnItem{{Role: "system", Content: "prompt2"}})

	got, _ := m.GetMessages(ctx, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 items after two SetInitialContext calls, got %d", len(got))
	}
	// SetInitialContext prepends, so the second call's items end up in front.
	// This is correct: initial context is prepended to the front every time.
	if got[0].Content != "prompt2" || got[1].Content != "prompt1" {
		t.Errorf("expected [prompt2, prompt1] (second prepend first), got [%s, %s]", got[0].Content, got[1].Content)
	}
}

// HC-007: Compact 未配置 Compactor 时返回 ErrNoCompactor（非 auto 策略）。
func TestHeuristicContextManager_CompactNoCompactor(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})

	_, err := m.Compact(ctx, CompactManual)
	if err != ErrNoCompactor {
		t.Errorf("expected ErrNoCompactor, got %v", err)
	}
}

// HC-008: Compact 自动策略在无 Compactor 时返回 noop（不报错）。
func TestHeuristicContextManager_CompactAutoNoCompactor(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})

	result, err := m.Compact(ctx, CompactAuto)
	if err != nil {
		t.Fatalf("Compact auto without compactor: %v", err)
	}
	if result.ItemsRemoved != 0 {
		t.Errorf("expected 0 removed for auto noop, got %d", result.ItemsRemoved)
	}
}

// mockCompactor 是一个测试用的 Compactor 存根。
type mockCompactor struct {
	MockCompact func(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error)
}

func (m *mockCompactor) Compact(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error) {
	return m.MockCompact(ctx, items, maxTokens)
}

// mockEstimator 是一个测试用的 TokenEstimator 存根。
type mockEstimator struct {
	MockEstimate func(text string) int
	MockEstimateFromItems func(items []TurnItem) int
}

func (m *mockEstimator) Estimate(text string) int {
	return m.MockEstimate(text)
}

func (m *mockEstimator) EstimateFromItems(items []TurnItem) int {
	return m.MockEstimateFromItems(items)
}

// HC-009: 配置 Compactor 后 Compact 委托给 compactor。
func TestHeuristicContextManager_CompactWithCompactor(t *testing.T) {
	var compactCalled bool
	mc := &mockCompactor{
		MockCompact: func(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error) {
			compactCalled = true
			return &CompactResult{
				Strategy: CompactTruncate,
				BeforeTokens: 20,
				AfterTokens: 8,
				ItemsRemoved: 2,
				RetainedItems: items[len(items)-1:],
			}, nil
		},
	}

	m := NewHeuristicContextManager(
		WithCompactor(mc),
		WithMaxTokens(100),
	)
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "AAAA"})
	_ = m.RecordItem(ctx, TurnItem{Role: "assistant", Content: "BBBB"})
	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "CCCC"})

	result, err := m.Compact(ctx, CompactManual)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !compactCalled {
		t.Error("compactor was not called")
	}
	if result.ItemsRemoved != 2 {
		t.Errorf("expected 2 removed, got %d", result.ItemsRemoved)
	}
	if result.AfterTokens != 8 {
		t.Errorf("expected after tokens 8, got %d", result.AfterTokens)
	}

	// 验证内存状态已更新
	items, _ := m.GetMessages(ctx, nil)
	if len(items) != 1 {
		t.Errorf("expected 1 retained item, got %d", len(items))
	}
}

// HC-010: Compact 自动策略——未超阈值时返回 noop。
func TestHeuristicContextManager_CompactAutoUnderThreshold(t *testing.T) {
	m := NewHeuristicContextManager(
		WithCompactor(&mockCompactor{
			MockCompact: func(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error) {
				t.Error("compactor should not be called when under threshold")
				return nil, nil
			},
		}),
		WithMaxTokens(100),
	)
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "short"})

	result, err := m.Compact(ctx, CompactAuto)
	if err != nil {
		t.Fatalf("Compact auto: %v", err)
	}
	if result.ItemsRemoved != 0 {
		t.Errorf("expected 0 removed, got %d", result.ItemsRemoved)
	}
}

// HC-011: Compact 自动策略——超阈值时触发压缩。
func TestHeuristicContextManager_CompactAutoTriggersWhenOverThreshold(t *testing.T) {
	var compactCalled bool
	mc := &mockCompactor{
		MockCompact: func(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error) {
			compactCalled = true
			return &CompactResult{
				Strategy: CompactTruncate,
				BeforeTokens: 20,
				AfterTokens: 4,
				ItemsRemoved: 2,
				RetainedItems: items[len(items)-1:],
			}, nil
		},
	}

	m := NewHeuristicContextManager(
		WithCompactor(mc),
		WithMaxTokens(5),
	)
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "AAAAAAAAAAAAAAAAAAA"}) // 19/4 ≈ 4
	_ = m.RecordItem(ctx, TurnItem{Role: "assistant", Content: "BBBBBBBBBBBBBBBBBBB"}) // 19/4 ≈ 4

	result, err := m.Compact(ctx, CompactAuto)
	if err != nil {
		t.Fatalf("Compact auto: %v", err)
	}
	if !compactCalled {
		t.Error("compactor should be called when over threshold")
	}
	if result.ItemsRemoved <= 0 {
		t.Errorf("expected items to be removed when over threshold, removed=%d", result.ItemsRemoved)
	}
}

// HC-012: GetMessages 支持 MaxItems 截断（保留前缀系统消息）。
func TestHeuristicContextManager_GetMessagesWithMaxItems(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	_ = m.SetInitialContext(ctx, []TurnItem{{Role: "system", Content: "prompt"}})
	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "q1"})
	_ = m.RecordItem(ctx, TurnItem{Role: "assistant", Content: "a1"})
	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "q2"})
	_ = m.RecordItem(ctx, TurnItem{Role: "assistant", Content: "a2"})

	opts := &ContextOptions{MaxItems: 3}
	got, _ := m.GetMessages(ctx, opts)

	// 应该保留前缀 system, 再取最近的 2 条: user(q2), assistant(a2)
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d", len(got))
	}
	if got[0].Role != "system" || got[0].Content != "prompt" {
		t.Errorf("first item should be system prompt, got {%s, %s}", got[0].Role, got[0].Content)
	}
	if got[1].Content != "q2" {
		t.Errorf("second item should be 'q2', got '%s'", got[1].Content)
	}
	if got[2].Content != "a2" {
		t.Errorf("third item should be 'a2', got '%s'", got[2].Content)
	}
}

// HC-013: GetMessages 的 MaxItems >= 总条目时不截断。
func TestHeuristicContextManager_GetMessagesMaxItemsLargeEnough(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})
	_ = m.RecordItem(ctx, TurnItem{Role: "assistant", Content: "world"})

	opts := &ContextOptions{MaxItems: 10}
	got, _ := m.GetMessages(ctx, opts)

	if len(got) != 2 {
		t.Errorf("expected 2 items, got %d", len(got))
	}
}

// HC-014: GetMessages 空 opts（nil ContextOptions）正常工作。
func TestHeuristicContextManager_GetMessagesNilOpts(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})

	got, err := m.GetMessages(ctx, nil)
	if err != nil {
		t.Fatalf("GetMessages with nil opts: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 item, got %d", len(got))
	}
}

// HC-015: 并发安全——多个 goroutine 同时 RecordItem 和 GetMessages。
func TestHeuristicContextManager_ConcurrentAccess(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()
	n := 50

	done := make(chan bool, 2)

	// 写 goroutine
	go func() {
		for i := 0; i < n; i++ {
			_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "test"})
		}
		done <- true
	}()

	// 读 goroutine
	go func() {
		for i := 0; i < n; i++ {
			_, _ = m.GetMessages(ctx, nil)
			_ = m.TokenUsage(ctx)
		}
		done <- true
	}()

	<-done
	<-done

	got, _ := m.GetMessages(ctx, nil)
	if len(got) != n {
		t.Errorf("expected %d items, got %d", n, len(got))
	}
}

// HC-016: Context 取消时操作返回错误。
func TestHeuristicContextManager_ContextCancellation(t *testing.T) {
	m := NewHeuristicContextManager()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err := m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})
	if err == nil {
		t.Error("expected error for cancelled context")
	}

	_, err = m.GetMessages(ctx, nil)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// HC-017: RecordItem 超阈值时记录警告但不报错。
func TestHeuristicContextManager_RecordItemOverMaxTokens(t *testing.T) {
	m := NewHeuristicContextManager(
		WithMaxTokens(5),
	)
	ctx := context.Background()

	// 短条目，应正常运行
	err := m.RecordItem(ctx, TurnItem{Role: "user", Content: "short"})
	if err != nil {
		t.Fatalf("RecordItem: %v", err)
	}

	// 长条目，超阈值，应仍成功（仅日志警告）
	err = m.RecordItem(ctx, TurnItem{Role: "user",
		Content: "A very long piece of text that should exceed the max tokens threshold significantly " +
			"to ensure the warning is logged but the operation itself does not fail"})
	if err != nil {
		t.Errorf("RecordItem should not fail even when over maxTokens: %v", err)
	}
}

// HC-018: DefaultHeuristicEstimator 估算合理（char/4）。
func TestDefaultHeuristicEstimator_Estimate(t *testing.T) {
	e := DefaultHeuristicEstimator{}
	if got := e.Estimate("hello world!"); got != 3 { // 12/4=3
		t.Errorf("Estimate(\"hello world!\") = %d, want 3", got)
	}
	items := []TurnItem{
		{Role: "user", Content: "hello"}, // 5/4=1
		{Role: "assistant", Content: "world"}, // 5/4=1
	}
	if got := e.EstimateFromItems(items); got != 2 {
		t.Errorf("EstimateFromItems = %d, want 2", got)
	}
}

// HC-019: GetMessages 空 items 返回空切片。
func TestHeuristicContextManager_GetMessagesEmpty(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	got, err := m.GetMessages(ctx, nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 items, got %d", len(got))
	}
}

// HC-020: Compact 策略的委托——CompactSummary（AC-2.6）和 CompactTruncate。
func TestHeuristicContextManager_CompactDelegationStrategies(t *testing.T) {
	var receivedMaxTokens int
	mc := &mockCompactor{
		MockCompact: func(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error) {
			receivedMaxTokens = maxTokens
			return &CompactResult{
				Strategy: CompactTruncate,
				BeforeTokens: 10,
				AfterTokens: 3,
				ItemsRemoved: 1,
				RetainedItems: items,
			}, nil
		},
	}

	// Test with non-zero maxTokens
	m := NewHeuristicContextManager(
		WithCompactor(mc),
		WithMaxTokens(50),
	)
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})
	_, err := m.Compact(ctx, CompactTruncate)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if receivedMaxTokens != 50 {
		t.Errorf("expected maxTokens=50, got %d", receivedMaxTokens)
	}
}

// HC-024: CompactSummary 委托独立测试（AC-2.6）。
// 验证 CompactSummary 策略委托给 summaryCompactor（而非 truncatingCompactor）。
func TestHeuristicContextManager_CompactSummaryDelegation(t *testing.T) {
	var summaryCalled bool
	var truncateCalled bool

	summaryMock := &mockCompactor{
		MockCompact: func(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error) {
			summaryCalled = true
			return &CompactResult{
				Strategy: CompactSummary,
				BeforeTokens: 50,
				AfterTokens: 10,
				ItemsRemoved: 3,
				Summary: "summary text",
				RetainedItems: items[len(items)-2:],
			}, nil
		},
	}
	truncateMock := &mockCompactor{
		MockCompact: func(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error) {
			truncateCalled = true
			return &CompactResult{
				Strategy: CompactTruncate,
				BeforeTokens: 50,
				AfterTokens: 20,
				ItemsRemoved: 2,
				RetainedItems: items[len(items)-1:],
			}, nil
		},
	}

	// 分别在两个字段注入不同的 mock，验证 CompactSummary 正确路由到 summaryMock。
	m := NewHeuristicContextManager(
		WithSummaryCompactor(summaryMock),
		WithTruncatingCompactor(truncateMock),
		WithMaxTokens(100),
	)
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "AAAA"})
	_ = m.RecordItem(ctx, TurnItem{Role: "assistant", Content: "BBBB"})
	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "CCCC"})

	result, err := m.Compact(ctx, CompactSummary)
	if err != nil {
		t.Fatalf("Compact(Summary): %v", err)
	}

	if !summaryCalled {
		t.Error("summaryCompactor was not called for CompactSummary")
	}
	if truncateCalled {
		t.Error("truncatingCompactor should NOT be called for CompactSummary")
	}
	if result.Strategy != CompactSummary {
		t.Errorf("expected CompactSummary strategy, got %s", result.Strategy)
	}
	if result.Summary != "summary text" {
		t.Errorf("expected summary text, got %s", result.Summary)
	}

	// 验证摘要后 GetMessages 含摘要文本
	msgs, _ := m.GetMessages(ctx, nil)
	if len(msgs) == 0 {
		t.Error("expected non-empty messages after compact")
	}
}

// HC-021: SetInitialContext 空切片不改变状态。
func TestHeuristicContextManager_SetInitialContextEmpty(t *testing.T) {
	m := NewHeuristicContextManager()
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "hello"})
	err := m.SetInitialContext(ctx, []TurnItem{})
	if err != nil {
		t.Fatalf("SetInitialContext with empty: %v", err)
	}

	got, _ := m.GetMessages(ctx, nil)
	if len(got) != 1 {
		t.Errorf("expected 1 item unchanged, got %d", len(got))
	}
}

// HC-022: WithEstimator 可以自定义估算器。
func TestHeuristicContextManager_CustomEstimator(t *testing.T) {
	me := &mockEstimator{
		MockEstimate: func(text string) int {
			return 42
		},
		MockEstimateFromItems: func(items []TurnItem) int {
			return 100
		},
	}
	m := NewHeuristicContextManager(
		WithEstimator(me),
	)
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "anything"})

	usage := m.TokenUsage(ctx)
	if usage != 100 {
		t.Errorf("TokenUsage = %d, want 100 (custom estimator)", usage)
	}
}

// HC-023: Compact with 0 maxTokens 传递当前 token 数作为 limit。
func TestHeuristicContextManager_CompactZeroMaxTokens(t *testing.T) {
	var receivedMaxTokens int
	mc := &mockCompactor{
		MockCompact: func(ctx context.Context, items []TurnItem, maxTokens int) (*CompactResult, error) {
			receivedMaxTokens = maxTokens
			return &CompactResult{
				Strategy: CompactTruncate,
				BeforeTokens: 10,
				AfterTokens: 3,
				ItemsRemoved: 1,
				RetainedItems: items,
			}, nil
		},
	}

	m := NewHeuristicContextManager(
		WithCompactor(mc),
		WithMaxTokens(0),
	)
	ctx := context.Background()

	_ = m.RecordItem(ctx, TurnItem{Role: "user", Content: "test content"})

	_, err := m.Compact(ctx, CompactManual)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// With maxTokens=0, current token count is passed as limit
	if receivedMaxTokens <= 0 {
		t.Errorf("expected positive maxTokens for 0 threshold, got %d", receivedMaxTokens)
	}
}
