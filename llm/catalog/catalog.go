// Package catalog 提供模型元数据目录与动态发现能力。
//
// Catalog 从嵌入式 catalog.json 加载模型元数据，支持按 provider、
// 名称搜索及能力/价格/上下文窗口过滤。model_list.go 提供从
// provider.ModelProvider 动态发现模型的能力。
//
// 本包零 Eino 依赖（IFACE-001），仅依赖 llm/provider 类型。
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/pengjunchen/go-agent-core/llm/provider"
)

//go:embed catalog.json
var catalogData []byte

// ModelEntry 描述目录中单个模型的完整元数据。
// JSON 标签对应 catalog.json 的字段名。
type ModelEntry struct {
	Provider string `json:"provider"`
	ModelName string `json:"model_name"`
	ContextWindow int `json:"context_window"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
	CostInputPerMillion float64 `json:"cost_input_per_million"`
	CostOutputPerMillion float64 `json:"cost_output_per_million"`
	CacheReadPerMillion float64 `json:"cache_read_per_million,omitempty"`
	CacheWritePerMillion float64 `json:"cache_write_per_million,omitempty"`
	SupportsStreaming bool `json:"supports_streaming"`
	SupportsVision bool `json:"supports_vision"`
	SupportsThinking bool `json:"supports_thinking"`
	Compat map[string]bool `json:"compat,omitempty"`
}

// ToModelInfo 将 ModelEntry 转换为 provider.ModelInfo。
func (e *ModelEntry) ToModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{
		Provider: e.Provider,
		ModelName: e.ModelName,
		ContextWindow: e.ContextWindow,
		MaxOutputTokens: e.MaxOutputTokens,
		CostInputPerMillion: e.CostInputPerMillion,
		CostOutputPerMillion: e.CostOutputPerMillion,
		CacheReadPerMillion: e.CacheReadPerMillion,
		CacheWritePerMillion: e.CacheWritePerMillion,
		SupportsStreaming: e.SupportsStreaming,
		SupportsVision: e.SupportsVision,
		SupportsThinking: e.SupportsThinking,
		Compat: e.Compat,
	}
}

// FromModelInfo 从 provider.ModelInfo 构造 ModelEntry。
func FromModelInfo(info *provider.ModelInfo) ModelEntry {
	return ModelEntry{
		Provider: info.Provider,
		ModelName: info.ModelName,
		ContextWindow: info.ContextWindow,
		MaxOutputTokens: info.MaxOutputTokens,
		CostInputPerMillion: info.CostInputPerMillion,
		CostOutputPerMillion: info.CostOutputPerMillion,
		CacheReadPerMillion: info.CacheReadPerMillion,
		CacheWritePerMillion: info.CacheWritePerMillion,
		SupportsStreaming: info.SupportsStreaming,
		SupportsVision: info.SupportsVision,
		SupportsThinking: info.SupportsThinking,
		Compat: info.Compat,
	}
}

// FilterOptions 定义可选的过滤条件。nil 字段表示不过滤该项。
type FilterOptions struct {
	Streaming *bool // 按 supports_streaming 过滤
	Vision *bool // 按 supports_vision 过滤
	Thinking *bool // 按 supports_thinking 过滤
	MinContextWindow *int // 最小上下文窗口
	MaxInputCostPerMillion *float64 // 输入价格上限（每百万 token）
	MaxOutputCostPerMillion *float64 // 输出价格上限（每百万 token）
}

// catalogFile 是 catalog.json 的顶层结构。
type catalogFile struct {
	Models []ModelEntry `json:"models"`
}

// Catalog 持有模型元数据条目，创建后不可变，支持并发读。
type Catalog struct {
	mu sync.RWMutex
	entries []ModelEntry
}

// NewCatalog 从嵌入式 catalog.json 创建 Catalog 实例。
func NewCatalog() *Catalog {
	var cf catalogFile
	if err := json.Unmarshal(catalogData, &cf); err != nil {
		// catalog.json 是编译期嵌入的静态资源，解析失败说明数据损坏，
		// 这是不可恢复的编程错误，直接 panic。
		panic(fmt.Sprintf("catalog: failed to parse embedded catalog.json: %v", err))
	}
	return &Catalog{entries: cf.Models}
}

// GetModel 按 provider 和 modelName 查找模型。未找到返回 (nil, false)。
func (c *Catalog) GetModel(providerName, modelName string) (*ModelEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.entries {
		if c.entries[i].Provider == providerName && c.entries[i].ModelName == modelName {
			entry := c.entries[i]
			return &entry, true
		}
	}
	return nil, false
}

// ListModels 返回所有模型条目的副本。
func (c *Catalog) ListModels() []ModelEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return copyEntries(c.entries)
}

// ListByProvider 按 provider 名称过滤模型。
func (c *Catalog) ListByProvider(providerName string) []ModelEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]ModelEntry, 0)
	for _, e := range c.entries {
		if e.Provider == providerName {
			result = append(result, e)
		}
	}
	return result
}

// Search 按模型名称做大小写不敏感的子串匹配，同时匹配 provider 名称。
func (c *Catalog) Search(query string) []ModelEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	q := strings.ToLower(query)
	result := make([]ModelEntry, 0)
	for _, e := range c.entries {
		if strings.Contains(strings.ToLower(e.ModelName), q) ||
			strings.Contains(strings.ToLower(e.Provider), q) {
			result = append(result, e)
		}
	}
	return result
}

// Filter 按 FilterOptions 过滤模型。
func (c *Catalog) Filter(opts FilterOptions) []ModelEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]ModelEntry, 0)
	for _, e := range c.entries {
		if matchFilter(e, opts) {
			result = append(result, e)
		}
	}
	return result
}

// Merge 将动态发现的条目与目录条目合并，去重（按 provider+modelName）。
// 动态条目优先：若与目录条目 provider+modelName 相同，用动态条目覆盖。
func (c *Catalog) Merge(dynamic []ModelEntry) []ModelEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	seen := make(map[string]bool)
	merged := make([]ModelEntry, 0, len(c.entries)+len(dynamic))

	// 先放入动态条目（优先级高）
	for _, e := range dynamic {
		key := e.Provider + "/" + e.ModelName
		if !seen[key] {
			seen[key] = true
			merged = append(merged, e)
		}
	}
	// 再放入目录中未被动态覆盖的条目
	for _, e := range c.entries {
		key := e.Provider + "/" + e.ModelName
		if !seen[key] {
			seen[key] = true
			merged = append(merged, e)
		}
	}
	return merged
}

// matchFilter 检查单条目是否满足过滤条件。
func matchFilter(e ModelEntry, opts FilterOptions) bool {
	if opts.Streaming != nil && e.SupportsStreaming != *opts.Streaming {
		return false
	}
	if opts.Vision != nil && e.SupportsVision != *opts.Vision {
		return false
	}
	if opts.Thinking != nil && e.SupportsThinking != *opts.Thinking {
		return false
	}
	if opts.MinContextWindow != nil && e.ContextWindow < *opts.MinContextWindow {
		return false
	}
	if opts.MaxInputCostPerMillion != nil && e.CostInputPerMillion > *opts.MaxInputCostPerMillion {
		return false
	}
	if opts.MaxOutputCostPerMillion != nil && e.CostOutputPerMillion > *opts.MaxOutputCostPerMillion {
		return false
	}
	return true
}

// copyEntries 返回切片的浅拷贝，避免外部修改影响内部数据。
func copyEntries(src []ModelEntry) []ModelEntry {
	dst := make([]ModelEntry, len(src))
	copy(dst, src)
	return dst
}
