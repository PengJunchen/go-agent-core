package tools

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const truncationMarker = "\n[truncated]"

// TruncationResult holds the result of a content truncation operation.
type TruncationResult struct {
	Content string // Truncated (or original) content
	WasTruncated bool // Whether truncation occurred
	OriginalLength int // Original length in UTF-16 code units
	TruncatedLength int // Result length in UTF-16 code units (before marker)
}

// isHighSurrogate reports whether u is a UTF-16 high surrogate (0xD800–0xDBFF).
func isHighSurrogate(u uint16) bool {
	return u >= 0xD800 && u < 0xDC00
}

// utf16Len returns the length of s in UTF-16 code units.
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// TruncateContent truncates content to at most maxLength UTF-16 code units.
// It never cuts in the middle of a multi-byte character (surrogate pair).
// When truncated, a "\n[truncated]" marker is appended.
func TruncateContent(content string, maxLength int) TruncationResult {
	units := utf16.Encode([]rune(content))
	originalLength := len(units)

	if originalLength <= maxLength {
		return TruncationResult{
			Content: content,
			WasTruncated: false,
			OriginalLength: originalLength,
			TruncatedLength: originalLength,
		}
	}

	cut := maxLength
	// Avoid splitting a surrogate pair: if the unit just before the cut
	// is a high surrogate, back off one code unit so the pair stays intact.
	if cut > 0 && isHighSurrogate(units[cut-1]) {
		cut--
	}

	truncatedRunes := utf16.Decode(units[:cut])
	result := string(truncatedRunes) + truncationMarker

	return TruncationResult{
		Content: result,
		WasTruncated: true,
		OriginalLength: originalLength,
		TruncatedLength: cut,
	}
}

// TruncateLines truncates content to at most maxLines lines (separated by \n).
// When truncated, a "\n[truncated]" marker is appended after the last kept line.
func TruncateLines(content string, maxLines int) TruncationResult {
	originalLength := utf16Len(content)

	if content == "" {
		return TruncationResult{
			Content: "",
			WasTruncated: false,
			OriginalLength: 0,
			TruncatedLength: 0,
		}
	}

	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return TruncationResult{
			Content: content,
			WasTruncated: false,
			OriginalLength: originalLength,
			TruncatedLength: originalLength,
		}
	}

	kept := strings.Join(lines[:maxLines], "\n")
	result := kept + truncationMarker

	return TruncationResult{
		Content: result,
		WasTruncated: true,
		OriginalLength: originalLength,
		TruncatedLength: utf16Len(kept),
	}
}

// TruncateBytes truncates content to at most maxBytes bytes, without cutting
// in the middle of a UTF-8 rune. When truncated, a "\n[truncated]" marker is
// appended after the last complete rune.
func TruncateBytes(content string, maxBytes int) TruncationResult {
	originalLength := utf16Len(content)

	if len(content) <= maxBytes {
		return TruncationResult{
			Content: content,
			WasTruncated: false,
			OriginalLength: originalLength,
			TruncatedLength: originalLength,
		}
	}

	// Find the last valid rune boundary at or before maxBytes.
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}

	kept := content[:cut]
	result := kept + truncationMarker

	return TruncationResult{
		Content: result,
		WasTruncated: true,
		OriginalLength: originalLength,
		TruncatedLength: utf16Len(kept),
	}
}
