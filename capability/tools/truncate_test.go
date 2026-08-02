package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// ─── TruncateContent ───────────────────────────────────────────

// TC-001 (AC-1): TruncationResult contains content/wasTruncated/originalLength/truncatedLength.
func TestTruncateContent_ResultFields(t *testing.T) {
	content := "Hello, World!"
	res := TruncateContent(content, 5)

	if res.WasTruncated != true {
		t.Error("expected WasTruncated=true")
	}
	if res.Content == "" {
		t.Error("expected non-empty Content")
	}
	if res.OriginalLength != utf16Len(content) {
		t.Errorf("OriginalLength = %d, want %d", res.OriginalLength, utf16Len(content))
	}
	if res.TruncatedLength <= 0 || res.TruncatedLength > 5 {
		t.Errorf("TruncatedLength = %d, want 1..5", res.TruncatedLength)
	}
}

// TC-002 (AC-2): UTF-16 alignment with emoji — emoji is 4 bytes UTF-8, 2 UTF-16 code units.
// Truncating right before a high surrogate must back off so the pair stays intact.
func TestTruncateContent_EmojiAlignment(t *testing.T) {
	// 😀 = U+1F600: 4 bytes UTF-8, 2 UTF-16 code units (surrogate pair)
	content := "Hello😀World"
	// UTF-16 units: H e l l o 😀(2 units) W o r l d => 12 units
	// Truncate at 6: unit[5] is high surrogate → back off to 5 → "Hello"
	res := TruncateContent(content, 6)
	if !res.WasTruncated {
		t.Fatal("expected WasTruncated=true")
	}
	// The output before the marker must be valid UTF-8 (no broken surrogate).
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if !utf8.ValidString(body) {
		t.Errorf("truncated body is not valid UTF-8: %q", body)
	}
	if body != "Hello" {
		t.Errorf("body = %q, want %q", body, "Hello")
	}
}

// TC-003 (AC-2): Truncating exactly at the end of a surrogate pair keeps the emoji.
func TestTruncateContent_EmojiKeepPair(t *testing.T) {
	content := "Hello😀World"
	// Truncate at 7: unit[6] is low surrogate → no back-off → "Hello😀"
	res := TruncateContent(content, 7)
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if !utf8.ValidString(body) {
		t.Errorf("truncated body is not valid UTF-8: %q", body)
	}
	if body != "Hello😀" {
		t.Errorf("body = %q, want %q", body, "Hello😀")
	}
}

// TC-004 (AC-2): Chinese characters (BMP, 1 UTF-16 unit each) truncate cleanly.
func TestTruncateContent_Chinese(t *testing.T) {
	content := "你好世界测试" // 6 chars, 6 UTF-16 units
	res := TruncateContent(content, 3)
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if !utf8.ValidString(body) {
		t.Errorf("truncated body is not valid UTF-8: %q", body)
	}
	if body != "你好世" {
		t.Errorf("body = %q, want %q", body, "你好世")
	}
	if res.OriginalLength != 6 {
		t.Errorf("OriginalLength = %d, want 6", res.OriginalLength)
	}
	if res.TruncatedLength != 3 {
		t.Errorf("TruncatedLength = %d, want 3", res.TruncatedLength)
	}
}

// TC-005 (AC-2): Mixed content (ASCII + emoji + Chinese) truncates without broken chars.
func TestTruncateContent_Mixed(t *testing.T) {
	// "A😀中" → UTF-16: A(1) 😀(2) 中(1) = 4 units
	content := "A😀中"
	// Truncate at 2: unit[1] is low surrogate (part of 😀 pair) → no back-off
	// → keeps "A" + high surrogate... wait, unit[0]=A, unit[1]=high surrogate
	// Actually: A=0x0041, 😀 = [0xD83D, 0xDE00], 中=0x4E2D
	// units = [0x0041, 0xD83D, 0xDE00, 0x4E2D]
	// Truncate at 2: unit[1] = 0xD83D is high surrogate → back off to 1 → "A"
	res := TruncateContent(content, 2)
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if !utf8.ValidString(body) {
		t.Errorf("truncated body is not valid UTF-8: %q", body)
	}
	if body != "A" {
		t.Errorf("body = %q, want %q", body, "A")
	}
}

// TC-006 (AC-3): Content under limit returns as-is with WasTruncated=false.
func TestTruncateContent_UnderLimit(t *testing.T) {
	content := "short"
	res := TruncateContent(content, 100)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false")
	}
	if res.Content != content {
		t.Errorf("Content = %q, want %q", res.Content, content)
	}
	if res.OriginalLength != len(content) {
		t.Errorf("OriginalLength = %d, want %d", res.OriginalLength, len(content))
	}
	if res.TruncatedLength != res.OriginalLength {
		t.Errorf("TruncatedLength = %d, want %d", res.TruncatedLength, res.OriginalLength)
	}
}

// TC-007 (AC-3): Content exactly at limit returns as-is.
func TestTruncateContent_ExactLimit(t *testing.T) {
	content := "abcde"
	res := TruncateContent(content, 5)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false for exact fit")
	}
	if res.Content != content {
		t.Errorf("Content = %q, want %q", res.Content, content)
	}
}

// TC-008 (AC-4): Truncation marker "[truncated]" is appended at the end.
func TestTruncateContent_MarkerAppended(t *testing.T) {
	content := "This is a long string that needs truncation"
	res := TruncateContent(content, 10)
	if !strings.HasSuffix(res.Content, "[truncated]") {
		t.Errorf("expected Content to end with [truncated], got %q", res.Content)
	}
}

// TC-009 (AC-2): Multiple surrogate pairs — never split any pair.
func TestTruncateContent_MultipleEmoji(t *testing.T) {
	// "😀😁😂" — each emoji is 2 UTF-16 units → 6 units total
	content := "😀😁😂"
	for maxLen := 0; maxLen <= 6; maxLen++ {
		res := TruncateContent(content, maxLen)
		body := strings.TrimSuffix(res.Content, truncationMarker)
		if !utf8.ValidString(body) {
			t.Errorf("maxLen=%d: invalid UTF-8 in body %q", maxLen, body)
		}
	}
}

// ─── TruncateLines ─────────────────────────────────────────────

// TC-010 (AC-5): TruncateLines keeps the first maxLines.
func TestTruncateLines_Basic(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	res := TruncateLines(content, 3)
	if !res.WasTruncated {
		t.Fatal("expected WasTruncated=true")
	}
	body := strings.TrimSuffix(res.Content, truncationMarker)
	lines := strings.Split(body, "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "line1" || lines[1] != "line2" || lines[2] != "line3" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

// TC-011 (AC-5): TruncateLines with content under limit returns as-is.
func TestTruncateLines_UnderLimit(t *testing.T) {
	content := "line1\nline2"
	res := TruncateLines(content, 10)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false")
	}
	if res.Content != content {
		t.Errorf("Content = %q, want %q", res.Content, content)
	}
}

// TC-012 (AC-5): TruncateLines with exact line count returns as-is.
func TestTruncateLines_ExactLimit(t *testing.T) {
	content := "line1\nline2\nline3"
	res := TruncateLines(content, 3)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false for exact fit")
	}
}

// TC-013 (AC-5): TruncateLines appends marker when truncated.
func TestTruncateLines_MarkerAppended(t *testing.T) {
	content := "a\nb\nc\nd\ne"
	res := TruncateLines(content, 2)
	if !strings.HasSuffix(res.Content, "[truncated]") {
		t.Errorf("expected marker, got %q", res.Content)
	}
}

// TC-014 (AC-5): TruncateLines with empty content.
func TestTruncateLines_Empty(t *testing.T) {
	res := TruncateLines("", 5)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false for empty content")
	}
	if res.Content != "" {
		t.Errorf("Content = %q, want empty", res.Content)
	}
}

// TC-015 (AC-5): TruncateLines with single line.
func TestTruncateLines_SingleLine(t *testing.T) {
	content := "only line"
	res := TruncateLines(content, 1)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false")
	}
	if res.Content != content {
		t.Errorf("Content = %q, want %q", res.Content, content)
	}
}

// ─── TruncateBytes ─────────────────────────────────────────────

// TC-016 (AC-6): TruncateBytes doesn't cut mid-rune (emoji = 4 bytes).
func TestTruncateBytes_NoMidRune(t *testing.T) {
	// "Hello😀World": H(1) e(1) l(1) l(1) o(1) 😀(4) W(1) o(1) r(1) l(1) d(1) = 14 bytes
	content := "Hello😀World"
	// Truncate at 7 bytes: byte 5-8 is the emoji. Byte 7 is a continuation byte.
	// Should back off to byte 5 → "Hello"
	res := TruncateBytes(content, 7)
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if !utf8.ValidString(body) {
		t.Errorf("body is not valid UTF-8: %q", body)
	}
	if body != "Hello" {
		t.Errorf("body = %q, want %q", body, "Hello")
	}
}

// TC-017 (AC-6): TruncateBytes with ASCII content cuts at exact byte.
func TestTruncateBytes_ASCII(t *testing.T) {
	content := "HelloWorld"
	res := TruncateBytes(content, 5)
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if body != "Hello" {
		t.Errorf("body = %q, want %q", body, "Hello")
	}
}

// TC-018 (AC-6): TruncateBytes with content under limit returns as-is.
func TestTruncateBytes_UnderLimit(t *testing.T) {
	content := "short"
	res := TruncateBytes(content, 100)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false")
	}
	if res.Content != content {
		t.Errorf("Content = %q, want %q", res.Content, content)
	}
}

// TC-019 (AC-6): TruncateBytes with exact byte count returns as-is.
func TestTruncateBytes_ExactLimit(t *testing.T) {
	content := "abc"
	res := TruncateBytes(content, 3)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false")
	}
}

// TC-020 (AC-6): TruncateBytes appends marker when truncated.
func TestTruncateBytes_MarkerAppended(t *testing.T) {
	content := "abcdefghijklmnopqrstuvwxyz"
	res := TruncateBytes(content, 5)
	if !strings.HasSuffix(res.Content, "[truncated]") {
		t.Errorf("expected marker, got %q", res.Content)
	}
}

// TC-021 (AC-6): TruncateBytes with Chinese (3 bytes per char) doesn't cut mid-rune.
func TestTruncateBytes_Chinese(t *testing.T) {
	// 你(3) 好(3) 世(3) = 9 bytes
	content := "你好世"
	// Truncate at 4 bytes: byte 3 is start of 好, byte 4 is continuation.
	// Should back off to byte 3 → "你"
	res := TruncateBytes(content, 4)
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if !utf8.ValidString(body) {
		t.Errorf("body is not valid UTF-8: %q", body)
	}
	if body != "你" {
		t.Errorf("body = %q, want %q", body, "你")
	}
}

// TC-022 (AC-6): TruncateBytes at exact rune boundary keeps full rune.
func TestTruncateBytes_RuneBoundary(t *testing.T) {
	content := "你好世"
	// Truncate at 6 bytes = exactly 你好 (2 chars × 3 bytes)
	res := TruncateBytes(content, 6)
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if body != "你好" {
		t.Errorf("body = %q, want %q", body, "你好")
	}
}

// ─── Edge Cases ────────────────────────────────────────────────

// TC-023: TruncateContent with maxLength 0.
func TestTruncateContent_ZeroMaxLength(t *testing.T) {
	content := "abc"
	res := TruncateContent(content, 0)
	if !res.WasTruncated {
		t.Error("expected WasTruncated=true")
	}
	body := strings.TrimSuffix(res.Content, truncationMarker)
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

// TC-024: TruncateContent with empty content.
func TestTruncateContent_EmptyContent(t *testing.T) {
	res := TruncateContent("", 10)
	if res.WasTruncated {
		t.Error("expected WasTruncated=false")
	}
	if res.Content != "" {
		t.Errorf("Content = %q, want empty", res.Content)
	}
}

// TC-025: Verify TruncatedLength never exceeds maxLength (surrogate back-off).
func TestTruncateContent_TruncatedLengthWithinLimit(t *testing.T) {
	content := "😀😀😀" // 6 UTF-16 units
	for maxLen := 1; maxLen <= 5; maxLen++ {
		res := TruncateContent(content, maxLen)
		if res.WasTruncated && res.TruncatedLength > maxLen {
			t.Errorf("maxLen=%d: TruncatedLength=%d exceeds limit", maxLen, res.TruncatedLength)
		}
	}
}

// TC-026: OriginalLength is always in UTF-16 code units (not bytes or runes).
func TestTruncateContent_OriginalLengthUTF16(t *testing.T) {
	// 😀 is 1 rune, 4 bytes, 2 UTF-16 units
	content := "😀"
	res := TruncateContent(content, 10)
	if res.OriginalLength != 2 {
		t.Errorf("OriginalLength = %d, want 2 (UTF-16 units for one emoji)", res.OriginalLength)
	}
}

// TC-027: TruncateLines reports UTF-16 lengths consistently.
func TestTruncateLines_UTF16Lengths(t *testing.T) {
	content := "😀\n😀\n😀"
	res := TruncateLines(content, 1)
	// Original = 3 lines joined by \n: "😀\n😀\n😀" = 2+1+2+1+2 = 8 UTF-16 units
	if res.OriginalLength != 8 {
		t.Errorf("OriginalLength = %d, want 8", res.OriginalLength)
	}
	// Truncated = "😀" = 2 UTF-16 units
	if res.TruncatedLength != 2 {
		t.Errorf("TruncatedLength = %d, want 2", res.TruncatedLength)
	}
}

// TC-028: TruncateBytes reports UTF-16 lengths consistently.
func TestTruncateBytes_UTF16Lengths(t *testing.T) {
	content := "Hello😀" // 5 ASCII + 1 emoji = 7 UTF-16 units, 9 bytes
	res := TruncateBytes(content, 5)
	// Truncated to "Hello" = 5 UTF-16 units
	if res.OriginalLength != 7 {
		t.Errorf("OriginalLength = %d, want 7", res.OriginalLength)
	}
	if res.TruncatedLength != 5 {
		t.Errorf("TruncatedLength = %d, want 5", res.TruncatedLength)
	}
}
