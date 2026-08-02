package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// CredentialStore 提供令牌的持久化存储抽象，使刷新后的令牌可在进程间复用。
type CredentialStore interface {
	Save(ctx context.Context, key string, token *Token) error
	Load(ctx context.Context, key string) (*Token, error)
	Delete(ctx context.Context, key string) error
}

// FileCredentialStore 将令牌以 JSON 文件形式存储在指定目录下。
// 每个文件权限为 0600 以保证安全性，并通过 per-key 互斥锁保证并发安全。
type FileCredentialStore struct {
	dir string
	mus sync.Map // map[string]*sync.Mutex
}

// NewFileCredentialStore 构造一个基于文件系统的凭证存储。
// dir 为存储目录，若不存在会自动创建（权限 0700）。
func NewFileCredentialStore(dir string) (*FileCredentialStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create credential dir %q: %w", dir, err)
	}
	return &FileCredentialStore{dir: dir}, nil
}

// lockFor 返回指定 key 对应的互斥锁（不存在则创建）。
func (s *FileCredentialStore) lockFor(key string) *sync.Mutex {
	v, _ := s.mus.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *FileCredentialStore) path(key string) string {
	return filepath.Join(s.dir, key+".json")
}

// Save 将令牌以 JSON 文件形式写入存储（文件权限 0600）。
func (s *FileCredentialStore) Save(ctx context.Context, key string, token *Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("token is nil")
	}

	mu := s.lockFor(key)
	mu.Lock()
	defer mu.Unlock()

	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	if err := os.WriteFile(s.path(key), data, 0o600); err != nil {
		return fmt.Errorf("write credential file: %w", err)
	}
	return nil
}

// Load 读取并解析指定 key 的令牌文件。
func (s *FileCredentialStore) Load(ctx context.Context, key string) (*Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	mu := s.lockFor(key)
	mu.Lock()
	defer mu.Unlock()

	data, err := os.ReadFile(s.path(key))
	if err != nil {
		return nil, fmt.Errorf("read credential file: %w", err)
	}
	var token Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("unmarshal token: %w", err)
	}
	return &token, nil
}

// Delete 删除指定 key 的令牌文件。文件不存在时不返回错误。
func (s *FileCredentialStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	mu := s.lockFor(key)
	mu.Lock()
	defer mu.Unlock()

	if err := os.Remove(s.path(key)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove credential file: %w", err)
	}
	return nil
}
