package config

import "testing"

// EV-001: parseInt parses valid numeric strings.
func TestParseInt_Valid(t *testing.T) {
	var n int
	got, err := parseInt("42", &n)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Errorf("parseInt returned %d, want 42", got)
	}
	if n != 42 {
		t.Errorf("out = %d, want 42", n)
	}
}

// EV-002: parseInt rejects non-numeric strings.
func TestParseInt_Invalid(t *testing.T) {
	var n int
	_, err := parseInt("12abc", &n)
	if err == nil {
		t.Error("expected error for non-numeric string")
	}
}

// EV-003: parseInt handles zero.
func TestParseInt_Zero(t *testing.T) {
	var n int
	got, err := parseInt("0", &n)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Errorf("parseInt returned %d, want 0", got)
	}
}

// EV-004: interpolateEnv replaces environment variables.
func TestInterpolateEnv(t *testing.T) {
	t.Setenv("TEST_INTERP_VAR", "hello-world")
	result := interpolateEnv("$TEST_INTERP_VAR")
	if result != "hello-world" {
		t.Errorf("interpolateEnv = %q, want %q", result, "hello-world")
	}
}

// EV-005: interpolateEnv with ${} syntax.
func TestInterpolateEnv_BraceSyntax(t *testing.T) {
	t.Setenv("TEST_BRACE_VAR", "braced")
	result := interpolateEnv("${TEST_BRACE_VAR}")
	if result != "braced" {
		t.Errorf("interpolateEnv = %q, want %q", result, "braced")
	}
}

// EV-006: interpolateEnv leaves undefined vars unchanged.
func TestInterpolateEnv_Undefined(t *testing.T) {
	result := interpolateEnv("$UNDEFINED_VAR_XYZ_12345")
	if result != "" {
		t.Errorf("interpolateEnv for undefined var = %q, want empty", result)
	}
}
