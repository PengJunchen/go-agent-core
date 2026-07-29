// Package compactor — HeuristicTokenEstimator: CJK-aware token estimation.
//
// HeuristicTokenEstimator improves on the original HeuristicEstimator by
// detecting CJK (Chinese/Japanese/Korean) character density and adjusting
// the heuristic (char/2 for high CJK, char/4 otherwise). It also accounts
// for ThinkingContent and ToolCall arguments in EstimateFromItems, and
// provides EstimateFromUsage for reliable post-generation token counts.
package compactor

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/pengjunchen/go-agent-core/llm/message"
	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// Compile-time check that HeuristicTokenEstimator implements TokenEstimator.
var _ memctx.TokenEstimator = (*HeuristicTokenEstimator)(nil)

// HeuristicTokenEstimator 是 CJK-aware 的启发式 token 估算器。
//
// 估算规则：
// - Estimate(text): 若 CJK 字符占比 > 30%，返回 len(runes)/2；否则 len(runes)/4
// - EstimateFromItems(items): 对每个 item 累加 Content + ThinkingContent +
// ToolCalls (Name + Arguments JSON)
// - EstimateFromUsage(usage): 信任 LLM 返回的实际 token 数
type HeuristicTokenEstimator struct{}

// NewHeuristicTokenEstimator 创建 HeuristicTokenEstimator 实例。
func NewHeuristicTokenEstimator() *HeuristicTokenEstimator {
	return &HeuristicTokenEstimator{}
}

// Estimate 估算一段文本的 token 数量。
//
// CJK 字符占比 > 30% 时使用 char/2 估算，否则 char/4。
func (e *HeuristicTokenEstimator) Estimate(text string) int {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0
	}

	cjkCount := 0
	for _, r := range runes {
		if isCJK(r) {
			cjkCount++
		}
	}

	ratio := float64(cjkCount) / float64(len(runes))
	if ratio > 0.3 {
		// CJK-dense: roughly 2 chars per token
		return max(1, len(runes)/2)
	}
	// English/other: roughly 4 chars per token
	return max(1, len(runes)/4)
}

// EstimateFromItems 估算一批 TurnItem 的 token 总数。
//
// 对每个条目累加：
// - Content 的估算
// - ThinkingContent 的估算
// - 每个 ToolCall 的 Name + Arguments JSON 序列化
func (e *HeuristicTokenEstimator) EstimateFromItems(items []memctx.TurnItem) int {
	total := 0
	for _, item := range items {
		total += e.Estimate(item.Content)
		total += e.Estimate(item.ThinkingContent)

		for _, tc := range item.ToolCalls {
			total += e.Estimate(tc.Name)
			if len(tc.Arguments) > 0 {
				if argsJSON, err := json.Marshal(tc.Arguments); err == nil {
					total += e.Estimate(string(argsJSON))
				}
			}
		}
	}
	return total
}

// EstimateFromUsage 从 LLM 返回的 Usage 结构估算 token 数量。
//
// 直接返回 Usage.TotalTokens，信任 LLM 的实际 token 计数。
func (e *HeuristicTokenEstimator) EstimateFromUsage(usage *message.Usage) int {
	if usage == nil {
		return 0
	}
	return usage.TotalTokens
}

// isCJK 判断一个 rune 是否属于 CJK Unicode 范围。
func isCJK(r rune) bool {
	// CJK Unified Ideographs
	if unicode.Is(unicode.Han, r) {
		return true
	}
	// CJK Unified Ideographs Extension A
	if r >= 0x3400 && r <= 0x4DBF {
		return true
	}
	// CJK Unified Ideographs Extension B-F
	if r >= 0x20000 && r <= 0x2FA1F {
		return true
	}
	// CJK Compatibility Ideographs
	if r >= 0xF900 && r <= 0xFAFF {
		return true
	}
	// CJK Compatibility Ideographs Supplement
	if r >= 0x2F800 && r <= 0x2FA1F {
		return true
	}
	// Hiragana + Katakana
	if r >= 0x3040 && r <= 0x30FF {
		return true
	}
	// Hangul Syllables
	if r >= 0xAC00 && r <= 0xD7AF {
		return true
	}
	return false
}

// max returns the larger of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Ensure strings import is used (for compatibility with future enhancements).
var _ = strings.Builder{}
