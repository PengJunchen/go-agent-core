package sharedctx

import (
	"context"
	"sort"
	"sync"
	"testing"
)

func TestNewSharedContext(t *testing.T) {
	sc := NewSharedContext()
	if sc == nil {
		t.Fatal("NewSharedContext() returned nil")
	}
	if len(sc.Keys()) != 0 {
		t.Fatalf("expected empty keys, got %v", sc.Keys())
	}
	if sc.Version() != 0 {
		t.Fatalf("expected version 0, got %d", sc.Version())
	}
}

func TestSetAndGet(t *testing.T) {
	sc := NewSharedContext()
	sc.Set("name", "alice")
	v, ok := sc.Get("name")
	if !ok {
		t.Fatal("expected key 'name' to exist")
	}
	if v != "alice" {
		t.Fatalf("expected 'alice', got %v", v)
	}
}

func TestGetNonExistent(t *testing.T) {
	sc := NewSharedContext()
	v, ok := sc.Get("missing")
	if ok {
		t.Fatal("expected ok=false for missing key")
	}
	if v != nil {
		t.Fatalf("expected nil value, got %v", v)
	}
}

func TestDelete(t *testing.T) {
	sc := NewSharedContext()
	sc.Set("k", "v")
	sc.Delete("k")
	_, ok := sc.Get("k")
	if ok {
		t.Fatal("expected key 'k' to be deleted")
	}
}

func TestKeys(t *testing.T) {
	sc := NewSharedContext()
	sc.Set("b", 2)
	sc.Set("a", 1)
	sc.Set("c", 3)

	keys := sc.Keys()
	sort.Strings(keys)

	want := []string{"a", "b", "c"}
	if len(keys) != len(want) {
		t.Fatalf("expected %d keys, got %d", len(want), len(keys))
	}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("key[%d]: expected %q, got %q", i, k, keys[i])
		}
	}
}

func TestSnapshot(t *testing.T) {
	sc := NewSharedContext()
	sc.Set("x", 10)
	sc.Set("y", "hello")

	snap := sc.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap))
	}
	if snap["x"] != 10 {
		t.Fatalf("expected snap['x']=10, got %v", snap["x"])
	}
	if snap["y"] != "hello" {
		t.Fatalf("expected snap['y']='hello', got %v", snap["y"])
	}

	// 快照是独立副本，修改不影响原数据
	snap["x"] = 999
	v, _ := sc.Get("x")
	if v != 10 {
		t.Fatalf("snapshot mutation affected original: expected 10, got %v", v)
	}
}

func TestVersion(t *testing.T) {
	sc := NewSharedContext()
	if sc.Version() != 0 {
		t.Fatalf("expected initial version 0, got %d", sc.Version())
	}

	sc.Set("a", 1)
	if sc.Version() != 1 {
		t.Fatalf("expected version 1 after Set, got %d", sc.Version())
	}

	sc.Set("b", 2)
	if sc.Version() != 2 {
		t.Fatalf("expected version 2 after second Set, got %d", sc.Version())
	}

	sc.Delete("a")
	if sc.Version() != 3 {
		t.Fatalf("expected version 3 after Delete, got %d", sc.Version())
	}

	sc.Clear()
	if sc.Version() != 4 {
		t.Fatalf("expected version 4 after Clear, got %d", sc.Version())
	}
}

func TestMerge(t *testing.T) {
	sc1 := NewSharedContext()
	sc1.Set("a", 1)
	sc1.Set("b", 2)

	sc2 := NewSharedContext()
	sc2.Set("b", 20)
	sc2.Set("c", 30)

	sc1.Merge(sc2)

	v, _ := sc1.Get("a")
	if v != 1 {
		t.Fatalf("expected a=1, got %v", v)
	}
	v, _ = sc1.Get("b")
	if v != 20 {
		t.Fatalf("expected b=20 (overwritten), got %v", v)
	}
	v, _ = sc1.Get("c")
	if v != 30 {
		t.Fatalf("expected c=30, got %v", v)
	}

	if len(sc1.Keys()) != 3 {
		t.Fatalf("expected 3 keys after merge, got %d", len(sc1.Keys()))
	}

	// version 应递增（a 已存在不算，但 b 覆盖和 c 新增都递增）
	if sc1.Version() != 4 { // Set(a)=1, Set(b)=2, merge(b)=3, merge(c)=4
		t.Fatalf("expected version 4 after merge, got %d", sc1.Version())
	}
}

func TestClear(t *testing.T) {
	sc := NewSharedContext()
	sc.Set("a", 1)
	sc.Set("b", 2)
	sc.Clear()

	if len(sc.Keys()) != 0 {
		t.Fatalf("expected 0 keys after Clear, got %d", len(sc.Keys()))
	}
	_, ok := sc.Get("a")
	if ok {
		t.Fatal("expected 'a' to be gone after Clear")
	}
}

func TestConcurrentAccess(t *testing.T) {
	sc := NewSharedContext()
	const goroutines = 100
	const opsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 3) // writers, readers, deleters

	// 并发写入
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				sc.Set("key", i*opsPerGoroutine+j)
			}
		}(i)
	}

	// 并发读取
	for range goroutines {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				_, _ = sc.Get("key")
			}
		}()
	}

	// 并发删除 + 重设
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				sc.Delete("key")
				sc.Set("key", i)
			}
		}(i)
	}

	wg.Wait()

	// 只要没有 data race 就算通过
	_ = sc.Version()
	_ = sc.Snapshot()
}

func TestContextIntegration(t *testing.T) {
	sc := NewSharedContext()
	sc.Set("agent", "loop")

	ctx := WithSharedContext(context.Background(), sc)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("expected SharedContext in context")
	}
	v, _ := got.Get("agent")
	if v != "loop" {
		t.Fatalf("expected agent=loop, got %v", v)
	}

	// 空 context 中取不到
	_, ok = FromContext(context.Background())
	if ok {
		t.Fatal("expected no SharedContext in empty context")
	}
}

func TestMustFromContext_Panic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from MustFromContext")
		}
	}()
	MustFromContext(context.Background())
}
