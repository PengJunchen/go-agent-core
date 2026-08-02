package provider

// ConstrainedMode defines the type of output constraint applied to LLM responses.
type ConstrainedMode string

const (
	// ConstrainedJSONSchema forces the LLM to produce output conforming to
	// a JSON Schema. The provider passes the schema to the LLM API.
	ConstrainedJSONSchema ConstrainedMode = "json_schema"
	// ConstrainedGrammar forces the LLM to produce output conforming to
	// a grammar (e.g., GBNF for llama.cpp backends).
	ConstrainedGrammar ConstrainedMode = "grammar"
)

// ResponseFormat specifies the desired output format constraint for an LLM call.
//
// When Type is ConstrainedJSONSchema, JSONSchema must contain a valid
// JSON Schema object. When Type is ConstrainedGrammar, JSONSchema may be
// nil (grammar is passed via provider-specific mechanisms).
type ResponseFormat struct {
	Type ConstrainedMode
	JSONSchema map[string]any // JSON Schema object for json_schema mode
}

// IsConstrained returns true if the ResponseFormat represents a valid
// constrained output request. Returns false for nil receiver.
func (rf *ResponseFormat) IsConstrained() bool {
	if rf == nil {
		return false
	}
	return rf.Type == ConstrainedJSONSchema || rf.Type == ConstrainedGrammar
}
