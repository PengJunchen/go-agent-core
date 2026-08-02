package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// PR-001 (AC-6): Relative paths are joined with workDir.
func TestPathResolver_RelativePath(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	resolved, err := r.Resolve("sub/file.txt")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	expected := filepath.Join(dir, "sub", "file.txt")
	if resolved != expected {
		t.Errorf("resolved = %q, want %q", resolved, expected)
	}
}

// PR-002 (AC-6): Relative path with dot/dotdot is cleaned and kept within sandbox.
func TestPathResolver_RelativePathCleaned(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	resolved, err := r.Resolve("sub/../file.txt")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	expected := filepath.Join(dir, "file.txt")
	if resolved != expected {
		t.Errorf("resolved = %q, want %q", resolved, expected)
	}
}

// PR-003 (AC-5): Path traversal with ../../../etc/passwd is rejected.
func TestPathResolver_TraversalRejected(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	_, err := r.Resolve("../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error should mention 'escapes', got: %v", err)
	}
}

// PR-004 (AC-5): Path traversal with ../../etc/passwd is rejected (double dot).
func TestPathResolver_TraversalDoubleDotRejected(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	_, err := r.Resolve("../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error should mention 'escapes', got: %v", err)
	}
}

// PR-005 (AC-5): Absolute path outside workDir is rejected.
func TestPathResolver_AbsoluteOutsideWorkDirRejected(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	_, err := r.Resolve("/etc/passwd")
	if err == nil {
		t.Error("expected error for absolute path outside workDir")
	}
}

// PR-006 (AC-5): Absolute path inside workDir is allowed.
func TestPathResolver_AbsoluteInsideWorkDirAllowed(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	absPath := filepath.Join(dir, "sub", "file.txt")
	resolved, err := r.Resolve(absPath)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved != absPath {
		t.Errorf("resolved = %q, want %q", resolved, absPath)
	}
}

// PR-007 (AC-2): Unicode NFC normalization with accented characters.
// "café" in NFD: c-a-f-e-U+0301 (e + combining acute).
// "café" in NFC: c-a-f-U+00E9 (precomposed é).
func TestPathResolver_NFCNormalization_Accented(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	// NFD form: "cafe" + combining acute (U+0301).
	nfdInput := "cafe\u0301.txt"
	resolved, err := r.Resolve(nfdInput)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	// The resolved path should be in NFC form.
	nfcExpected := norm.NFC.String(filepath.Join(dir, "cafe\u0301.txt"))
	if resolved != nfcExpected {
		t.Errorf("resolved bytes = % x, want % x (NFC normalized)", []byte(resolved), []byte(nfcExpected))
	}

	// Verify the resolved path is actually NFC.
	if !norm.NFC.IsNormalString(resolved) {
		t.Errorf("resolved path %q is not NFC normalized", resolved)
	}

	// Verify the é character was composed (U+00E9 = 0xC3 0xA9 in UTF-8).
	if !strings.Contains(resolved, "\u00e9") {
		t.Errorf("resolved path should contain precomposed é (U+00E9), got bytes: % x", []byte(resolved))
	}
}

// PR-008 (AC-2): Unicode NFC normalization with Chinese characters.
func TestPathResolver_NFCNormalization_Chinese(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	resolved, err := r.Resolve("测试文件.txt")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	expected := filepath.Join(dir, "测试文件.txt")
	if resolved != expected {
		t.Errorf("resolved = %q, want %q", resolved, expected)
	}
}

// PR-009 (AC-2): Unicode NFC normalization with precomposed vs decomposed CJK.
// "가" (U+AC00) has NFC/NFD forms.
func TestPathResolver_NFCNormalization_Korean(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	// NFD form of "가" (U+AC00): ㄱ (U+1100) + ㅏ (U+1161).
	nfdInput := "\u1100\u1161.txt"
	resolved, err := r.Resolve(nfdInput)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	nfcExpected := norm.NFC.String(filepath.Join(dir, "\u1100\u1161.txt"))
	if resolved != nfcExpected {
		t.Errorf("resolved bytes = % x, want % x (NFC normalized)", []byte(resolved), []byte(nfcExpected))
	}
}

// PR-010 (AC-4): Tilde expansion for ~/path.
func TestPathResolver_TildeExpansion(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("os.UserHomeDir() failed: %v", err)
	}

	// ~/foo should resolve to $HOME/foo — but this is outside workDir (temp dir).
	// So we expect a sandbox "escapes" error, confirming tilde expansion happened.
	_, resolveErr := r.Resolve("~/foo.txt")
	if resolveErr == nil {
		t.Error("expected 'escapes' error for ~/foo outside workDir")
	} else if !strings.Contains(resolveErr.Error(), "escapes") {
		t.Errorf("expected 'escapes' error, got: %v", resolveErr)
	}

	// Verify the error message references the home directory (proving tilde was expanded).
	if !strings.Contains(resolveErr.Error(), home) {
		t.Errorf("error should reference home dir %q, got: %v", home, resolveErr)
	}
}

// PR-011 (AC-4): Tilde expansion when workDir IS the home directory.
func TestPathResolver_TildeExpansionInHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("os.UserHomeDir() failed: %v", err)
	}

	r := NewPathResolver(home)
	resolved, err := r.Resolve("~/test_tilde.txt")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	expected := filepath.Join(home, "test_tilde.txt")
	if resolved != expected {
		t.Errorf("resolved = %q, want %q", resolved, expected)
	}
}

// PR-012 (AC-4): Bare tilde expands to home directory.
func TestPathResolver_BareTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("os.UserHomeDir() failed: %v", err)
	}

	r := NewPathResolver(home)
	resolved, err := r.Resolve("~")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved != home {
		t.Errorf("resolved = %q, want %q", resolved, home)
	}
}

// PR-013 (AC-3): macOS HFS+ NFD→NFC fallback.
// Create a file with NFC name, look up with NFD, and vice versa.
func TestPathResolver_NFCNFD_Fallback(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	// Create a file with an NFC filename: café.txt (é = U+00E9).
	nfcPath := filepath.Join(dir, "cafe\u0301.txt") // NFC after filepath.Join
	nfcPath = norm.NFC.String(nfcPath)
	if err := os.WriteFile(nfcPath, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Look up with NFD form of the same name: cafe + combining acute (U+0301).
	nfdInput := "cafe\u0301.txt" // NFD: e + combining acute
	resolved, err := r.Resolve(nfdInput)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	// The resolved path should point to the existing file (either NFC or NFD).
	_, statErr := os.Stat(resolved)
	if statErr != nil {
		t.Errorf("resolved path %q does not exist: %v", resolved, statErr)
	}
}

// PR-014 (AC-3): NFC→NFD fallback when file exists in NFD form.
func TestPathResolver_NFDFile_NFCLookup(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS normalizes filenames automatically; test is for non-macOS behavior")
	}

	dir := t.TempDir()
	r := NewPathResolver(dir)

	// Create a file with an NFD filename (decomposed form): cafe + combining acute.
	nfdName := "cafe\u0301.txt"
	nfdPath := filepath.Join(dir, nfdName)
	if err := os.WriteFile(nfdPath, []byte("nfd"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify the filesystem actually preserved the NFD form.
	// Some Linux filesystems (e.g. overlayfs in containers) normalize to NFC.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries in temp dir")
	}
	actualName := entries[0].Name()
	if actualName != nfdName {
		t.Skipf("filesystem normalized NFD filename to %q; skipping NFD lookup test", actualName)
	}

	// Look up with NFC form of the same name: café (é = U+00E9).
	nfcInput := "cafe\u00e9.txt" // NFC: precomposed é
	resolved, err := r.Resolve(nfcInput)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	// The resolver should find the NFD-named file.
	_, statErr := os.Stat(resolved)
	if statErr != nil {
		t.Errorf("resolved path %q does not exist: %v", resolved, statErr)
	}
}

// PR-015: Nonexistent file still resolves to NFC path.
func TestPathResolver_NonexistentFileReturnsNFC(t *testing.T) {
	dir := t.TempDir()
	r := NewPathResolver(dir)

	// NFD form of "café": cafe + combining acute.
	nfdInput := "cafe\u0301_nonexistent.txt"
	resolved, err := r.Resolve(nfdInput)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	// Should return the NFC form since neither NFC nor NFD file exists.
	nfcExpected := norm.NFC.String(filepath.Join(dir, nfdInput))
	if resolved != nfcExpected {
		t.Errorf("resolved = %q, want %q (NFC for nonexistent file)", resolved, nfcExpected)
	}
}

// PR-016: Empty workDir is handled gracefully (resolves to CWD).
func TestPathResolver_EmptyWorkDir(t *testing.T) {
	r := NewPathResolver("")

	resolved, err := r.Resolve("test.txt")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	cwd, _ := os.Getwd()
	expected := filepath.Join(cwd, "test.txt")
	if resolved != expected {
		t.Errorf("resolved = %q, want %q", resolved, expected)
	}
}
