package tools

import "sync"

// Package-level default instances shared by write_file and edit_file tools.
var (
	defaultMutationQueue = NewFileMutationQueue()
	defaultPathResolver *PathResolver
)

// FileMutationQueue serializes concurrent file mutations to the same path.
// Different paths can be written concurrently; same-path writes are serialized
// using per-path mutex locks to prevent race conditions.
type FileMutationQueue struct {
	locks sync.Map // map[string]*sync.Mutex
}

// NewFileMutationQueue creates a new FileMutationQueue.
func NewFileMutationQueue() *FileMutationQueue {
	return &FileMutationQueue{}
}

// WithLock acquires a per-path lock, executes fn, then releases the lock.
// Different paths can be written concurrently; same-path writes are serialized.
// It is safe for concurrent use.
func (q *FileMutationQueue) WithLock(path string, fn func() error) error {
	mu := q.getLock(path)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// getLock retrieves or creates a mutex for the given path.
func (q *FileMutationQueue) getLock(path string) *sync.Mutex {
	val, _ := q.locks.LoadOrStore(path, &sync.Mutex{})
	return val.(*sync.Mutex)
}
