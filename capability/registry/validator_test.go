package registry

import (
	"encoding/json"
	"testing"
)

// VL-001: 缺少必需属性校验失败。
func TestValidator_RequiredPropertyMissing(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["name"],
		"properties": {
			"name": {"type": "string"}
		}
	}`)

	err := v.Validate(map[string]any{}, schema)
	if err == nil {
		t.Error("expected error for missing required property")
	}
}

// VL-002: 必需属性存在校验通过。
func TestValidator_RequiredPropertyPresent(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["name"],
		"properties": {
			"name": {"type": "string"}
		}
	}`)

	err := v.Validate(map[string]any{"name": "test"}, schema)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// VL-003: 类型不匹配校验失败。
func TestValidator_TypeMismatch(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"count": {"type": "number"}
		}
	}`)

	err := v.Validate(map[string]any{"count": "not-a-number"}, schema)
	if err == nil {
		t.Error("expected error for type mismatch")
	}
}

// VL-004: 枚举值校验。
func TestValidator_EnumValidation(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"color": {"type": "string", "enum": ["red", "green", "blue"]}
		}
	}`)

	// 有效值
	err := v.Validate(map[string]any{"color": "red"}, schema)
	if err != nil {
		t.Errorf("valid enum value: unexpected error: %v", err)
	}

	// 无效值
	err = v.Validate(map[string]any{"color": "yellow"}, schema)
	if err == nil {
		t.Error("expected error for enum value not in list")
	}
}

// VL-005: 数值范围校验。
func TestValidator_NumberRange(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"age": {"type": "number", "minimum": 0, "maximum": 150}
		}
	}`)

	// 有效值
	err := v.Validate(map[string]any{"age": float64(25)}, schema)
	if err != nil {
		t.Errorf("valid number: unexpected error: %v", err)
	}

	// 低于最小值
	err = v.Validate(map[string]any{"age": float64(-1)}, schema)
	if err == nil {
		t.Error("expected error for value below minimum")
	}

	// 超过最大值
	err = v.Validate(map[string]any{"age": float64(200)}, schema)
	if err == nil {
		t.Error("expected error for value above maximum")
	}
}

// VL-006: 字符串长度校验。
func TestValidator_StringLength(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"code": {"type": "string", "minLength": 3, "maxLength": 10}
		}
	}`)

	// 有效长度
	err := v.Validate(map[string]any{"code": "abc"}, schema)
	if err != nil {
		t.Errorf("valid string: unexpected error: %v", err)
	}

	// 太短
	err = v.Validate(map[string]any{"code": "ab"}, schema)
	if err == nil {
		t.Error("expected error for string shorter than minLength")
	}

	// 太长
	err = v.Validate(map[string]any{"code": "abcdefghijk"}, schema)
	if err == nil {
		t.Error("expected error for string longer than maxLength")
	}
}

// VL-007: strict 模式拒绝未知属性。
func TestValidator_UnknownPropertiesStrict(t *testing.T) {
	v := NewToolParameterValidator(true)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		}
	}`)

	err := v.Validate(map[string]any{"name": "test", "extra": "value"}, schema)
	if err == nil {
		t.Error("strict mode should reject unknown properties")
	}
}

// VL-008: lenient 模式允许未知属性。
func TestValidator_UnknownPropertiesLenient(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		}
	}`)

	err := v.Validate(map[string]any{"name": "test", "extra": "value"}, schema)
	if err != nil {
		t.Errorf("lenient mode should allow unknown properties, got: %v", err)
	}
}

// VL-009: 嵌套对象属性校验。
func TestValidator_NestedObject(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"address": {
				"type": "object",
				"required": ["city"],
				"properties": {
					"city": {"type": "string"},
					"zip": {"type": "string"}
				}
			}
		}
	}`)

	// 有效嵌套
	err := v.Validate(map[string]any{
		"address": map[string]any{
			"city": "Beijing",
			"zip": "100000",
		},
	}, schema)
	if err != nil {
		t.Errorf("valid nested object: unexpected error: %v", err)
	}

	// 嵌套缺少必需属性
	err = v.Validate(map[string]any{
		"address": map[string]any{
			"zip": "100000",
		},
	}, schema)
	if err == nil {
		t.Error("expected error for missing required property in nested object")
	}

	// 嵌套类型错误
	err = v.Validate(map[string]any{
		"address": map[string]any{
			"city": 123,
		},
	}, schema)
	if err == nil {
		t.Error("expected error for type mismatch in nested object")
	}
}

// VL-010: 数组元素类型校验。
func TestValidator_ArrayItems(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"tags": {
				"type": "array",
				"items": {"type": "string"}
			}
		}
	}`)

	// 有效数组
	err := v.Validate(map[string]any{
		"tags": []any{"a", "b", "c"},
	}, schema)
	if err != nil {
		t.Errorf("valid array: unexpected error: %v", err)
	}

	// 数组元素类型错误
	err = v.Validate(map[string]any{
		"tags": []any{"a", 123, "c"},
	}, schema)
	if err == nil {
		t.Error("expected error for array item type mismatch")
	}
}

// VL-011: 空 schema 始终通过。
func TestValidator_EmptySchema(t *testing.T) {
	v := NewToolParameterValidator(false)

	err := v.Validate(map[string]any{"anything": "goes"}, nil)
	if err != nil {
		t.Errorf("empty schema should always pass, got: %v", err)
	}

	err = v.Validate(map[string]any{"anything": "goes"}, json.RawMessage{})
	if err != nil {
		t.Errorf("empty raw schema should always pass, got: %v", err)
	}
}

// VL-012: 复杂 schema 综合校验。
func TestValidator_ComplexSchema(t *testing.T) {
	v := NewToolParameterValidator(true)
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["query", "limit"],
		"properties": {
			"query": {
				"type": "string",
				"minLength": 1,
				"maxLength": 500
			},
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 100
			},
			"sort": {
				"type": "string",
				"enum": ["asc", "desc"]
			},
			"filters": {
				"type": "array",
				"items": {
					"type": "object",
					"required": ["field"],
					"properties": {
						"field": {"type": "string"},
						"value": {"type": "string"}
					}
				}
			}
		}
	}`)

	// 完全有效
	err := v.Validate(map[string]any{
		"query": "test",
		"limit": float64(10),
		"sort": "asc",
		"filters": []any{
			map[string]any{"field": "status", "value": "active"},
		},
	}, schema)
	if err != nil {
		t.Errorf("valid complex object: unexpected error: %v", err)
	}

	// 缺少必需字段
	err = v.Validate(map[string]any{
		"query": "test",
	}, schema)
	if err == nil {
		t.Error("expected error for missing required fields")
	}

	// query 太短
	err = v.Validate(map[string]any{
		"query": "",
		"limit": float64(10),
	}, schema)
	if err == nil {
		t.Error("expected error for query too short")
	}

	// limit 超范围
	err = v.Validate(map[string]any{
		"query": "test",
		"limit": float64(200),
	}, schema)
	if err == nil {
		t.Error("expected error for limit above maximum")
	}

	// sort 不在枚举
	err = v.Validate(map[string]any{
		"query": "test",
		"limit": float64(10),
		"sort": "invalid",
	}, schema)
	if err == nil {
		t.Error("expected error for sort not in enum")
	}

	// 未知属性（strict 模式）
	err = v.Validate(map[string]any{
		"query": "test",
		"limit": float64(10),
		"unknownP": "x",
	}, schema)
	if err == nil {
		t.Error("expected error for unknown property in strict mode")
	}

	// 数组元素缺少必需属性
	err = v.Validate(map[string]any{
		"query": "test",
		"limit": float64(10),
		"filters": []any{
			map[string]any{"value": "active"},
		},
	}, schema)
	if err == nil {
		t.Error("expected error for array item missing required field")
	}
}

// VL-013: ValidateRequired 单独检查。
func TestValidator_ValidateRequired(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["name", "age"],
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "number"}
		}
	}`)

	// 缺少 age
	err := v.ValidateRequired(map[string]any{"name": "test"}, schema)
	if err == nil {
		t.Error("expected error for missing required property")
	}

	// 全部存在
	err = v.ValidateRequired(map[string]any{"name": "test", "age": float64(25)}, schema)
	if err != nil {
		t.Errorf("all required present: unexpected error: %v", err)
	}
}

// VL-014: boolean 类型校验。
func TestValidator_BooleanType(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"enabled": {"type": "boolean"}
		}
	}`)

	err := v.Validate(map[string]any{"enabled": true}, schema)
	if err != nil {
		t.Errorf("valid boolean: unexpected error: %v", err)
	}

	err = v.Validate(map[string]any{"enabled": "yes"}, schema)
	if err == nil {
		t.Error("expected error for boolean type mismatch")
	}
}

// VL-015: integer 类型校验。
func TestValidator_IntegerType(t *testing.T) {
	v := NewToolParameterValidator(false)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"count": {"type": "integer"}
		}
	}`)

	// 整数值
	err := v.Validate(map[string]any{"count": float64(42)}, schema)
	if err != nil {
		t.Errorf("valid integer: unexpected error: %v", err)
	}

	// 非整数值
	err = v.Validate(map[string]any{"count": float64(3.14)}, schema)
	if err == nil {
		t.Error("expected error for non-integer float as integer type")
	}

	// 字符串
	err = v.Validate(map[string]any{"count": "42"}, schema)
	if err == nil {
		t.Error("expected error for string as integer type")
	}
}
