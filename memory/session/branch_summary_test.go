package session

import (
	"strings"
	"testing"
	"time"
)

// ─── BS-001: SummarizeBranch generates summary ─────────────────

func TestBranchSummary_GeneratesSummary(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi there")
	_ = tree.AddNode(a1)
	a2 := newTestNode("user", "how are you?")
	_ = tree.AddNode(a2)

	// 使用默认摘要器
	summary, err := tree.SummarizeBranch(a2.ID, nil)
	if err != nil {
		t.Fatalf("SummarizeBranch: %v", err)
	}

	if summary.BranchID != a2.ID {
		t.Errorf("BranchID = %q, want %q", summary.BranchID, a2.ID)
	}
	if summary.ItemCount != 3 {
		t.Errorf("ItemCount = %d, want 3", summary.ItemCount)
	}
	if summary.Summary == "" {
		t.Error("Summary is empty")
	}
	if !strings.Contains(summary.Summary, "[user]") || !strings.Contains(summary.Summary, "[assistant]") {
		t.Errorf("Summary should contain role labels, got %q", summary.Summary)
	}
	if summary.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	// 不存在的节点应报错
	_, err = tree.SummarizeBranch("nonexistent", nil)
	if err == nil {
		t.Error("SummarizeBranch nonexistent: expected error, got nil")
	}
}

// ─── BS-002: GetRetainedTail returns last N items ──────────────

func TestRetainedTail_ReturnsLastN(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)
	a2 := newTestNode("user", "how are you?")
	_ = tree.AddNode(a2)
	a3 := newTestNode("assistant", "fine")
	_ = tree.AddNode(a3)
	a4 := newTestNode("user", "great")
	_ = tree.AddNode(a4)

	// 获取最后 2 个节点
	tail := tree.GetRetainedTail(2)
	if len(tail) != 2 {
		t.Fatalf("tail length = %d, want 2", len(tail))
	}
	if tail[0].ID != a3.ID {
		t.Errorf("tail[0].ID = %q, want %q", tail[0].ID, a3.ID)
	}
	if tail[1].ID != a4.ID {
		t.Errorf("tail[1].ID = %q, want %q", tail[1].ID, a4.ID)
	}

	// 获取最后 10 个节点（超出路径长度）
	tailAll := tree.GetRetainedTail(10)
	if len(tailAll) != 5 {
		t.Fatalf("tail length = %d, want 5 (full path)", len(tailAll))
	}

	// 获取 0 个节点
	tailZero := tree.GetRetainedTail(0)
	if len(tailZero) != 0 {
		t.Errorf("tail length = %d, want 0", len(tailZero))
	}

	// 获取最后 1 个节点
	tailOne := tree.GetRetainedTail(1)
	if len(tailOne) != 1 {
		t.Fatalf("tail length = %d, want 1", len(tailOne))
	}
	if tailOne[0].ID != a4.ID {
		t.Errorf("tail[0].ID = %q, want %q", tailOne[0].ID, a4.ID)
	}
}

// ─── BS-003: ListAllBranches finds all paths ───────────────────

func TestListAllBranches_FindsAllPaths(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)

	// 目前只有一条路径，无分支点
	branches := tree.ListAllBranches()
	if len(branches) != 1 {
		t.Fatalf("branches count = %d, want 1", len(branches))
	}

	// 从 root 创建分支
	branch1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(root.ID, branch1)

	// 现在有两条分支
	branches = tree.ListAllBranches()
	if len(branches) != 2 {
		t.Fatalf("branches count = %d, want 2", len(branches))
	}

	// 每条分支都应包含从分支点（root）到叶子的节点
	for i, branch := range branches {
		if len(branch) < 1 {
			t.Errorf("branch %d: length = %d, want >= 1", i, len(branch))
		}
		// 分支起点是 root（分支点）
		if branch[0].ID != root.ID {
			t.Errorf("branch %d start = %q, want %q (root)", i, branch[0].ID, root.ID)
		}
	}

	// 在 a1 上继续添加节点，创建更深的分支
	_ = tree.MoveTo(a1.ID)
	a2 := newTestNode("user", "continue")
	_ = tree.AddNode(a2)

	branches = tree.ListAllBranches()
	if len(branches) != 2 {
		t.Fatalf("branches count = %d, want 2", len(branches))
	}
}

// TestListAllBranches_MultipleForkPoints 测试多级分支点。
func TestListAllBranches_MultipleForkPoints(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)
	a2 := newTestNode("user", "follow up")
	_ = tree.AddNode(a2) // root → a1 → a2

	// 从 root 创建分支
	branch1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(root.ID, branch1) // root → branch1

	// 在 a1 上也创建分支
	_ = tree.MoveTo(a1.ID)
	branch2 := newTestNode("user", "alt follow-up")
	_, _ = tree.Branch(a1.ID, branch2) // root → a1 → branch2

	// 应有 3 条分支：root→a1→a2, root→a1→branch2, root→branch1
	branches := tree.ListAllBranches()
	if len(branches) != 3 {
		t.Fatalf("branches count = %d, want 3", len(branches))
	}
}

// ─── BS-004: Custom summarizer function is used when provided ──

func TestBranchSummary_CustomSummarizer(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi there")
	_ = tree.AddNode(a1)

	// 使用自定义摘要器
	customSummarizer := func(nodes []*TreeNode) string {
		return "custom-summary"
	}

	summary, err := tree.SummarizeBranch(a1.ID, customSummarizer)
	if err != nil {
		t.Fatalf("SummarizeBranch: %v", err)
	}

	if summary.Summary != "custom-summary" {
		t.Errorf("Summary = %q, want %q", summary.Summary, "custom-summary")
	}
	if summary.ItemCount != 2 {
		t.Errorf("ItemCount = %d, want 2", summary.ItemCount)
	}
}

// ─── BranchSummary.ParentID tests ──────────────────────────────

func TestBranchSummary_ParentID(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)

	// 无分支点时 ParentID 应为空
	summary, err := tree.SummarizeBranch(a1.ID, nil)
	if err != nil {
		t.Fatalf("SummarizeBranch: %v", err)
	}
	if summary.ParentID != "" {
		t.Errorf("ParentID = %q, want empty (no fork point)", summary.ParentID)
	}

	// 从 root 创建分支
	branch1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(root.ID, branch1)

	// 现在 root 是分支点，branch1 的 ParentID 应为 root.ID
	summary2, err := tree.SummarizeBranch(branch1.ID, nil)
	if err != nil {
		t.Fatalf("SummarizeBranch: %v", err)
	}
	if summary2.ParentID != root.ID {
		t.Errorf("ParentID = %q, want %q", summary2.ParentID, root.ID)
	}
}

// ─── GetRetainedTail with branching ───────────────────────────

func TestRetainedTail_WithBranching(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)
	a1 := newTestNode("assistant", "hi")
	_ = tree.AddNode(a1)
	a2 := newTestNode("user", "how are you?")
	_ = tree.AddNode(a2)

	// 从 a1 创建分支
	branch1 := newTestNode("assistant", "hey!")
	_, _ = tree.Branch(a1.ID, branch1)
	b1_child := newTestNode("user", "branch content")
	_ = tree.AddNode(b1_child)

	// 当前活跃分支是 root → a1 → branch1 → b1_child
	tail := tree.GetRetainedTail(2)
	if len(tail) != 2 {
		t.Fatalf("tail length = %d, want 2", len(tail))
	}
	if tail[0].ID != branch1.ID {
		t.Errorf("tail[0].ID = %q, want %q", tail[0].ID, branch1.ID)
	}
	if tail[1].ID != b1_child.ID {
		t.Errorf("tail[1].ID = %q, want %q", tail[1].ID, b1_child.ID)
	}
}

// ─── GetRetainedTail empty tree ────────────────────────────────

func TestRetainedTail_EmptyTree(t *testing.T) {
	tree := NewSessionTree()

	tail := tree.GetRetainedTail(5)
	if len(tail) != 0 {
		t.Errorf("tail length = %d, want 0 for empty tree", len(tail))
	}
}

// ─── defaultSummarizer content truncation ──────────────────────

func TestDefaultSummarizer_Truncation(t *testing.T) {
	tree := NewSessionTree()

	longContent := strings.Repeat("x", 200)
	root := newTestNode("user", longContent)
	_ = tree.AddNode(root)

	summary, err := tree.SummarizeBranch(root.ID, nil)
	if err != nil {
		t.Fatalf("SummarizeBranch: %v", err)
	}

	// 默认摘要器截断到 100 字符 + "..."
	if !strings.Contains(summary.Summary, "...") {
		t.Error("Summary should contain '...' for long content")
	}
}

// ─── BranchSummary.CreatedAt is recent ────────────────────────

func TestBranchSummary_CreatedAtRecent(t *testing.T) {
	tree := NewSessionTree()

	root := newTestNode("user", "hello")
	_ = tree.AddNode(root)

	before := time.Now()
	summary, err := tree.SummarizeBranch(root.ID, nil)
	after := time.Now()

	if err != nil {
		t.Fatalf("SummarizeBranch: %v", err)
	}

	if summary.CreatedAt.Before(before) || summary.CreatedAt.After(after) {
		t.Errorf("CreatedAt = %v, expected between %v and %v", summary.CreatedAt, before, after)
	}
}
