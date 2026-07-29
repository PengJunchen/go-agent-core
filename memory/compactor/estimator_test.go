package compactor

import (
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// TestHeuristicTokenEstimator_Estimate_English: 纯英文 char/4。
func TestHeuristicTokenEstimator_Estimate_English(t *testing.T) {
	e := &HeuristicTokenEstimator{}

	tests := []struct {
		input string
		want int
	}{
		{"hello world", 2}, // 11/4=2
		{"a", 1}, // 1/4=0 -> min 1
		{"hello world! foo", 4}, // 16/4=4
		{"", 0},
	}

	for _, tt := range tests {
		got := e.Estimate(tt.input)
		if got != tt.want {
			t.Errorf("Estimate(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// TestHeuristicTokenEstimator_Estimate_CJK: CJK 占多时 char/2。
func TestHeuristicTokenEstimator_Estimate_CJK(t *testing.T) {
	e := &HeuristicTokenEstimator{}

	// 纯中文文本，CJK 占比 100% > 30%，用 char/2。
	cjkText := "你好世界这是一个测试"
	// rune count: 10（你/好/世/界/这/是/一/个/测/试）
	// 10/2 = 5
	if got := e.Estimate(cjkText); got != 5 {
		t.Errorf("Estimate(%q) = %d, want 5 (10 runes / 2)", cjkText, got)
	}

	// 混合：CJK 占比刚好 30%（6/20=0.3），NOT > 0.3，用 char/4。
	mixed := "你好hello世界world测试test"
	// rune count: 20, CJK count: 6 (你,好,世,界,测,试)
	// 6/20 = 0.3, not > 0.3, so char/4: 20/4 = 5
	if got := e.Estimate(mixed); got != 5 {
		t.Errorf("Estimate(%q) = %d, want 5 (20 runes / 4, CJK ratio=0.3 not > 0.3)", mixed, got)
	}
}

// TestHeuristicTokenEstimator_Estimate_Mixed: 混合中英文边界。
func TestHeuristicTokenEstimator_Estimate_Mixed(t *testing.T) {
	e := &HeuristicTokenEstimator{}

	// 少量中文，CJK 占比 <= 30%，用 char/4。
	// "hello wo中文": runes=11, CJK=2 (中文), 2/11≈0.18 < 0.3 -> char/4: 11/4=2
	// max(1,2)=2
	input := "hello wo中文"
	if got := e.Estimate(input); got != 2 {
		t.Errorf("Estimate(%q) = %d, want 2", input, got)
	}

	// "abc中文de": runes=7, CJK=2, 2/7≈0.286 < 0.3 -> char/4: 7/4=1, max(1,1)=1
	boundary := "abc中文de"
	if got := e.Estimate(boundary); got != 1 {
		t.Errorf("Estimate(%q) = %d, want 1 (7 runes / 4 = 1)", boundary, got)
	}
}

// TestHeuristicTokenEstimator_Estimate_Empty: 空字符串返回 0。
func TestHeuristicTokenEstimator_Estimate_Empty(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	if got := e.Estimate(""); got != 0 {
		t.Errorf("Estimate(\"\") = %d, want 0", got)
	}
}

// TestHeuristicTokenEstimator_EstimateFromItems_WithThinking: ThinkingContent 计入。
func TestHeuristicTokenEstimator_EstimateFromItems_WithThinking(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	items := []memctx.TurnItem{
		{
			Role: "assistant",
			Content: "short reply", // 12/4=3
			ThinkingContent: "this is a long thinking process for testing", // 44/4=11
		},
	}
	// "short reply" = 11 runes, 11/4=2, max(1,2)=2
	// "this is a long thinking process for testing" = 43 runes, 43/4=10, max(1,10)=10
	// 2 + 10 = 12
	if got := e.EstimateFromItems(items); got != 12 {
		t.Errorf("EstimateFromItems with thinking = %d, want 14", got)
	}
}

// TestHeuristicTokenEstimator_EstimateFromItems_WithToolCalls: ToolCall(Name+Args) 计入。
func TestHeuristicTokenEstimator_EstimateFromItems_WithToolCalls(t *testing.T) {
	e := &HeuristicTokenEstimator{}

	items := []memctx.TurnItem{
		{
			Role: "assistant",
			Content: "I'll call a tool",
			ToolCalls: []memctx.ToolCallRef{
				{
					ID: "call_1",
					Name: "read_file",
					Arguments: map[string]any{
						"path": "/tmp/test.go",
					},
				},
			},
		},
		{
			Role: "tool",
			Content: "file content here",
		},
	}
	// We just verify the call works without error; actual count depends on
	// precise rune lengths and json.Marshal output.
	// Check it's at least counting tool calls (not just content).
	got := e.EstimateFromItems(items)
	contentOnly := e.Estimate(items[0].Content) + e.Estimate(items[1].Content)
	if got <= contentOnly {
		t.Errorf("EstimateFromItems with tool calls = %d, should be > content-only %d", got, contentOnly)
	}
}

// TestHeuristicTokenEstimator_EstimateFromUsage: 委托 usage.TotalTokens。
func TestHeuristicTokenEstimator_EstimateFromUsage(t *testing.T) {
	e := &HeuristicTokenEstimator{}

	tests := []struct {
		usage *message.Usage
		want int
	}{
		{&message.Usage{TotalTokens: 100}, 100},
		{&message.Usage{TotalTokens: 0}, 0},
		{nil, 0},
		{&message.Usage{Input: 50, Output: 30, TotalTokens: 80}, 80},
	}

	for _, tt := range tests {
		got := e.EstimateFromUsage(tt.usage)
		if got != tt.want {
			t.Errorf("EstimateFromUsage(%+v) = %d, want %d", tt.usage, got, tt.want)
		}
	}
}

// TestHeuristicTokenEstimator_BackwardCompat: 与旧 HeuristicEstimator 在纯英文场景一致。
func TestHeuristicTokenEstimator_BackwardCompat(t *testing.T) {
	e := &HeuristicTokenEstimator{}

	// 纯英文场景：HeuristicTokenEstimator 应与旧 char/4 一致。
	oldEst := func(text string) int {
		if len(text) == 0 {
			return 0
		}
		return max(1, len([]rune(text))/4)
	}

	tests := []string{
		"hello world",
		"this is a test",
		"abc",
		"",
		"a very long english sentence with many words for testing purposes only",
	}

	for _, input := range tests {
		newResult := e.Estimate(input)
		oldResult := oldEst(input)
		if newResult != oldResult {
			t.Errorf("Estimate(%q) = %d, old = %d (backward compat fail)", input, newResult, oldResult)
		}
	}
}

// TestHeuristicTokenEstimator_Interface: 编译检查实现 TokenEstimator 接口。
func TestHeuristicTokenEstimator_Interface(t *testing.T) {
	var _ memctx.TokenEstimator = (*HeuristicTokenEstimator)(nil)
}
