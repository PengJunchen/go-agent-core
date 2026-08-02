// Package session 的会话树摘要与分支导航增强。
//
// 提供 BranchSummary、SummarizeBranch、GetRetainedTail 和 ListAllBranches，
// 支持上下文压缩时保留活跃分支尾部和生成分支摘要。
package session

import (
	"fmt"
	"strings"
	"time"
)

// ─── BranchSummary ──────────────────────────────────────────────

// BranchSummary 包含对话分支的摘要信息。
type BranchSummary struct {
	BranchID string // 分支末端节点 ID
	ParentID string // 分支点（拥有多个子节点的祖先）ID，无分支点时为空
	Summary string // 摘要文本
	ItemCount int // 分支包含的节点数
	CreatedAt time.Time // 摘要生成时间
}

// ─── SessionTree 摘要与导航方法 ─────────────────────────────────

// SummarizeBranch 生成从根节点到指定 nodeID 路径的摘要。
//
// 如果提供 summarizer 函数，则使用它生成摘要；否则使用简单截断作为 fallback。
// 返回 BranchSummary，包含路径上所有节点的摘要信息。
func (t *SessionTree) SummarizeBranch(nodeID string, summarizer func(nodes []*TreeNode) string) (*BranchSummary, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if _, ok := t.nodes[nodeID]; !ok {
		return nil, fmt.Errorf("node %q not found", nodeID)
	}

	path := t.pathToLocked(nodeID)
	if len(path) == 0 {
		return nil, fmt.Errorf("empty path to node %q", nodeID)
	}

	var summary string
	if summarizer != nil {
		summary = summarizer(path)
	} else {
		summary = defaultSummarizer(path)
	}

	// 查找分支点：路径中最近的拥有多个子节点的祖先
	parentID := ""
	for i := len(path) - 1; i >= 0; i-- {
		if len(path[i].Children) > 1 {
			parentID = path[i].ID
			break
		}
	}

	return &BranchSummary{
		BranchID: nodeID,
		ParentID: parentID,
		Summary: summary,
		ItemCount: len(path),
		CreatedAt: time.Now(),
	}, nil
}

// GetRetainedTail 返回当前活跃分支路径的最后 N 个节点。
//
// 这些节点在上下文压缩时应保留，不被截断。
// 如果路径长度不足 N，返回全部节点。
func (t *SessionTree) GetRetainedTail(n int) []*TreeNode {
	if n <= 0 {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	path := t.pathToLocked(t.activeID)
	if len(path) <= n {
		result := make([]*TreeNode, len(path))
		copy(result, path)
		return result
	}

	tail := make([]*TreeNode, n)
	copy(tail, path[len(path)-n:])
	return tail
}

// ListAllBranches 返回所有分支（从分支点到叶子节点的路径）。
//
// 每个元素是一条完整路径（从分支点到叶子节点）。
// 如果树没有分支点，返回从根到唯一叶子节点的单条路径。
func (t *SessionTree) ListAllBranches() [][]*TreeNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// 找到所有叶子节点（没有子节点的节点）
	var leaves []string
	for id, node := range t.nodes {
		if len(node.Children) == 0 {
			leaves = append(leaves, id)
		}
	}

	if len(leaves) == 0 && len(t.nodes) > 0 {
		// 无叶子节点（不可能，根节点为空时除外）
		return nil
	}

	var branches [][]*TreeNode
	for _, leafID := range leaves {
		path := t.pathToLocked(leafID)
		if len(path) == 0 {
			continue
		}

		// 找到路径上最近的分支点作为分支起点
		branchStart := 0
		for i := len(path) - 1; i >= 0; i-- {
			if len(path[i].Children) > 1 {
				branchStart = i
				break
			}
		}

		branch := make([]*TreeNode, len(path)-branchStart)
		copy(branch, path[branchStart:])
		branches = append(branches, branch)
	}

	// 如果没有分支点，整条路径就是唯一的分支
	if len(branches) == 0 && len(t.nodes) > 0 {
		path := t.pathToLocked(t.activeID)
		if len(path) > 0 {
			branches = append(branches, path)
		}
	}

	return branches
}

// ─── 默认摘要函数 ──────────────────────────────────────────────

// defaultSummarizer 使用简单截断生成路径摘要。
// 将每个节点的 Role 和 Content（最多 100 字符）拼接。
func defaultSummarizer(nodes []*TreeNode) string {
	var sb strings.Builder
	for i, node := range nodes {
		if i > 0 {
			sb.WriteString(" -> ")
		}
		content := node.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		fmt.Fprintf(&sb, "[%s] %s", node.Role, content)
	}
	return sb.String()
}
