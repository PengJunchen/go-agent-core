package production

import (
	"context"
	"testing"
	"time"
)

func TestMemoryIdempotencyKey_Interface(t *testing.T) {
	var _ IdempotencyKey = (*MemoryIdempotencyKey)(nil)
}

func TestMemoryIdempotencyKey_CheckNotFound(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	rec, found, err := ik.Check(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("Check err = %v", err)
	}
	if found {
		t.Error("found = true, want false")
	}
	if rec != nil {
		t.Error("rec should be nil")
	}
}

func TestMemoryIdempotencyKey_RecordAndCheck(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	err := ik.Record(context.Background(), "key-1", "result-1")
	if err != nil {
		t.Errorf("Record err = %v", err)
	}
	rec, found, err := ik.Check(context.Background(), "key-1")
	if err != nil {
		t.Errorf("Check err = %v", err)
	}
	if !found {
		t.Error("found = false, want true")
	}
	if rec == nil {
		t.Fatal("rec should not be nil")
	}
	if rec.Result != "result-1" {
		t.Errorf("Result = %v, want result-1", rec.Result)
	}
}

func TestMemoryIdempotencyKey_Delete(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	_ = ik.Record(context.Background(), "key-1", "result-1")
	err := ik.Delete(context.Background(), "key-1")
	if err != nil {
		t.Errorf("Delete err = %v", err)
	}
	_, found, _ := ik.Check(context.Background(), "key-1")
	if found {
		t.Error("found should be false after delete")
	}
}

func TestMemoryIdempotencyKey_ExpiredRecord(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	ik.mu.Lock()
	ik.records["expired"] = &IdempotencyRecord{
		Key: "expired",
		Result: "old",
		CreatedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour), // already expired
	}
	ik.mu.Unlock()

	rec, found, err := ik.Check(context.Background(), "expired")
	if err != nil {
		t.Errorf("Check err = %v", err)
	}
	if found {
		t.Error("expired record should not be found")
	}
	if rec != nil {
		t.Error("rec should be nil for expired")
	}
}

func TestMemoryIdempotencyKey_NonExpiredRecord(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	ik.mu.Lock()
	ik.records["valid"] = &IdempotencyRecord{
		Key: "valid",
		Result: "current",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	ik.mu.Unlock()

	rec, found, _ := ik.Check(context.Background(), "valid")
	if !found {
		t.Error("non-expired record should be found")
	}
	if rec.Result != "current" {
		t.Errorf("Result = %v, want current", rec.Result)
	}
}

func TestMemoryIdempotencyKey_ConcurrentSafety(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	done := make(chan struct{})

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			_ = ik.Record(context.Background(), "key", i)
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			_, _, _ = ik.Check(context.Background(), "key")
		}
	}()

	<-done
	<-done
}

func TestMemoryIdempotencyKey_DeleteNonExistent(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	err := ik.Delete(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("Delete non-existent key err = %v", err)
	}
}

func TestMemoryIdempotencyKey_OverwriteRecord(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	_ = ik.Record(context.Background(), "key-1", "result-1")
	_ = ik.Record(context.Background(), "key-1", "result-2")

	rec, found, _ := ik.Check(context.Background(), "key-1")
	if !found {
		t.Error("found = false, want true")
	}
	if rec.Result != "result-2" {
		t.Errorf("Result = %v, want result-2", rec.Result)
	}
}

func TestMemoryIdempotencyKey_NoExpiryRecord(t *testing.T) {
	ik := NewMemoryIdempotencyKey()
	_ = ik.Record(context.Background(), "key-1", "result-1")

	rec, found, _ := ik.Check(context.Background(), "key-1")
	if !found {
		t.Error("record with no expiry should be found")
	}
	if rec.ExpiresAt.IsZero() {
		// Expected: ExpiresAt is zero when not set
	} else {
		t.Error("ExpiresAt should be zero for record without expiry")
	}
}
