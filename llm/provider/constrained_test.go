package provider

import (
	"testing"
)

// AC-2: Constrained sampling json_schema mode.
func TestConstrainedMode_Constants(t *testing.T) {
	if ConstrainedJSONSchema != "json_schema" {
		t.Errorf("ConstrainedJSONSchema = %q, want %q", ConstrainedJSONSchema, "json_schema")
	}
	if ConstrainedGrammar != "grammar" {
		t.Errorf("ConstrainedGrammar = %q, want %q", ConstrainedGrammar, "grammar")
	}
}

// AC-2: ResponseFormat struct holds mode and schema.
func TestResponseFormat_Struct(t *testing.T) {
	rf := ResponseFormat{
		Type: ConstrainedJSONSchema,
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}

	if rf.Type != ConstrainedJSONSchema {
		t.Errorf("Type = %q, want %q", rf.Type, ConstrainedJSONSchema)
	}
	if rf.JSONSchema == nil {
		t.Fatal("JSONSchema should not be nil")
	}
	schema, ok := rf.JSONSchema["type"]
	if !ok || schema != "object" {
		t.Errorf("JSONSchema type = %v, want object", schema)
	}
}

// AC-2: ChatOptions can hold ResponseFormat.
func TestChatOptions_ResponseFormat(t *testing.T) {
	opts := &ChatOptions{
		ResponseFormat: &ResponseFormat{
			Type: ConstrainedJSONSchema,
			JSONSchema: map[string]any{
				"type": "object",
			},
		},
	}

	if opts.ResponseFormat == nil {
		t.Fatal("ResponseFormat should not be nil")
	}
	if opts.ResponseFormat.Type != ConstrainedJSONSchema {
		t.Errorf("Type = %q, want %q", opts.ResponseFormat.Type, ConstrainedJSONSchema)
	}
}

// AC-2: ChatOptions without ResponseFormat works.
func TestChatOptions_NoResponseFormat(t *testing.T) {
	opts := &ChatOptions{}

	if opts.ResponseFormat != nil {
		t.Error("ResponseFormat should be nil by default")
	}
}

// AC-2: ResponseFormat with grammar mode.
func TestResponseFormat_GrammarMode(t *testing.T) {
	rf := ResponseFormat{
		Type: ConstrainedGrammar,
		JSONSchema: nil, // grammar mode may not use JSON schema
	}

	if rf.Type != ConstrainedGrammar {
		t.Errorf("Type = %q, want %q", rf.Type, ConstrainedGrammar)
	}
}

// AC-2: IsConstrained helper returns correct value.
func TestResponseFormat_IsConstrained(t *testing.T) {
	rf := &ResponseFormat{Type: ConstrainedJSONSchema}
	if !rf.IsConstrained() {
		t.Error("json_schema mode should be constrained")
	}

	rf2 := &ResponseFormat{Type: ConstrainedGrammar}
	if !rf2.IsConstrained() {
		t.Error("grammar mode should be constrained")
	}

	var rf3 *ResponseFormat
	if rf3.IsConstrained() {
		t.Error("nil ResponseFormat should not be constrained")
	}
}
