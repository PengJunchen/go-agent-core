// Package session 的会话树分支管理实现。
//
// SessionTree 将对话建模为树结构（非线性），每个节点是一次对话回合。
// 分支操作可以从任意节点创建新路径，实现对话的探索与回溯。
//
// 设计要点：
// - 线程安全：读写锁保护并发访问
// - 分支独立：从同一节点分出的分支各自演化，互不影响
// - 活跃指针：activeID 标记当前分支的尖端，支持导航切换
package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// ─── TreeNode ────────────────────────────────────────────────────

// TreeNode 表示会话树中的一个节点。
type TreeNode struct {
	ID string
	ParentID string
	SessionID string
	Role string // "user", "assistant", "tool", "system"
	Content string
	Children []string // 子节点 ID 列表
	CreatedAt time.Time
	Metadata map[string]any
}

// ─── SessionTree ─────────────────────────────────────────────────

// SessionTree 管理树状对话结构。
//
// 与 SessionTreeData（从 Sink 重建的纯数据快照）不同，
// SessionTree 是活跃的内存树管理器，支持增删节点、分支创建和导航。
type SessionTree struct {
	mu sync.RWMutex
	nodes map[string]*TreeNode
	rootID string
	activeID string // 当前活跃节点（活跃分支的尖端）
}

// NewSessionTree 创建一个空的 SessionTree。
func NewSessionTree() *SessionTree {
	return &SessionTree{
		nodes: make(map[string]*TreeNode),
	}
}

// AddNode 添加一个新节点作为当前活跃节点的子节点。
//
// 如果树为空，该节点成为根节点。
// 添加后，新节点成为活跃节点。
func (t *SessionTree) AddNode(node *TreeNode) error {
	if node == nil {
		return fmt.Errorf("node must not be nil")
	}
	if node.ID == "" {
		node.ID = newNodeID()
	}
	if node.CreatedAt.IsZero() {
		node.CreatedAt = time.Now()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// 检查 ID 冲突
	if _, exists := t.nodes[node.ID]; exists {
		return fmt.Errorf("node ID %q already exists", node.ID)
	}

	if len(t.nodes) == 0 {
		// 树为空，设为根节点
		t.rootID = node.ID
		node.ParentID = ""
	} else {
		// 作为活跃节点的子节点
		parent, ok := t.nodes[t.activeID]
		if !ok {
			return fmt.Errorf("active node %q not found", t.activeID)
		}
		node.ParentID = parent.ID
		parent.Children = append(parent.Children, node.ID)
	}

	t.nodes[node.ID] = node
	t.activeID = node.ID
	return nil
}

// Branch 从指定父节点创建新分支。
//
// 新节点作为 parentID 的子节点添加，并成为活跃节点。
// 返回新分支的根节点 ID（即新添加的节点 ID）。
func (t *SessionTree) Branch(parentID string, node *TreeNode) (string, error) {
	if node == nil {
		return "", fmt.Errorf("node must not be nil")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	parent, ok := t.nodes[parentID]
	if !ok {
		return "", fmt.Errorf("parent node %q not found", parentID)
	}

	if node.ID == "" {
		node.ID = newNodeID()
	}
	if node.CreatedAt.IsZero() {
		node.CreatedAt = time.Now()
	}

	// 检查 ID 冲突
	if _, exists := t.nodes[node.ID]; exists {
		return "", fmt.Errorf("node ID %q already exists", node.ID)
	}

	node.ParentID = parent.ID
	parent.Children = append(parent.Children, node.ID)
	t.nodes[node.ID] = node
	t.activeID = node.ID

	return node.ID, nil
}

// GetNode 按 ID 返回节点。
func (t *SessionTree) GetNode(id string) (*TreeNode, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	node, ok := t.nodes[id]
	return node, ok
}

// GetPath 返回从根节点到当前活跃节点的路径。
func (t *SessionTree) GetPath() []*TreeNode {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.pathToLocked(t.activeID)
}

// GetBranchPath 返回从根节点到指定节点的路径。
func (t *SessionTree) GetBranchPath(nodeID string) ([]*TreeNode, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if _, ok := t.nodes[nodeID]; !ok {
		return nil, fmt.Errorf("node %q not found", nodeID)
	}
	return t.pathToLocked(nodeID), nil
}

// MoveTo 设置活跃节点（导航到不同分支）。
func (t *SessionTree) MoveTo(nodeID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.nodes[nodeID]; !ok {
		return fmt.Errorf("node %q not found", nodeID)
	}
	t.activeID = nodeID
	return nil
}

// ListBranches 返回所有分支点（拥有多个子节点的节点）。
func (t *SessionTree) ListBranches() []*TreeNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var branches []*TreeNode
	for _, node := range t.nodes {
		if len(node.Children) > 1 {
			branches = append(branches, node)
		}
	}
	return branches
}

// ActiveID 返回当前活跃节点 ID。
func (t *SessionTree) ActiveID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.activeID
}

// FindLCA 返回两个节点的最近公共祖先（Lowest Common Ancestor）。
//
// 算法：
// 1. 获取从根到 node1ID 的路径（祖先链）
// 2. 获取从根到 node2ID 的路径
// 3. 两条路径按顺序比较，最后一个相同节点即为 LCA
//
// 如果任一节点不存在返回错误。
// 特殊情况：node1ID == node2ID 时，LCA 是该节点自身。
func (t *SessionTree) FindLCA(node1ID, node2ID string) (*TreeNode, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if _, ok := t.nodes[node1ID]; !ok {
		return nil, fmt.Errorf("node %q not found", node1ID)
	}
	if _, ok := t.nodes[node2ID]; !ok {
		return nil, fmt.Errorf("node %q not found", node2ID)
	}

	path1 := t.pathToLocked(node1ID)
	path2 := t.pathToLocked(node2ID)

	var lca *TreeNode
	minLen := len(path1)
	if len(path2) < minLen {
		minLen = len(path2)
	}

	for i := 0; i < minLen; i++ {
		if path1[i].ID == path2[i].ID {
			lca = path1[i]
		} else {
			break
		}
	}

	if lca == nil {
		return nil, fmt.Errorf("no common ancestor found between %q and %q", node1ID, node2ID)
	}

	return lca, nil
}

// pathToLocked 返回从根到指定节点的路径（调用方持读锁）。
func (t *SessionTree) pathToLocked(nodeID string) []*TreeNode {
	var path []*TreeNode
	cur, ok := t.nodes[nodeID]
	if !ok {
		return path
	}

	// 从目标节点向根回溯
	for cur != nil {
		path = append(path, cur)
		if cur.ParentID == "" {
			break
		}
		cur = t.nodes[cur.ParentID]
	}

	// 反转为根→目标顺序
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	return path
}

// newNodeID 使用 crypto/rand 生成节点 ID（格式与 newSessionID 一致）。
func newNodeID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return hex.EncodeToString(b[:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:])
}
