package config

import "testing"

// MG-001: deepMergeMaps recursively merges nested maps.
func TestDeepMergeMaps_Nested(t *testing.T) {
	dst := map[string]any{
		"a": map[string]any{
			"x": 1,
			"y": 2,
		},
	}
	src := map[string]any{
		"a": map[string]any{
			"y": 3,
			"z": 4,
		},
	}

	result := deepMergeMaps(dst, src)

	a, ok := result["a"].(map[string]any)
	if !ok {
		t.Fatalf("result[a] is not map[string]any, got %T", result["a"])
	}
	if a["x"] != 1 {
		t.Errorf("result[a][x] = %v, want 1 (preserved from dst)", a["x"])
	}
	if a["y"] != 3 {
		t.Errorf("result[a][y] = %v, want 3 (overridden by src)", a["y"])
	}
	if a["z"] != 4 {
		t.Errorf("result[a][z] = %v, want 4 (added from src)", a["z"])
	}
}

// MG-002: deepMergeMaps with non-map values — src overrides dst.
func TestDeepMergeMaps_NonMapValues(t *testing.T) {
	dst := map[string]any{
		"key1": "value1",
		"key2": 42,
	}
	src := map[string]any{
		"key2": 999,
		"key3": "value3",
	}

	result := deepMergeMaps(dst, src)

	if result["key1"] != "value1" {
		t.Errorf("result[key1] = %v, want %q (preserved)", result["key1"], "value1")
	}
	if result["key2"] != 999 {
		t.Errorf("result[key2] = %v, want 999 (overridden)", result["key2"])
	}
	if result["key3"] != "value3" {
		t.Errorf("result[key3] = %v, want %q (added)", result["key3"], "value3")
	}
}

// MG-003: deepMergeMaps handles arbitrarily nested maps.
func TestDeepMergeMaps_DeeplyNested(t *testing.T) {
	dst := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"level3": map[string]any{
					"a": 1,
					"b": 2,
				},
			},
		},
	}
	src := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"level3": map[string]any{
					"b": 3,
					"c": 4,
				},
			},
		},
	}

	result := deepMergeMaps(dst, src)

	l1 := result["level1"].(map[string]any)
	l2 := l1["level2"].(map[string]any)
	l3 := l2["level3"].(map[string]any)

	if l3["a"] != 1 {
		t.Errorf("level3[a] = %v, want 1", l3["a"])
	}
	if l3["b"] != 3 {
		t.Errorf("level3[b] = %v, want 3", l3["b"])
	}
	if l3["c"] != 4 {
		t.Errorf("level3[c] = %v, want 4", l3["c"])
	}
}

// MG-004: mergeSettings deep-merges Extra nested maps (AC-3).
func TestMergeSettings_DeepExtraMerge(t *testing.T) {
	dst := Settings{
		Extra: map[string]any{
			"a": map[string]any{
				"x": 1,
				"y": 2,
			},
		},
	}
	src := Settings{
		Extra: map[string]any{
			"a": map[string]any{
				"y": 3,
				"z": 4,
			},
		},
	}

	result := mergeSettings(dst, src)

	a, ok := result.Extra["a"].(map[string]any)
	if !ok {
		t.Fatalf("Extra[a] is not map[string]any, got %T", result.Extra["a"])
	}
	if a["x"] != 1 {
		t.Errorf("Extra[a][x] = %v, want 1", a["x"])
	}
	if a["y"] != 3 {
		t.Errorf("Extra[a][y] = %v, want 3", a["y"])
	}
	if a["z"] != 4 {
		t.Errorf("Extra[a][z] = %v, want 4", a["z"])
	}
}

// MG-005: mergeSettings preserves non-zero scalar overrides.
func TestMergeSettings_ScalarOverrides(t *testing.T) {
	dst := Settings{
		Provider: "openai",
		Model: "gpt-4o",
		MaxTurns: 20,
	}
	src := Settings{
		Provider: "anthropic",
		MaxTurns: 30,
	}

	result := mergeSettings(dst, src)

	if result.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", result.Provider, "anthropic")
	}
	if result.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q (preserved)", result.Model, "gpt-4o")
	}
	if result.MaxTurns != 30 {
		t.Errorf("MaxTurns = %d, want 30", result.MaxTurns)
	}
}

// MG-006: deepMergeMaps does not mutate the original src map.
func TestDeepMergeMaps_DoesNotMutateSrc(t *testing.T) {
	src := map[string]any{
		"a": map[string]any{"x": 1},
	}
	dst := map[string]any{
		"a": map[string]any{"y": 2},
	}

	_ = deepMergeMaps(dst, src)

	srcA := src["a"].(map[string]any)
	if _, exists := srcA["y"]; exists {
		t.Error("deepMergeMaps mutated the src map")
	}
}
