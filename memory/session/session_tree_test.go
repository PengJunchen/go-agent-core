package session

import (
	"testing"
)

// ─── 辅助函数 ────────────────────────────────────────────────────

// newTestNode 创建测试用 TreeNode。
func newTestNode(role, content string) *TreeNode {
	return &TreeNode{
		Role: role,
		Content: content,
	}
}

// ─── SessionTree 测试 ────────────────────────────────────────────

// ST-001: AddNode 创建线性路径。
func TestSessionTree_AddNodeLinearPath(t *testing.T) {
	tree := NewSessionTree()

	// 添加根节点
	root := newTestNode("user", "hello")
	if err := tree.AddNode(root); err != nil {
		t.Fatalf("AddNode root: %v", err)
	}
	if tree.ActiveID() != root.ID {
		t.Errorf("activeID = %q, want %q", tree.ActiveID(), root.ID)
	}

	// 添加第二个节点
	second := newTestNode("assistant", "hi there")
	if err := tree.AddNode(second); err != nil {
		t.Fatalf("AddNode second: %v", err)
	}
	if tree.ActiveID() != second.ID {
		t.Errorf("activeID = %q, want %q", tree.ActiveID(), second.ID)
	}

	// 添加第三个节点
	third := newTestNode("user", "how are you?")
	if err := tree.AddNode(third); err != nil {
		t.Fatalf("AddNode third: %v", err)
	}

	// 验证线性链接
	if second.ParentID != root.ID {
		t.Errorf("second.ParentID = %q, want %q", second.ParentID, root.ID)
	}
	if third.ParentID != second.ID {
		t.Errorf("third.ParentID = %q, want %q", third.ParentID, second.ID)
	}

	// 验证根节点没有父节点
	if root.ParentID != "" {
		t.Errorf("root.ParentID = %q, want empty", root.ParentID)
	}
}

// ST-002: Branch 在任意节点创建分叉。
func TestSessionTree_BranchCreatesFork(t *testing.T) {
	tree := NewSessionTree()

	// 创建线性路径：root → a1
	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)

	// 从 root 创建分支
	branch1 := newTestNode("assistant", "hey!")
	branchID, err := tree.Branch(root.ID, branch1)
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if branchID != branch1.ID {
		t.Errorf("Branch returned %q, want %q", branchID, branch1.ID)
	}

	// 验证 root 有两个子节点
	if len(root.Children) != 2 {
		t.Fatalf("root has %d children, want 2", len(root.Children))
	}

	// 验证 branch1 的父节点是 root
	if branch1.ParentID != root.ID {
		t.Errorf("branch1.ParentID = %q, want %q", branch1.ParentID, root.ID)
	}

	// 验证活跃节点切换到新分支
	if tree.ActiveID() != branch1.ID {
		t.Errorf("activeID = %q, want %q", tree.ActiveID(), branch1.ID)
	}
}

// ST-003: 分支对话独立演化。
func TestSessionTree_BranchesEvolveIndependently(t *testing.T) {
	tree := NewSessionTree()

	// root → a1
	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)

	// 从 root 分支：branch1
	branch1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(root.ID, branch1)

	// 在 branch1 上继续添加节点
	b1_child := newTestNode("user", "tell me more")
	_ = tree.AddNode(b1_child)

	if b1_child.ParentID != branch1.ID {
		t.Errorf("b1_child.ParentID = %q, want %q", b1_child.ParentID, branch1.ID)
	}

	// 切换回 a1 分支
	_ = tree.MoveTo(a1.ID)

	// 在 a1 分支上继续
	a1_child := newTestNode("user", "continue")
	_ = tree.AddNode(a1_child)

	if a1_child.ParentID != a1.ID {
		t.Errorf("a1_child.ParentID = %q, want %q", a1_child.ParentID, a1.ID)
	}

	// 验证两个分支的子节点互不影响
	a1Node, _ := tree.GetNode(a1.ID)
	branch1Node, _ := tree.GetNode(branch1.ID)

	if len(a1Node.Children) != 1 {
		t.Errorf("a1 has %d children, want 1", len(a1Node.Children))
	}
	if a1Node.Children[0] != a1_child.ID {
		t.Errorf("a1 child = %q, want %q", a1Node.Children[0], a1_child.ID)
	}
	if len(branch1Node.Children) != 1 {
		t.Errorf("branch1 has %d children, want 1", len(branch1Node.Children))
	}
	if branch1Node.Children[0] != b1_child.ID {
		t.Errorf("branch1 child = %q, want %q", branch1Node.Children[0], b1_child.ID)
	}
}

// ST-004: GetPath 返回正确的路径。
func TestSessionTree_GetPath(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)
	a2 := newTestNode("user", "bye")
	_ = tree.AddNode(a2)

	path := tree.GetPath()
	if len(path) != 3 {
		t.Fatalf("path length = %d, want 3", len(path))
	}
	if path[0].ID != root.ID {
		t.Errorf("path[0] = %q, want %q", path[0].ID, root.ID)
	}
	if path[1].ID != a1.ID {
		t.Errorf("path[1] = %q, want %q", path[1].ID, a1.ID)
	}
	if path[2].ID != a2.ID {
		t.Errorf("path[2] = %q, want %q", path[2].ID, a2.ID)
	}
}

// ST-005: MoveTo 在分支间导航。
func TestSessionTree_MoveToNavigatesBranches(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)

	// 从 root 创建分支
	branch1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(root.ID, branch1)

	// 当前活跃节点是 branch1
	if tree.ActiveID() != branch1.ID {
		t.Errorf("activeID = %q, want %q", tree.ActiveID(), branch1.ID)
	}

	// 导航到 a1 分支
	if err := tree.MoveTo(a1.ID); err != nil {
		t.Fatalf("MoveTo a1: %v", err)
	}
	if tree.ActiveID() != a1.ID {
		t.Errorf("activeID = %q, want %q", tree.ActiveID(), a1.ID)
	}

	// 验证 GetPath 返回 root → a1 路径
	path := tree.GetPath()
	if len(path) != 2 {
		t.Fatalf("path length = %d, want 2", len(path))
	}
	if path[0].ID != root.ID {
		t.Errorf("path[0] = %q, want %q", path[0].ID, root.ID)
	}
	if path[1].ID != a1.ID {
		t.Errorf("path[1] = %q, want %q", path[1].ID, a1.ID)
	}

	// 导航到 branch1
	_ = tree.MoveTo(branch1.ID)
	path2 := tree.GetPath()
	if len(path2) != 2 {
		t.Fatalf("path length = %d, want 2", len(path2))
	}
	if path2[1].ID != branch1.ID {
		t.Errorf("path[1] = %q, want %q", path2[1].ID, branch1.ID)
	}

	// MoveTo 不存在的节点应报错
	if err := tree.MoveTo("nonexistent"); err == nil {
		t.Error("MoveTo nonexistent: expected error, got nil")
	}
}

// ST-006: ListBranches 找到所有分支点。
func TestSessionTree_ListBranches(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)

	// root 暂时只有一个子节点，不是分支点
	branches := tree.ListBranches()
	if len(branches) != 0 {
		t.Errorf("branches = %d, want 0 (no fork yet)", len(branches))
	}

	// 从 root 创建分支
	_, _ = tree.Branch(root.ID, newTestNode("assistant", "hey!"))

	// 现在 root 有两个子节点，是分支点
	branches = tree.ListBranches()
	if len(branches) != 1 {
		t.Fatalf("branches = %d, want 1", len(branches))
	}
	if branches[0].ID != root.ID {
		t.Errorf("branch point = %q, want %q", branches[0].ID, root.ID)
	}

	// 从 a1 创建两个子节点使其成为分支点
	_, _ = tree.Branch(a1.ID, newTestNode("user", "follow up"))
	_, _ = tree.Branch(a1.ID, newTestNode("assistant", "sure"))

	branches = tree.ListBranches()
	if len(branches) != 2 {
		t.Fatalf("branches = %d, want 2", len(branches))
	}
}

// ─── 额外测试 ────────────────────────────────────────────────────

// TestSessionTree_GetBranchPath 测试指定节点路径查询。
func TestSessionTree_GetBranchPath(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)
	branch1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(root.ID, branch1)

	// 查询 branch1 的路径
	path, err := tree.GetBranchPath(branch1.ID)
	if err != nil {
		t.Fatalf("GetBranchPath: %v", err)
	}
	if len(path) != 2 {
		t.Fatalf("path length = %d, want 2", len(path))
	}
	if path[0].ID != root.ID {
		t.Errorf("path[0] = %q, want %q", path[0].ID, root.ID)
	}
	if path[1].ID != branch1.ID {
		t.Errorf("path[1] = %q, want %q", path[1].ID, branch1.ID)
	}

	// 查询不存在的节点
	_, err = tree.GetBranchPath("nonexistent")
	if err == nil {
		t.Error("GetBranchPath nonexistent: expected error, got nil")
	}
}

// TestSessionTree_AddNodeNil 测试 nil 节点报错。
func TestSessionTree_AddNodeNil(t *testing.T) {
	tree := NewSessionTree()
	if err := tree.AddNode(nil); err == nil {
		t.Error("AddNode nil: expected error, got nil")
	}
}

// TestSessionTree_BranchNilNode 测试 Branch nil 节点报错。
func TestSessionTree_BranchNilNode(t *testing.T) {
	tree := NewSessionTree()
	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)

	if _, err := tree.Branch(root.ID, nil); err == nil {
		t.Error("Branch nil node: expected error, got nil")
	}
}

// TestSessionTree_BranchNonexistentParent 测试 Branch 不存在的父节点报错。
func TestSessionTree_BranchNonexistentParent(t *testing.T) {
	tree := NewSessionTree()
	if _, err := tree.Branch("nonexistent", newTestNode("user", "x")); err == nil {
		t.Error("Branch nonexistent parent: expected error, got nil")
	}
}

// TestSessionTree_DuplicateID 测试重复 ID 报错。
func TestSessionTree_DuplicateID(t *testing.T) {
	tree := NewSessionTree()
	node := &TreeNode{ID: "dup-id", Role: "user", Content: "hello"}
	_ = tree.AddNode(node)

	dup := &TreeNode{ID: "dup-id", Role: "assistant", Content: "hi"}
	if err := tree.AddNode(dup); err == nil {
		t.Error("AddNode duplicate ID: expected error, got nil")
	}
}

// ─── FindLCA 测试 ────────────────────────────────────────────────

// LCA-001: 两个兄弟节点的 LCA 是它们的父节点。
func TestFindLCA_SiblingNodes(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	child1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(child1)

	// 从 root 创建分支，child1 和 branch1 是兄弟节点
	_ = tree.MoveTo(root.ID)
	branch1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(root.ID, branch1)

	// child1 和 branch1 的 LCA 应该是 root
	lca, err := tree.FindLCA(child1.ID, branch1.ID)
	if err != nil {
		t.Fatalf("FindLCA: %v", err)
	}
	if lca.ID != root.ID {
		t.Errorf("LCA of siblings = %q, want %q (parent)", lca.ID, root.ID)
	}
}

// LCA-002: 节点与其祖先的 LCA 是该祖先。
func TestFindLCA_NodeAndAncestor(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)
	a2 := newTestNode("user", "follow up")
	_ = tree.AddNode(a2)

	// a2 和 root 的 LCA 应该是 root
	lca, err := tree.FindLCA(a2.ID, root.ID)
	if err != nil {
		t.Fatalf("FindLCA: %v", err)
	}
	if lca.ID != root.ID {
		t.Errorf("LCA of node and ancestor = %q, want %q (ancestor)", lca.ID, root.ID)
	}

	// a2 和 a1 的 LCA 应该是 a1
	lca2, err := tree.FindLCA(a2.ID, a1.ID)
	if err != nil {
		t.Fatalf("FindLCA: %v", err)
	}
	if lca2.ID != a1.ID {
		t.Errorf("LCA of node and its parent = %q, want %q", lca2.ID, a1.ID)
	}
}

// LCA-003: 节点与自身的 LCA 是该节点自身。
func TestFindLCA_NodeWithItself(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)

	lca, err := tree.FindLCA(a1.ID, a1.ID)
	if err != nil {
		t.Fatalf("FindLCA: %v", err)
	}
	if lca.ID != a1.ID {
		t.Errorf("LCA of node with itself = %q, want %q", lca.ID, a1.ID)
	}
}

// LCA-004: 不同分支中节点的 LCA 是分支点。
func TestFindLCA_DifferentBranches(t *testing.T) {
	tree := NewSessionTree()

	// 构建树结构：
	// root → a1 → a2 → a3 (左分支)
	// root → b1 → b2 (右分支)
	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)
	a2 := newTestNode("user", "follow up")
	_ = tree.AddNode(a2)
	a3 := newTestNode("assistant", "answer")
	_ = tree.AddNode(a3)

	// 从 root 创建分支
	_ = tree.MoveTo(root.ID)
	b1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(root.ID, b1)
	b2 := newTestNode("user", "branch question")
	_ = tree.AddNode(b2)

	// a3 (左分支深处) 和 b2 (右分支深处) 的 LCA 应该是 root
	lca, err := tree.FindLCA(a3.ID, b2.ID)
	if err != nil {
		t.Fatalf("FindLCA: %v", err)
	}
	if lca.ID != root.ID {
		t.Errorf("LCA of different branches = %q, want %q (branch point)", lca.ID, root.ID)
	}
}

// LCA-005: 不存在的节点返回错误。
func TestFindLCA_NonexistentNode(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)

	_, err := tree.FindLCA(root.ID, "nonexistent")
	if err == nil {
		t.Error("FindLCA with nonexistent node2: expected error, got nil")
	}

	_, err = tree.FindLCA("nonexistent", root.ID)
	if err == nil {
		t.Error("FindLCA with nonexistent node1: expected error, got nil")
	}
}
