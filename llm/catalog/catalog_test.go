package catalog

import (
	"context"
	"sync"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
	llmprovider "github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// ---------------------------------------------------------------------------
// AC-1: Embedded catalog.json contains model metadata
// ---------------------------------------------------------------------------

func TestCatalog_EmbeddedData(t *testing.T) {
	c := NewCatalog()
	models := c.ListModels()
	if len(models) == 0 {
		t.Fatal("catalog should contain models from embedded catalog.json")
	}

	// 验证已知模型存在
	wantModels := []struct {
		provider string
		modelName string
	}{
		{"openai", "gpt-4o"},
		{"openai", "gpt-4o-mini"},
		{"anthropic", "claude-sonnet-4-20250514"},
		{"anthropic", "claude-opus-4-20250514"},
		{"google", "gemini-2.5-pro"},
	}
	for _, wm := range wantModels {
		entry, ok := c.GetModel(wm.provider, wm.modelName)
		if !ok {
			t.Errorf("GetModel(%q, %q) not found", wm.provider, wm.modelName)
			continue
		}
		if entry.Provider != wm.provider {
			t.Errorf("Provider = %q, want %q", entry.Provider, wm.provider)
		}
		if entry.ModelName != wm.modelName {
			t.Errorf("ModelName = %q, want %q", entry.ModelName, wm.modelName)
		}
	}
}

// AC-3: Catalog contains cost/contextWindow/compat info
func TestCatalog_CostContextCompat(t *testing.T) {
	c := NewCatalog()

	entry, ok := c.GetModel("openai", "gpt-4o")
	if !ok {
		t.Fatal("gpt-4o not found")
	}
	if entry.ContextWindow != 128000 {
		t.Errorf("ContextWindow = %d, want 128000", entry.ContextWindow)
	}
	if entry.MaxOutputTokens != 16384 {
		t.Errorf("MaxOutputTokens = %d, want 16384", entry.MaxOutputTokens)
	}
	if entry.CostInputPerMillion != 2.50 {
		t.Errorf("CostInputPerMillion = %v, want 2.50", entry.CostInputPerMillion)
	}
	if entry.CostOutputPerMillion != 10.00 {
		t.Errorf("CostOutputPerMillion = %v, want 10.00", entry.CostOutputPerMillion)
	}
	if entry.CacheReadPerMillion != 1.25 {
		t.Errorf("CacheReadPerMillion = %v, want 1.25", entry.CacheReadPerMillion)
	}
	if entry.CacheWritePerMillion != 2.50 {
		t.Errorf("CacheWritePerMillion = %v, want 2.50", entry.CacheWritePerMillion)
	}
	if entry.Compat == nil {
		t.Fatal("Compat should not be nil for gpt-4o")
	}
	if !entry.Compat["tool_use"] {
		t.Error("gpt-4o should support tool_use")
	}
	if !entry.Compat["json_mode"] {
		t.Error("gpt-4o should support json_mode")
	}

	// 验证 Anthropic 条目
	aEntry, ok := c.GetModel("anthropic", "claude-sonnet-4-20250514")
	if !ok {
		t.Fatal("claude-sonnet-4-20250514 not found")
	}
	if aEntry.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", aEntry.ContextWindow)
	}
	if aEntry.CostInputPerMillion != 3.00 {
		t.Errorf("CostInputPerMillion = %v, want 3.00", aEntry.CostInputPerMillion)
	}
	if !aEntry.SupportsThinking {
		t.Error("claude-sonnet-4 should support thinking")
	}

	// 验证 Gemini 条目（无缓存价格字段）
	gEntry, ok := c.GetModel("google", "gemini-2.5-pro")
	if !ok {
		t.Fatal("gemini-2.5-pro not found")
	}
	if gEntry.ContextWindow != 1000000 {
		t.Errorf("ContextWindow = %d, want 1000000", gEntry.ContextWindow)
	}
}

// ---------------------------------------------------------------------------
// AC-4: Model search and filter API works correctly
// ---------------------------------------------------------------------------

func TestCatalog_GetModel(t *testing.T) {
	c := NewCatalog()

	// 正常查找
	entry, ok := c.GetModel("openai", "gpt-4o")
	if !ok {
		t.Fatal("expected to find gpt-4o")
	}
	if entry.ModelName != "gpt-4o" {
		t.Errorf("ModelName = %q, want gpt-4o", entry.ModelName)
	}

	// 不存在的模型
	_, ok = c.GetModel("openai", "nonexistent")
	if ok {
		t.Error("should not find nonexistent model")
	}

	// 不存在的 provider
	_, ok = c.GetModel("unknown", "gpt-4o")
	if ok {
		t.Error("should not find model from unknown provider")
	}
}

func TestCatalog_ListModels(t *testing.T) {
	c := NewCatalog()
	models := c.ListModels()
	if len(models) < 5 {
		t.Errorf("ListModels returned %d entries, want at least 5", len(models))
	}

	// 验证返回的是副本，修改不影响原数据
	models[0].ModelName = "tampered"
	_, ok := c.GetModel(models[0].Provider, "tampered")
	if ok {
		t.Error("modifying ListModels result should not affect catalog")
	}
}

func TestCatalog_ListByProvider(t *testing.T) {
	c := NewCatalog()

	// OpenAI 应该有 2 个模型
	openaiModels := c.ListByProvider("openai")
	if len(openaiModels) != 2 {
		t.Errorf("ListByProvider(openai) = %d, want 2", len(openaiModels))
	}
	for _, m := range openaiModels {
		if m.Provider != "openai" {
			t.Errorf("Provider = %q, want openai", m.Provider)
		}
	}

	// Anthropic 应该有 2 个模型
	anthropicModels := c.ListByProvider("anthropic")
	if len(anthropicModels) != 2 {
		t.Errorf("ListByProvider(anthropic) = %d, want 2", len(anthropicModels))
	}

	// 不存在的 provider 返回空
	unknownModels := c.ListByProvider("unknown")
	if len(unknownModels) != 0 {
		t.Errorf("ListByProvider(unknown) = %d, want 0", len(unknownModels))
	}
}

func TestCatalog_Search(t *testing.T) {
	c := NewCatalog()

	// 按模型名搜索（大小写不敏感）
	results := c.Search("GPT-4O")
	if len(results) == 0 {
		t.Error("Search(GPT-4O) should return results")
	}
	found := false
	for _, r := range results {
		if r.ModelName == "gpt-4o" {
			found = true
		}
	}
	if !found {
		t.Error("Search(GPT-4O) should find gpt-4o")
	}

	// 按子串搜索
	results = c.Search("claude")
	if len(results) < 2 {
		t.Errorf("Search(claude) returned %d results, want at least 2", len(results))
	}

	// 按 provider 名搜索
	results = c.Search("anthropic")
	if len(results) < 2 {
		t.Errorf("Search(anthropic) returned %d results, want at least 2", len(results))
	}

	// 搜索不存在的
	results = c.Search("nonexistent-model-xyz")
	if len(results) != 0 {
		t.Errorf("Search(nonexistent) = %d, want 0", len(results))
	}
}

func TestCatalog_Filter(t *testing.T) {
	c := NewCatalog()

	// 过滤支持 thinking 的模型
	thinking := true
	results := c.Filter(FilterOptions{Thinking: &thinking})
	if len(results) == 0 {
		t.Error("Filter(thinking=true) should return results")
	}
	for _, r := range results {
		if !r.SupportsThinking {
			t.Errorf("Filter(thinking=true) returned %q which does not support thinking", r.ModelName)
		}
	}

	// 过滤不支持 thinking 的模型
	noThinking := false
	results = c.Filter(FilterOptions{Thinking: &noThinking})
	for _, r := range results {
		if r.SupportsThinking {
			t.Errorf("Filter(thinking=false) returned %q which supports thinking", r.ModelName)
		}
	}

	// 过滤大上下文窗口
	minCtx := 200000
	results = c.Filter(FilterOptions{MinContextWindow: &minCtx})
	for _, r := range results {
		if r.ContextWindow < 200000 {
			t.Errorf("Filter(minCtx=200000) returned %q with ContextWindow=%d", r.ModelName, r.ContextWindow)
		}
	}

	// 过滤低成本
	maxInput := 3.0
	results = c.Filter(FilterOptions{MaxInputCostPerMillion: &maxInput})
	for _, r := range results {
		if r.CostInputPerMillion > 3.0 {
			t.Errorf("Filter(maxInput=3.0) returned %q with cost %v", r.ModelName, r.CostInputPerMillion)
		}
	}

	// 组合过滤：vision + thinking
	vision := true
	results = c.Filter(FilterOptions{Vision: &vision, Thinking: &thinking})
	for _, r := range results {
		if !r.SupportsVision || !r.SupportsThinking {
			t.Errorf("Filter(vision+thinking) returned %q with vision=%v thinking=%v",
				r.ModelName, r.SupportsVision, r.SupportsThinking)
		}
	}

	// 空过滤选项返回全部
	results = c.Filter(FilterOptions{})
	allModels := c.ListModels()
	if len(results) != len(allModels) {
		t.Errorf("Filter({}) = %d, want %d", len(results), len(allModels))
	}
}

func TestCatalog_Filter_OutputCost(t *testing.T) {
	c := NewCatalog()

	maxOutput := 10.0
	results := c.Filter(FilterOptions{MaxOutputCostPerMillion: &maxOutput})
	for _, r := range results {
		if r.CostOutputPerMillion > 10.0 {
			t.Errorf("Filter(maxOutput=10.0) returned %q with output cost %v",
				r.ModelName, r.CostOutputPerMillion)
		}
	}
}

func TestCatalog_Merge(t *testing.T) {
	c := NewCatalog()

	// 动态条目覆盖同名目录条目
	dynamic := []ModelEntry{
		{
			Provider: "openai",
			ModelName: "gpt-4o",
			ContextWindow: 256000, // 更新的上下文窗口
			MaxOutputTokens: 32768,
		},
		{
			Provider: "custom",
			ModelName: "my-model",
			ContextWindow: 64000,
		},
	}

	merged := c.Merge(dynamic)
	if len(merged) < len(c.ListModels()) {
		t.Error("Merge should produce at least as many entries as catalog")
	}

	// 验证动态条目优先
	for _, m := range merged {
		if m.Provider == "openai" && m.ModelName == "gpt-4o" {
			if m.ContextWindow != 256000 {
				t.Errorf("Merged gpt-4o ContextWindow = %d, want 256000 (from dynamic)", m.ContextWindow)
			}
			break
		}
	}

	// 验证新 provider 的模型存在
	found := false
	for _, m := range merged {
		if m.Provider == "custom" && m.ModelName == "my-model" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Merge should include dynamic entry custom/my-model")
	}
}

// ---------------------------------------------------------------------------
// AC-2: Dynamic model discovery (test with mock provider)
// ---------------------------------------------------------------------------

// mockListerProvider 实现了 ModelProvider + ModelLister 接口
type mockListerProvider struct {
	info *llmprovider.ModelInfo
	entries []ModelEntry
}

func (m *mockListerProvider) StreamChat(_ context.Context, _ []message.Message, _ *llmprovider.ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, nil
}
func (m *mockListerProvider) Generate(_ context.Context, _ []message.Message, _ *llmprovider.ChatOptions) (*message.Message, error) {
	return nil, nil
}
func (m *mockListerProvider) ModelInfo() *llmprovider.ModelInfo { return m.info }
func (m *mockListerProvider) ListModels(_ context.Context) ([]ModelEntry, error) {
	return m.entries, nil
}

// mockNoListerProvider 仅实现 ModelProvider（不支持动态发现）
type mockNoListerProvider struct {
	info *llmprovider.ModelInfo
}

func (m *mockNoListerProvider) StreamChat(_ context.Context, _ []message.Message, _ *llmprovider.ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, nil
}
func (m *mockNoListerProvider) Generate(_ context.Context, _ []message.Message, _ *llmprovider.ChatOptions) (*message.Message, error) {
	return nil, nil
}
func (m *mockNoListerProvider) ModelInfo() *llmprovider.ModelInfo { return m.info }

// mockErrorListerProvider 实现 ModelLister 但总是返回错误
type mockErrorListerProvider struct {
	mockNoListerProvider
}

func (m *mockErrorListerProvider) ListModels(_ context.Context) ([]ModelEntry, error) {
	return nil, context.DeadlineExceeded
}

func TestListModelsFromProvider_WithLister(t *testing.T) {
	p := &mockListerProvider{
		info: &llmprovider.ModelInfo{Provider: "openai", ModelName: "gpt-4o"},
		entries: []ModelEntry{
			{Provider: "openai", ModelName: "gpt-4o", ContextWindow: 256000},
			{Provider: "openai", ModelName: "gpt-4-turbo", ContextWindow: 128000},
		},
	}

	results, err := ListModelsFromProvider(context.Background(), p)
	if err != nil {
		t.Fatalf("ListModelsFromProvider err = %v", err)
	}
	if len(results) < 2 {
		t.Errorf("ListModelsFromProvider returned %d entries, want at least 2", len(results))
	}

	// 动态条目应覆盖目录中的 gpt-4o
	for _, r := range results {
		if r.Provider == "openai" && r.ModelName == "gpt-4o" {
			if r.ContextWindow != 256000 {
				t.Errorf("gpt-4o ContextWindow = %d, want 256000 (from dynamic)", r.ContextWindow)
			}
			break
		}
	}
}

func TestListModelsFromProvider_WithoutLister(t *testing.T) {
	p := &mockNoListerProvider{
		info: &llmprovider.ModelInfo{Provider: "openai", ModelName: "gpt-4o"},
	}

	results, err := ListModelsFromProvider(context.Background(), p)
	if err != nil {
		t.Fatalf("ListModelsFromProvider err = %v", err)
	}
	if len(results) == 0 {
		t.Error("ListModelsFromProvider should fall back to catalog for known provider")
	}
}

func TestListModelsFromProvider_UnknownProvider(t *testing.T) {
	p := &mockNoListerProvider{
		info: &llmprovider.ModelInfo{Provider: "unknown-provider", ModelName: "x"},
	}

	_, err := ListModelsFromProvider(context.Background(), p)
	if err == nil {
		t.Error("ListModelsFromProvider should return error for unknown provider without ModelLister")
	}
}

func TestListModelsFromProvider_ErrorLister(t *testing.T) {
	p := &mockErrorListerProvider{
		mockNoListerProvider: mockNoListerProvider{
			info: &llmprovider.ModelInfo{Provider: "openai", ModelName: "gpt-4o"},
		},
	}

	results, err := ListModelsFromProvider(context.Background(), p)
	if err != nil {
		t.Fatalf("ListModelsFromProvider should fall back to catalog, got err = %v", err)
	}
	if len(results) == 0 {
		t.Error("ListModelsFromProvider should fall back to catalog entries for openai")
	}
}

func TestListModelsFromProvider_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	p := &mockListerProvider{
		info: &llmprovider.ModelInfo{Provider: "openai", ModelName: "gpt-4o"},
	}

	_, err := ListModelsFromProvider(ctx, p)
	if err == nil {
		t.Error("ListModelsFromProvider should return error with cancelled context")
	}
}

// ---------------------------------------------------------------------------
// ModelEntry ↔ ModelInfo 转换
// ---------------------------------------------------------------------------

func TestModelEntry_ToModelInfo(t *testing.T) {
	entry := ModelEntry{
		Provider: "anthropic",
		ModelName: "claude-sonnet-4-20250514",
		ContextWindow: 200000,
		MaxOutputTokens: 64000,
		CostInputPerMillion: 3.0,
		CostOutputPerMillion: 15.0,
		CacheReadPerMillion: 0.3,
		CacheWritePerMillion: 3.75,
		SupportsStreaming: true,
		SupportsVision: true,
		SupportsThinking: true,
		Compat: map[string]bool{"tool_use": true},
	}

	info := entry.ToModelInfo()
	if info.Provider != entry.Provider {
		t.Errorf("Provider = %q, want %q", info.Provider, entry.Provider)
	}
	if info.ModelName != entry.ModelName {
		t.Errorf("ModelName = %q, want %q", info.ModelName, entry.ModelName)
	}
	if info.ContextWindow != entry.ContextWindow {
		t.Errorf("ContextWindow = %d, want %d", info.ContextWindow, entry.ContextWindow)
	}
	if info.MaxOutputTokens != entry.MaxOutputTokens {
		t.Errorf("MaxOutputTokens = %d, want %d", info.MaxOutputTokens, entry.MaxOutputTokens)
	}
	if info.CostInputPerMillion != entry.CostInputPerMillion {
		t.Errorf("CostInputPerMillion = %v, want %v", info.CostInputPerMillion, entry.CostInputPerMillion)
	}
	if info.CacheReadPerMillion != entry.CacheReadPerMillion {
		t.Errorf("CacheReadPerMillion = %v, want %v", info.CacheReadPerMillion, entry.CacheReadPerMillion)
	}
	if info.SupportsThinking != entry.SupportsThinking {
		t.Errorf("SupportsThinking = %v, want %v", info.SupportsThinking, entry.SupportsThinking)
	}
	if !info.Compat["tool_use"] {
		t.Error("Compat.tool_use should be true")
	}
}

func TestFromModelInfo(t *testing.T) {
	info := &llmprovider.ModelInfo{
		Provider: "google",
		ModelName: "gemini-2.5-pro",
		ContextWindow: 1000000,
		MaxOutputTokens: 8192,
		CostInputPerMillion: 1.25,
		CostOutputPerMillion: 5.0,
		SupportsStreaming: true,
		SupportsVision: true,
		SupportsThinking: true,
		Compat: map[string]bool{"tool_use": true, "json_mode": true},
	}

	entry := FromModelInfo(info)
	if entry.Provider != info.Provider {
		t.Errorf("Provider = %q, want %q", entry.Provider, info.Provider)
	}
	if entry.ModelName != info.ModelName {
		t.Errorf("ModelName = %q, want %q", entry.ModelName, info.ModelName)
	}
	if entry.ContextWindow != info.ContextWindow {
		t.Errorf("ContextWindow = %d, want %d", entry.ContextWindow, info.ContextWindow)
	}
	if !entry.Compat["json_mode"] {
		t.Error("Compat.json_mode should be true")
	}
}

// ---------------------------------------------------------------------------
// AC-5: go test -race passes — 并发安全测试
// ---------------------------------------------------------------------------

func TestCatalog_ConcurrentAccess(t *testing.T) {
	c := NewCatalog()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			c.ListModels()
		}()
		go func() {
			defer wg.Done()
			c.ListByProvider("openai")
		}()
		go func() {
			defer wg.Done()
			c.Search("gpt")
		}()
		go func() {
			defer wg.Done()
			thinking := true
			c.Filter(FilterOptions{Thinking: &thinking})
		}()
	}
	wg.Wait()
}

func TestCatalog_GetModel_Concurrent(t *testing.T) {
	c := NewCatalog()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry, ok := c.GetModel("openai", "gpt-4o")
			if !ok {
				t.Error("concurrent GetModel should find gpt-4o")
				return
			}
			if entry.Provider != "openai" {
				t.Errorf("concurrent GetModel Provider = %q, want openai", entry.Provider)
			}
		}()
	}
	wg.Wait()
}

// 编译期接口检查
func TestInterface_ModelLister(t *testing.T) {
	var _ ModelLister = (*mockListerProvider)(nil)
}
