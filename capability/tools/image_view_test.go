package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// IV-001: image_view reads a PNG image and returns base64 content.
func TestImageView_PNG(t *testing.T) {
	dir := t.TempDir()
	// Create a minimal valid PNG file (1x1 red pixel).
	pngData := createMinimalPNG()
	imgPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(imgPath, pngData, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": imgPath,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	// Should have data URI prefix for PNG.
	expected := "data:image/png;base64,"
	if len(result.Content) < len(expected) || result.Content[:len(expected)] != expected {
		t.Errorf("output should start with %q, got %q", expected, result.Content[:min(len(result.Content), 50)])
	}
	// Decode and compare.
	b64 := result.Content[len(expected):]
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode error: %v", err)
	}
	if len(decoded) != len(pngData) {
		t.Errorf("decoded length = %d, want %d", len(decoded), len(pngData))
	}
}

// IV-002: image_view reads a JPEG image.
func TestImageView_JPEG(t *testing.T) {
	dir := t.TempDir()
	// Create a minimal JPEG file.
	jpegData := createMinimalJPEG()
	imgPath := filepath.Join(dir, "test.jpg")
	if err := os.WriteFile(imgPath, jpegData, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": imgPath,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	expected := "data:image/jpeg;base64,"
	if len(result.Content) < len(expected) || result.Content[:len(expected)] != expected {
		t.Errorf("output should start with %q", expected)
	}
}

// IV-003: image_view reads a .jpeg extension (not just .jpg).
func TestImageView_JPEGExtension(t *testing.T) {
	dir := t.TempDir()
	jpegData := createMinimalJPEG()
	imgPath := filepath.Join(dir, "test.jpeg")
	if err := os.WriteFile(imgPath, jpegData, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": imgPath,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	expected := "data:image/jpeg;base64,"
	if len(result.Content) < len(expected) || result.Content[:len(expected)] != expected {
		t.Errorf("output should start with %q", expected)
	}
}

// IV-004: image_view reads a GIF image.
func TestImageView_GIF(t *testing.T) {
	dir := t.TempDir()
	gifData := createMinimalGIF()
	imgPath := filepath.Join(dir, "test.gif")
	if err := os.WriteFile(imgPath, gifData, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": imgPath,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	expected := "data:image/gif;base64,"
	if len(result.Content) < len(expected) || result.Content[:len(expected)] != expected {
		t.Errorf("output should start with %q", expected)
	}
}

// IV-005: image_view reads a WebP image.
func TestImageView_WebP(t *testing.T) {
	dir := t.TempDir()
	webpData := createMinimalWebP()
	imgPath := filepath.Join(dir, "test.webp")
	if err := os.WriteFile(imgPath, webpData, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": imgPath,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	expected := "data:image/webp;base64,"
	if len(result.Content) < len(expected) || result.Content[:len(expected)] != expected {
		t.Errorf("output should start with %q", expected)
	}
}

// IV-006: image_view rejects unsupported extensions.
func TestImageView_UnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.bmp")
	if err := os.WriteFile(imgPath, []byte("fake bmp"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": imgPath,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for unsupported extension")
	}
}

// IV-007: image_view returns error for missing path parameter.
func TestImageView_MissingPath(t *testing.T) {
	dir := t.TempDir()
	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for missing path")
	}
}

// IV-008: image_view returns error for non-existent file.
func TestImageView_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": filepath.Join(dir, "nonexistent.png"),
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for non-existent file")
	}
}

// IV-009: image_view rejects files larger than max_size.
func TestImageView_ExceedsMaxSize(t *testing.T) {
	dir := t.TempDir()
	// Create a file that exceeds the custom max_size.
	pngData := createMinimalPNG()
	imgPath := filepath.Join(dir, "big.png")
	if err := os.WriteFile(imgPath, pngData, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": imgPath,
		"max_size": 1, // 1 byte — way too small
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for file exceeding max_size")
	}
}

// IV-010: image_view resolves relative paths against workDir.
func TestImageView_RelativePath(t *testing.T) {
	dir := t.TempDir()
	pngData := createMinimalPNG()
	imgPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(imgPath, pngData, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": "test.png", // relative path
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
}

// IV-011: image_view is ParallelSafe.
func TestImageView_ParallelSafe(t *testing.T) {
	dir := t.TempDir()
	tool := NewImageViewTool(dir)
	if !tool.ParallelSafe {
		t.Error("image_view should be ParallelSafe")
	}
}

// IV-012: image_view respects context cancellation.
func TestImageView_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	tool := NewImageViewTool(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Handler(ctx, map[string]any{
		"path": "test.png",
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// IV-013: image_view non-string path returns error.
func TestImageView_InvalidPathType(t *testing.T) {
	dir := t.TempDir()
	tool := NewImageViewTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": 123,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for non-string path")
	}
}

// IV-014: image_view default max_size is 10MB.
func TestImageView_DefaultMaxSize(t *testing.T) {
	dir := t.TempDir()
	pngData := createMinimalPNG()
	imgPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(imgPath, pngData, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewImageViewTool(dir)
	// Without max_size param, 10MB default should accept small file.
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": imgPath,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
}

// --- Test helpers for creating minimal image files ---

// createMinimalPNG creates a minimal valid 1x1 PNG file.
func createMinimalPNG() []byte {
	// Minimal valid PNG: 8-byte signature + IHDR + IDAT + IEND
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length
		0x49, 0x48, 0x44, 0x52, // IHDR
		0x00, 0x00, 0x00, 0x01, // width: 1
		0x00, 0x00, 0x00, 0x01, // height: 1
		0x08, 0x02, // bit depth: 8, color type: 2 (RGB)
		0x00, 0x00, 0x00, // compression, filter, interlace
		0x90, 0x77, 0x53, 0xDE, // CRC
		0x00, 0x00, 0x00, 0x0C, // IDAT length
		0x49, 0x44, 0x41, 0x54, // IDAT
		0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, // compressed data
		0x00, 0x02, 0x00, 0x01, // compressed data + checksum
		0xE2, 0x21, 0xBC, 0x33, // CRC
		0x00, 0x00, 0x00, 0x00, // IEND length
		0x49, 0x45, 0x4E, 0x44, // IEND
		0xAE, 0x42, 0x60, 0x82, // CRC
	}
}

// createMinimalJPEG creates a minimal valid JPEG file.
func createMinimalJPEG() []byte {
	return []byte{
		0xFF, 0xD8, 0xFF, 0xE0, // SOI + APP0 marker
		0x00, 0x10, // APP0 length
		0x4A, 0x46, 0x49, 0x46, 0x00, // "JFIF\0"
		0x01, 0x01, // version
		0x00, // units
		0x00, 0x01, // X density
		0x00, 0x01, // Y density
		0x00, 0x00, // thumbnail
		0xFF, 0xD9, // EOI
	}
}

// createMinimalGIF creates a minimal valid GIF87a file.
func createMinimalGIF() []byte {
	return []byte{
		0x47, 0x49, 0x46, 0x38, 0x37, 0x61, // "GIF87a"
		0x01, 0x00, // width: 1
		0x01, 0x00, // height: 1
		0x00, // GCT flag
		0x00, 0x00, // background color + aspect ratio
		0x2C, // Image descriptor
		0x00, 0x00, 0x00, 0x00, // left, top
		0x01, 0x00, 0x01, 0x00, // width, height
		0x00, // no LCT
		0x02, 0x02, 0x44, 0x01, 0x00, // LZW min code size + block
		0x3B, // Trailer
	}
}

// createMinimalWebP creates a minimal valid WebP file.
func createMinimalWebP() []byte {
	// RIFF header + VP8 chunk
	return []byte{
		0x52, 0x49, 0x46, 0x46, // "RIFF"
		0x1A, 0x00, 0x00, 0x00, // file size - 8
		0x57, 0x45, 0x42, 0x50, // "WEBP"
		0x56, 0x50, 0x38, 0x20, // "VP8 "
		0x0E, 0x00, 0x00, 0x00, // chunk size
		0x00, 0x00, 0x00, 0x00, // frame tag (keyframe)
		0x9D, 0x01, 0x2A, // VP8 sync code
		0x01, 0x00, 0x01, 0x00, // width: 1, height: 1
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // padding
	}
}
