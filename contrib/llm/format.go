package llm

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// ---- 结构化输出 Format 抽象 ----
//
// Format/Unmarshal 函数对:统一不同 provider 的结构化输出(json_schema)支持。
// Format 从 Go 类型生成 ResponseFormat;Unmarshal 从 Response 反序列化到目标类型。
// Provider 可提供自己的实现以处理特殊 wire format;默认实现基于 json.Marshal/Unmarshal。

// FormatFunc 从 Go 类型的 reflect.Type 生成 ResponseFormat(含 JSON Schema)。
type FormatFunc func(t reflect.Type) (*ResponseFormat, error)

// UnmarshalFunc 从模型响应反序列化到 Go 值。
type UnmarshalFunc func(resp *Response, target any) error

// TypedOutput 封装类型安全的结构化输出配置。
// 使用泛型构造,携带 Format 和 Unmarshal 函数。
type TypedOutput[T any] struct {
	format    *ResponseFormat
	unmarshal UnmarshalFunc
}

// NewTypedOutput 为类型 T 创建结构化输出配置。
// 自动生成 JSON Schema 并注册反序列化函数。
func NewTypedOutput[T any](name string, opts ...SchemaOption) (*TypedOutput[T], error) {
	cfg := &schemaConfig{strict: true}
	for _, o := range opts {
		o(cfg)
	}
	var zero T
	schema, err := generateSchema(reflect.TypeOf(zero))
	if err != nil {
		return nil, fmt.Errorf("llm: generate schema for %T: %w", zero, err)
	}
	rf := &ResponseFormat{
		Type: "json_schema",
		JSONSchema: &JSONSchema{
			Name:   name,
			Schema: schema,
			Strict: cfg.strict,
		},
	}
	return &TypedOutput[T]{
		format: rf,
		unmarshal: func(resp *Response, target any) error {
			return json.Unmarshal([]byte(resp.Content), target)
		},
	}, nil
}

// Format 返回给请求使用的 ResponseFormat。
func (t *TypedOutput[T]) Format() *ResponseFormat { return t.format }

// Unmarshal 从 Response 反序列化为 T。
func (t *TypedOutput[T]) Unmarshal(resp *Response) (T, error) {
	var result T
	if err := t.unmarshal(resp, &result); err != nil {
		return result, fmt.Errorf("llm: unmarshal typed output: %w", err)
	}
	return result, nil
}

// ApplyTo 把结构化输出配置应用到请求上。
func (t *TypedOutput[T]) ApplyTo(req *Request) {
	req.ResponseFormat = t.format
}

// SchemaOption 配置 schema 生成。
type SchemaOption func(*schemaConfig)

type schemaConfig struct {
	strict bool
}

// WithStrict 控制是否启用 strict 模式(默认 true)。
func WithStrict(strict bool) SchemaOption {
	return func(c *schemaConfig) { c.strict = strict }
}

// generateSchema 从 Go 类型生成 JSON Schema(简化版,覆盖常用类型)。
func generateSchema(t reflect.Type) (json.RawMessage, error) {
	schema := buildSchema(t)
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func buildSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		return map[string]any{
			"type":  "array",
			"items": buildSchema(t.Elem()),
		}
	case reflect.Map:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": buildSchema(t.Elem()),
		}
	case reflect.Struct:
		properties := map[string]any{}
		var required []string
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := f.Tag.Get("json")
			if name == "-" {
				continue
			}
			omitempty := false
			if idx := len(name) - 1; idx >= 0 {
				parts := splitJSONTag(name)
				name = parts[0]
				for _, p := range parts[1:] {
					if p == "omitempty" {
						omitempty = true
					}
				}
			}
			if name == "" {
				name = f.Name
			}
			properties[name] = buildSchema(f.Type)
			if !omitempty {
				required = append(required, name)
			}
		}
		schema := map[string]any{
			"type":                 "object",
			"properties":           properties,
			"additionalProperties": false,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	default:
		return map[string]any{"type": "string"}
	}
}

func splitJSONTag(tag string) []string {
	var parts []string
	start := 0
	for i := range len(tag) {
		if tag[i] == ',' {
			parts = append(parts, tag[start:i])
			start = i + 1
		}
	}
	parts = append(parts, tag[start:])
	return parts
}
