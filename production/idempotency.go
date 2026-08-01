package production

import (
	"context"
	"sync"
	"time"
)

// MemoryIdempotencyKey provides in-memory idempotency checking.
// It implements the IdempotencyKey interface with a concurrent-safe map
// and optional record expiration.
type MemoryIdempotencyKey struct {
	mu sync.RWMutex
	records map[string]*IdempotencyRecord
}

// NewMemoryIdempotencyKey creates a new in-memory idempotency key store.
func NewMemoryIdempotencyKey() *MemoryIdempotencyKey {
	return &MemoryIdempotencyKey{
		records: make(map[string]*IdempotencyRecord),
	}
}

// Check checks if this operation was already executed.
// Returns the record and true if found and not expired, nil and false otherwise.
func (m *MemoryIdempotencyKey) Check(_ context.Context, key string) (*IdempotencyRecord, bool, error) {
	m.mu.RLock()
	rec, ok := m.records[key]
	if !ok {
		m.mu.RUnlock()
		return nil, false, nil
	}

	// Check expiration
	if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) {
		m.mu.RUnlock()
		// Clean up expired record
		m.mu.Lock()
		delete(m.records, key)
		m.mu.Unlock()
		return nil, false, nil
	}

	m.mu.RUnlock()
	return rec, true, nil
}

// Record records a completed operation with its result.
// If a record for the key already exists, it is overwritten.
func (m *MemoryIdempotencyKey) Record(_ context.Context, key string, result any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.records[key] = &IdempotencyRecord{
		Key: key,
		Result: result,
		CreatedAt: time.Now(),
	}
	return nil
}

// Delete removes an idempotency record.
// Deleting a non-existent key is a no-op.
func (m *MemoryIdempotencyKey) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.records, key)
	return nil
}
