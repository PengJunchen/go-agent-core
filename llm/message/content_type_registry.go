package message

import (
	"fmt"
	"sync"
)

// ContentTypeRegistry manages custom content types and their handlers.
// Thread-safe. Allows runtime registration of new content types beyond
// the built-in ContentText/ContentThinking/ContentImage.
type ContentTypeRegistry struct {
	mu sync.RWMutex
	types map[string]ContentTypeHandler
}

// ContentTypeHandler handles transformation for a specific content type.
type ContentTypeHandler interface {
	// Name returns the content type name (e.g., "audio", "video", "file").
	Name() string
	// Transform converts content for a target provider.
	// Returns the transformed Content or an error.
	Transform(content Content, targetProvider string) (Content, error)
	// Validate checks if the content is valid for this type.
	Validate(content Content) error
}

// ContentTypeHandlerFunc is a function adapter for ContentTypeHandler.
type ContentTypeHandlerFunc struct {
	nameFn func() string
	transformFn func(Content, string) (Content, error)
	validateFn func(Content) error
}

// NewContentTypeHandlerFunc creates a ContentTypeHandlerFunc from functions.
func NewContentTypeHandlerFunc(
	name func() string,
	transform func(Content, string) (Content, error),
	validate func(Content) error,
) ContentTypeHandlerFunc {
	return ContentTypeHandlerFunc{
		nameFn: name,
		transformFn: transform,
		validateFn: validate,
	}
}

// Name implements ContentTypeHandler.
func (h ContentTypeHandlerFunc) Name() string {
	return h.nameFn()
}

// Transform implements ContentTypeHandler.
func (h ContentTypeHandlerFunc) Transform(content Content, targetProvider string) (Content, error) {
	return h.transformFn(content, targetProvider)
}

// Validate implements ContentTypeHandler.
func (h ContentTypeHandlerFunc) Validate(content Content) error {
	return h.validateFn(content)
}

// NewContentTypeRegistry creates a new empty registry.
func NewContentTypeRegistry() *ContentTypeRegistry {
	return &ContentTypeRegistry{
		types: make(map[string]ContentTypeHandler),
	}
}

// Register adds a new content type handler. Panics on duplicate name.
func (r *ContentTypeRegistry) Register(handler ContentTypeHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := handler.Name()
	if _, exists := r.types[name]; exists {
		panic(fmt.Sprintf("content type handler already registered: %s", name))
	}
	r.types[name] = handler
}

// Get retrieves a handler by content type name.
func (r *ContentTypeRegistry) Get(name string) (ContentTypeHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.types[name]
	return h, ok
}

// List returns all registered content type names.
func (r *ContentTypeRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.types))
	for name := range r.types {
		names = append(names, name)
	}
	return names
}

// Transform applies the handler's Transform method for the given content type name.
// Returns an error if no handler is registered for the content type.
func (r *ContentTypeRegistry) Transform(name string, content Content, targetProvider string) (Content, error) {
	r.mu.RLock()
	h, ok := r.types[name]
	r.mu.RUnlock()
	if !ok {
		return Content{}, fmt.Errorf("no handler registered for content type: %s", name)
	}
	return h.Transform(content, targetProvider)
}

// Validate applies the handler's Validate method for the given content type name.
// Returns an error if no handler is registered for the content type.
func (r *ContentTypeRegistry) Validate(name string, content Content) error {
	r.mu.RLock()
	h, ok := r.types[name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no handler registered for content type: %s", name)
	}
	return h.Validate(content)
}

// DefaultContentTypeRegistry is the global default content type registry.
var DefaultContentTypeRegistry = NewContentTypeRegistry()
