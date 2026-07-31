package registry

import (
	"encoding/json"
	"fmt"
)

// ToolParameterValidator 根据工具的参数 schema 校验调用参数。
type ToolParameterValidator struct {
	strict bool // true 时拒绝未知属性
}

// NewToolParameterValidator 创建一个新的校验器。
func NewToolParameterValidator(strict bool) *ToolParameterValidator {
	return &ToolParameterValidator{strict: strict}
}

// Validate 校验参数是否符合工具的参数 schema。
// 返回 nil 表示通过，否则返回描述问题的错误。
func (v *ToolParameterValidator) Validate(args map[string]any, schema json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}

	var s map[string]any
	if err := json.Unmarshal(schema, &s); err != nil {
		return fmt.Errorf("invalid schema: %w", err)
	}

	return v.validateObject(args, s, "")
}

// ValidateRequired 检查所有必需属性是否存在。
func (v *ToolParameterValidator) ValidateRequired(args map[string]any, schema json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}

	var s map[string]any
	if err := json.Unmarshal(schema, &s); err != nil {
		return fmt.Errorf("invalid schema: %w", err)
	}

	return v.checkRequired(args, s, "")
}

// validateObject 校验一个对象值。
func (v *ToolParameterValidator) validateObject(obj map[string]any, schema map[string]any, path string) error {
	if typ, _ := schema["type"].(string); typ != "" && typ != "object" {
		return fmt.Errorf("%s: expected type object, got %s", pathPrefix(path), typ)
	}

	// 检查必需属性
	if err := v.checkRequired(obj, schema, path); err != nil {
		return err
	}

	// 检查未知属性（strict 模式）
	if v.strict {
		props, _ := schema["properties"].(map[string]any)
		if props != nil {
			for key := range obj {
				if _, ok := props[key]; !ok {
					p := joinPath(path, key)
					return fmt.Errorf("%s: unknown property", p)
				}
			}
		}
	}

	// 校验各属性
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return nil
	}

	for key, propSchema := range props {
		val, exists := obj[key]
		if !exists {
			continue
		}
		propSchemaMap, ok := propSchema.(map[string]any)
		if !ok {
			continue
		}
		p := joinPath(path, key)
		if err := v.validateValue(val, propSchemaMap, p); err != nil {
			return err
		}
	}

	return nil
}

// checkRequired 检查必需属性。
func (v *ToolParameterValidator) checkRequired(obj map[string]any, schema map[string]any, path string) error {
	required, _ := schema["required"].([]any)
	for _, r := range required {
		name, ok := r.(string)
		if !ok {
			continue
		}
		if _, exists := obj[name]; !exists {
			p := joinPath(path, name)
			return fmt.Errorf("%s: required property missing", p)
		}
	}
	return nil
}

// validateValue 校验任意值。
func (v *ToolParameterValidator) validateValue(val any, schema map[string]any, path string) error {
	// 类型检查
	if typ, ok := schema["type"].(string); ok {
		if err := v.checkType(val, typ, path); err != nil {
			return err
		}
	}

	// 枚举检查
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if fmt.Sprintf("%v", val) == fmt.Sprintf("%v", e) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: value not in enum %v", pathPrefix(path), enum)
		}
	}

	// 根据类型做进一步校验
	switch typ, _ := schema["type"].(string); typ {
	case "string":
		return v.validateString(val, schema, path)
	case "number":
		return v.validateNumber(val, schema, path)
	case "integer":
		return v.validateNumber(val, schema, path)
	case "array":
		return v.validateArray(val, schema, path)
	case "object":
		return v.validateNestedObject(val, schema, path)
	}

	return nil
}

// checkType 检查值的类型是否匹配。
func (v *ToolParameterValidator) checkType(val any, expected string, path string) error {
	switch expected {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("%s: expected string, got %T", pathPrefix(path), val)
		}
	case "number":
		if !isNumber(val) {
			return fmt.Errorf("%s: expected number, got %T", pathPrefix(path), val)
		}
	case "integer":
		if !isInteger(val) {
			return fmt.Errorf("%s: expected integer, got %T", pathPrefix(path), val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("%s: expected boolean, got %T", pathPrefix(path), val)
		}
	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Errorf("%s: expected array, got %T", pathPrefix(path), val)
		}
	case "object":
		if _, ok := val.(map[string]any); !ok {
			return fmt.Errorf("%s: expected object, got %T", pathPrefix(path), val)
		}
	}
	return nil
}

// validateString 校验字符串值。
func (v *ToolParameterValidator) validateString(val any, schema map[string]any, path string) error {
	s, ok := val.(string)
	if !ok {
		return nil // 类型不匹配已由 checkType 处理
	}

	if minLen, ok := toFloat(schema["minLength"]); ok && float64(len(s)) < minLen {
		return fmt.Errorf("%s: string length %d less than minLength %.0f", pathPrefix(path), len(s), minLen)
	}
	if maxLen, ok := toFloat(schema["maxLength"]); ok && float64(len(s)) > maxLen {
		return fmt.Errorf("%s: string length %d greater than maxLength %.0f", pathPrefix(path), len(s), maxLen)
	}
	return nil
}

// validateNumber 校验数值。
func (v *ToolParameterValidator) validateNumber(val any, schema map[string]any, path string) error {
	n, ok := toFloat(val)
	if !ok {
		return nil
	}

	if min, ok := toFloat(schema["minimum"]); ok && n < min {
		return fmt.Errorf("%s: value %v less than minimum %v", pathPrefix(path), n, min)
	}
	if max, ok := toFloat(schema["maximum"]); ok && n > max {
		return fmt.Errorf("%s: value %v greater than maximum %v", pathPrefix(path), n, max)
	}
	return nil
}

// validateArray 校验数组值。
func (v *ToolParameterValidator) validateArray(val any, schema map[string]any, path string) error {
	arr, ok := val.([]any)
	if !ok {
		return nil
	}

	itemsSchema, _ := schema["items"].(map[string]any)
	if itemsSchema == nil {
		return nil
	}

	for i, item := range arr {
		p := fmt.Sprintf("%s[%d]", pathPrefix(path), i)
		if err := v.validateValue(item, itemsSchema, p); err != nil {
			return err
		}
	}
	return nil
}

// validateNestedObject 校验嵌套对象。
func (v *ToolParameterValidator) validateNestedObject(val any, schema map[string]any, path string) error {
	obj, ok := val.(map[string]any)
	if !ok {
		return nil
	}

	// 检查必需属性
	if err := v.checkRequired(obj, schema, path); err != nil {
		return err
	}

	// 检查未知属性（strict 模式）
	if v.strict {
		props, _ := schema["properties"].(map[string]any)
		if props != nil {
			for key := range obj {
				if _, ok := props[key]; !ok {
					p := joinPath(path, key)
					return fmt.Errorf("%s: unknown property", p)
				}
			}
		}
	}

	// 校验各属性
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return nil
	}

	for key, propSchema := range props {
		propVal, exists := obj[key]
		if !exists {
			continue
		}
		propSchemaMap, ok := propSchema.(map[string]any)
		if !ok {
			continue
		}
		p := joinPath(path, key)
		if err := v.validateValue(propVal, propSchemaMap, p); err != nil {
			return err
		}
	}

	return nil
}

// --- 辅助函数 ---

func isNumber(val any) bool {
	switch val.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	}
	return false
}

func isInteger(val any) bool {
	switch val.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	// JSON 反序列化后 number 默认为 float64，检查是否为整数值
	if f, ok := val.(float64); ok {
		return f == float64(int64(f))
	}
	return false
}

func toFloat(val any) (float64, bool) {
	switch v := val.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

func pathPrefix(path string) string {
	if path == "" {
		return "root"
	}
	return path
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}
