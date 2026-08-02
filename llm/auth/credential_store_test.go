package auth

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// CS-001 (AC-1): FileCredentialStore 保存后可加载令牌。
func TestFileCredentialStore_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	want := &Token{
		AccessToken: "access-123",
		RefreshToken: "refresh-456",
		ExpiresAt: time.Now().Add(time.Hour).UTC(),
		TokenType: "Bearer",
	}

	if err := store.Save(context.Background(), "test-key", want); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	got, err := store.Load(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if got.AccessToken != want.AccessToken {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, want.AccessToken)
	}
	if got.RefreshToken != want.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, want.RefreshToken)
	}
	if got.TokenType != want.TokenType {
		t.Errorf("TokenType = %q, want %q", got.TokenType, want.TokenType)
	}
	// ExpiresAt may have sub-microsecond rounding in JSON; compare within 1s
	if got.ExpiresAt.Sub(want.ExpiresAt) > time.Second {
		t.Errorf("ExpiresAt = %v, want ~%v", got.ExpiresAt, want.ExpiresAt)
	}
}

// CS-002: 加载不存在的 key 返回错误。
func TestFileCredentialStore_LoadMissingKey(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	_, err = store.Load(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Load() expected error for missing key, got nil")
	}
}

// CS-003: Delete 移除文件后 Load 失败。
func TestFileCredentialStore_DeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	tok := &Token{AccessToken: "to-delete", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Save(context.Background(), "del-key", tok); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	if err := store.Delete(context.Background(), "del-key"); err != nil {
		t.Fatalf("Delete() err = %v", err)
	}
	if _, err := store.Load(context.Background(), "del-key"); err == nil {
		t.Error("Load() after Delete should return error")
	}
}

// CS-004: Delete 不存在的 key 不返回错误。
func TestFileCredentialStore_DeleteMissingNoError(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	if err := store.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("Delete() missing key err = %v, want nil", err)
	}
}

// CS-005: 文件权限为 0600。
func TestFileCredentialStore_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	tok := &Token{AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Save(context.Background(), "perm-key", tok); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "perm-key.json"))
	if err != nil {
		t.Fatalf("Stat() err = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
}

// CS-006: 并发 Save/Load 同一 key 不触发 race（-race 验证）。
func TestFileCredentialStore_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	tok := &Token{AccessToken: "concurrent", ExpiresAt: time.Now().Add(time.Hour)}
	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = store.Save(context.Background(), "concurrent-key", tok)
		}()
		go func() {
			defer wg.Done()
			_, _ = store.Load(context.Background(), "concurrent-key")
		}()
	}
	wg.Wait()
}

// CS-007: Save nil 令牌返回错误。
func TestFileCredentialStore_SaveNilToken(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	if err := store.Save(context.Background(), "nil-key", nil); err == nil {
		t.Error("Save() nil token expected error, got nil")
	}
}

// CS-008: 构造时自动创建目录。
func TestFileCredentialStore_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "creds")
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}

	// Verify store is usable
	tok := &Token{AccessToken: "created", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Save(context.Background(), "k", tok); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
}

// CS-009: CredentialStore 接口由 FileCredentialStore 实现。
func TestCredentialStore_Interface(t *testing.T) {
	var _ CredentialStore = (*FileCredentialStore)(nil)
}
