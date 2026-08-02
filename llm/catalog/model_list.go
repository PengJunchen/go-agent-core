package catalog

import (
	"context"
	"fmt"

	llmprovider "github.com/pengjunchen/go-agent-core/llm/provider"
)

// ModelLister 是可选接口，支持模型列表发现的 Provider 可实现此接口。
// 未实现此接口的 Provider 将在 ListModelsFromProvider 中回退到目录查询。
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelEntry, error)
}

// ListModelsFromProvider 从 Provider 动态发现可用模型。
//
// 工作流程：
// 1. 若 Provider 实现了 ModelLister 接口，调用其 ListModels 方法获取动态列表；
// 2. 将动态结果与嵌入式目录合并（动态优先），返回合并后的完整列表；
// 3. 若 Provider 未实现 ModelLister，回退到目录中该 provider 的条目。
func ListModelsFromProvider(ctx context.Context, p llmprovider.ModelProvider) ([]ModelEntry, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	catalog := NewCatalog()
	providerName := p.ModelInfo().Provider

	// 尝试动态发现
	lister, ok := p.(ModelLister)
	if !ok {
		// Provider 不支持动态列表，回退到目录
		entries := catalog.ListByProvider(providerName)
		if len(entries) == 0 {
			return nil, fmt.Errorf("catalog: provider %q not found in catalog and does not implement ModelLister", providerName)
		}
		return entries, nil
	}

	dynamicEntries, err := lister.ListModels(ctx)
	if err != nil {
		// 动态发现失败，回退到目录
		entries := catalog.ListByProvider(providerName)
		if len(entries) == 0 {
			return nil, fmt.Errorf("catalog: dynamic discovery failed (%w) and provider %q not found in catalog", err, providerName)
		}
		return entries, nil
	}

	// 合并动态结果与目录（动态优先）
	return catalog.Merge(dynamicEntries), nil
}
