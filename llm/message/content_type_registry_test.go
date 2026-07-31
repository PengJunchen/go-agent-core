package message

import (
	"fmt"
	"sync"
	"testing"
)

// mockContentTypeHandler is a test implementation of ContentTypeHandler.
type mockContentTypeHandler struct {
	name string
	transform func(Content, string) (Content, error)
	validate func(Content) error
}

func (h *mockContentTypeHandler) Name() string { return h.name }
func (h *mockContentTypeHandler) Transform(c Content, p string) (Content, error) {
	if h.transform != nil {
		return h.transform(c, p)
	}
	return c, nil
}
func (h *mockContentTypeHandler) Validate(c Content) error {
	if h.validate != nil {
		return h.validate(c)
	}
	return nil
}

// TestContentTypeRegistry_RegisterAndGet verifies register and retrieve handler.
func TestContentTypeRegistry_RegisterAndGet(t *testing.T) {
	r := NewContentTypeRegistry()
	h := &mockContentTypeHandler{name: "audio"}
	r.Register(h)

	got, ok := r.Get("audio")
	if !ok {
		t.Fatal("expected handler to be found")
	}
	if got.Name() != "audio" {
		t.Errorf("handler name = %q, want %q", got.Name(), "audio")
	}
}

// TestContentTypeRegistry_DuplicatePanics verifies duplicate name panics.
func TestContentTypeRegistry_DuplicatePanics(t *testing.T) {
	r := NewContentTypeRegistry()
	r.Register(&mockContentTypeHandler{name: "audio"})

	defer func() {
		if rec := recover(); rec == nil {
			t.Error("expected panic on duplicate registration, but did not panic")
		}
	}()
	r.Register(&mockContentTypeHandler{name: "audio"})
}

// TestContentTypeRegistry_GetNonexistent verifies get non-existent returns false.
func TestContentTypeRegistry_GetNonexistent(t *testing.T) {
	r := NewContentTypeRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected false for non-existent handler")
	}
}

// TestContentTypeRegistry_List verifies list all registered names.
func TestContentTypeRegistry_List(t *testing.T) {
	r := NewContentTypeRegistry()
	r.Register(&mockContentTypeHandler{name: "audio"})
	r.Register(&mockContentTypeHandler{name: "video"})
	r.Register(&mockContentTypeHandler{name: "file"})

	names := r.List()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}

	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range []string{"audio", "video", "file"} {
		if !seen[want] {
			t.Errorf("expected name %q in list", want)
		}
	}
}

// TestContentTypeRegistry_Transform verifies handler transform is called.
func TestContentTypeRegistry_Transform(t *testing.T) {
	r := NewContentTypeRegistry()
	r.Register(&mockContentTypeHandler{
		name: "audio",
		transform: func(c Content, targetProvider string) (Content, error) {
			return Content{Type: ContentText, Text: "[Audio converted for " + targetProvider + "]"}, nil
		},
	})

	input := Content{Type: ContentText, Text: "raw audio data"}
	result, err := r.Transform("audio", input, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if result.Text != "[Audio converted for openai]" {
		t.Errorf("result = %q, want %q", result.Text, "[Audio converted for openai]")
	}
}

// TestContentTypeRegistry_TransformNonexistent verifies transform for non-existent type returns error.
func TestContentTypeRegistry_TransformNonexistent(t *testing.T) {
	r := NewContentTypeRegistry()
	_, err := r.Transform("nonexistent", Content{}, "openai")
	if err == nil {
		t.Error("expected error for non-existent handler, got nil")
	}
}

// TestContentTypeRegistry_Validate verifies handler validate is called.
func TestContentTypeRegistry_Validate(t *testing.T) {
	r := NewContentTypeRegistry()
	r.Register(&mockContentTypeHandler{
		name: "audio",
		validate: func(c Content) error {
			if c.Text == "" {
				return fmt.Errorf("audio content must have text data")
			}
			return nil
		},
	})

	if err := r.Validate("audio", Content{Type: ContentText, Text: "data"}); err != nil {
		t.Errorf("valid content should pass, got: %v", err)
	}
	if err := r.Validate("audio", Content{Type: ContentText, Text: ""}); err == nil {
		t.Error("invalid content should fail validation")
	}
}

// TestContentTypeRegistry_ValidateNonexistent verifies validate for non-existent type returns error.
func TestContentTypeRegistry_ValidateNonexistent(t *testing.T) {
	r := NewContentTypeRegistry()
	err := r.Validate("nonexistent", Content{})
	if err == nil {
		t.Error("expected error for non-existent handler, got nil")
	}
}

// TestContentTypeRegistry_ConcurrentAccess verifies concurrent register/get is safe.
func TestContentTypeRegistry_ConcurrentAccess(t *testing.T) {
	r := NewContentTypeRegistry()
	var wg sync.WaitGroup

	// Concurrent registers
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r.Register(&mockContentTypeHandler{name: fmt.Sprintf("type_%d", idx)})
		}(i)
	}
	wg.Wait()

	// Concurrent gets
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			h, ok := r.Get(fmt.Sprintf("type_%d", idx))
			if !ok {
				t.Errorf("handler type_%d not found", idx)
			}
			if ok && h.Name() != fmt.Sprintf("type_%d", idx) {
				t.Errorf("handler name = %q, want type_%d", h.Name(), idx)
			}
		}(i)
	}
	wg.Wait()
}

// TestContentTypeRegistry_DefaultRegistry verifies global default registry exists.
func TestContentTypeRegistry_DefaultRegistry(t *testing.T) {
	if DefaultContentTypeRegistry == nil {
		t.Fatal("DefaultContentTypeRegistry is nil")
	}
	names := DefaultContentTypeRegistry.List()
	// Default starts empty; just verify it's usable
	if names == nil {
		t.Error("List() returned nil, expected empty slice")
	}
}

// TestContentTypeHandlerFunc verifies function adapter works.
func TestContentTypeHandlerFunc(t *testing.T) {
	h := NewContentTypeHandlerFunc(
		func() string { return "custom" },
		func(c Content, targetProvider string) (Content, error) {
			return Content{Type: ContentText, Text: "transformed:" + c.Text}, nil
		},
		func(c Content) error {
			if c.Text == "" {
				return fmt.Errorf("empty text")
			}
			return nil
		},
	)

	if h.Name() != "custom" {
		t.Errorf("Name() = %q, want %q", h.Name(), "custom")
	}

	result, err := h.Transform(Content{Type: ContentText, Text: "hello"}, "openai")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if result.Text != "transformed:hello" {
		t.Errorf("Transform result = %q, want %q", result.Text, "transformed:hello")
	}

	if err := h.Validate(Content{Type: ContentText, Text: "ok"}); err != nil {
		t.Errorf("Validate valid content: %v", err)
	}
	if err := h.Validate(Content{Type: ContentText, Text: ""}); err == nil {
		t.Error("Validate invalid content should return error")
	}
}

// TestContentTypeRegistry_EmptyRegistry verifies empty registry handles gracefully.
func TestContentTypeRegistry_EmptyRegistry(t *testing.T) {
	r := NewContentTypeRegistry()

	// List on empty registry
	names := r.List()
	if len(names) != 0 {
		t.Errorf("empty registry List() = %v, want empty", names)
	}

	// Get on empty registry
	_, ok := r.Get("anything")
	if ok {
		t.Error("Get on empty registry should return false")
	}

	// Transform on empty registry
	_, err := r.Transform("anything", Content{}, "openai")
	if err == nil {
		t.Error("Transform on empty registry should return error")
	}

	// Validate on empty registry
	err = r.Validate("anything", Content{})
	if err == nil {
		t.Error("Validate on empty registry should return error")
	}
}
