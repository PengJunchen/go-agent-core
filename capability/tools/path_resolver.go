package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// PathResolver resolves and sanitizes file paths with Unicode NFC normalization,
// tilde expansion, sandbox security checks, and macOS HFS+ NFD fallback.
type PathResolver struct {
	workDir string
}

// NewPathResolver creates a PathResolver scoped to workDir.
func NewPathResolver(workDir string) *PathResolver {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		absWorkDir = workDir
	}
	return &PathResolver{workDir: filepath.Clean(absWorkDir)}
}

// Resolve normalizes and resolves the given path through the following steps:
// - Unicode NFC normalization (AC-2)
// - Tilde expansion: ~ → home directory (AC-4)
// - Relative paths are joined with workDir (AC-6)
// - Absolute paths are used as-is
// - Security sandbox check: reject paths escaping workDir (AC-5)
// - macOS HFS+ NFD→NFC fallback (AC-3)
func (r *PathResolver) Resolve(path string) (string, error) {
	// AC-2: Unicode NFC normalization.
	path = norm.NFC.String(path)

	// AC-4: Tilde expansion.
	expanded, err := expandTilde(path)
	if err != nil {
		return "", err
	}
	path = expanded

	// AC-6: Relative paths are joined with workDir.
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.workDir, path)
	}
	path = filepath.Clean(path)

	// AC-5: Security sandbox check.
	if err := r.checkSandbox(path); err != nil {
		return "", err
	}

	// AC-3: macOS HFS+ NFD→NFC fallback.
	// If the file doesn't exist at the NFC path, try NFD normalization
	// (macOS HFS+ stores filenames in NFD form).
	path = r.resolveNFCNFD(path)

	return path, nil
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) (string, error) {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to expand tilde: %w", err)
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to expand tilde: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// checkSandbox ensures the resolved path does not escape the workDir.
func (r *PathResolver) checkSandbox(resolved string) error {
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}
	absResolved = filepath.Clean(absResolved)

	// The resolved path must be within workDir (or equal to it).
	if absResolved == r.workDir {
		return nil
	}
	if !strings.HasPrefix(absResolved, r.workDir+string(filepath.Separator)) {
		return fmt.Errorf("path escapes allowed directory: %s", resolved)
	}
	return nil
}

// resolveNFCNFD handles the macOS HFS+ normalization mismatch.
// If the NFC path doesn't exist, it tries the NFD form and vice versa.
// As a last resort, it scans the parent directory for a matching entry
// by comparing NFC-normalized basenames.
func (r *PathResolver) resolveNFCNFD(path string) string {
	nfcPath := norm.NFC.String(path)
	nfdPath := norm.NFD.String(path)

	// If NFC and NFD are identical (no combining characters), no fallback needed.
	if nfcPath == nfdPath {
		return nfcPath
	}

	_, nfcErr := os.Stat(nfcPath)
	if nfcErr == nil {
		return nfcPath
	}

	_, nfdErr := os.Stat(nfdPath)
	if nfdErr == nil {
		return nfdPath
	}

	// Fallback: scan parent directory for a file whose NFC-normalized
	// basename matches the expected NFC basename.
	nfcBase := norm.NFC.String(filepath.Base(nfcPath))
	parent := filepath.Dir(nfcPath)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nfcPath
	}
	for _, entry := range entries {
		if norm.NFC.String(entry.Name()) == nfcBase {
			return filepath.Join(parent, entry.Name())
		}
	}

	// Neither exists; return the NFC form (canonical) for consistency.
	return nfcPath
}
