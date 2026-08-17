package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/rushteam/beauty/contrib/llm"
)

// ToolOption 配置 TypedFunc 构造的 Tool。
type ToolOption func(*typedToolConfig)

type typedToolConfig struct {
	permission Permission
}

// WithToolPermission 设置工具权限(默认 PermitAllow)。
func WithToolPermission(p Permission) ToolOption {
	return func(c *typedToolConfig) {
		c.permission = p
	}
}

// TypedFunc 从类型安全的 handler 构造 Tool。
// In: 输入 struct 类型 → 自动生成 JSON Schema 作为 ToolDef.Parameters
// Out: 输出类型 → 自动序列化为 JSON 字符串结果(Out 为 string 时直接返回)
//
// 示例:
//
//	type WeatherInput struct {
//	    City string `json:"city"`
//	}
//	type WeatherOutput struct {
//	    Temp int    `json:"temp"`
//	    Cond string `json:"cond"`
//	}
//	tool, err := agent.TypedFunc[WeatherInput, WeatherOutput]("get_weather", "查天气",
//	    func(ctx context.Context, in WeatherInput) (WeatherOutput, error) {
//	        return WeatherOutput{Temp: 25, Cond: "晴"}, nil
//	    })
func TypedFunc[In, Out any](name, description string, handler func(context.Context, In) (Out, error), opts ...ToolOption) (Tool, error) {
	cfg := typedToolConfig{permission: PermitAllow}
	for _, o := range opts {
		o(&cfg)
	}

	var zero In
	inType := reflect.TypeOf(zero)
	if err := validateToolInputType(inType); err != nil {
		return Tool{}, err
	}

	schema, err := llm.GenerateSchema(inType)
	if err != nil {
		return Tool{}, fmt.Errorf("agent: generate schema for %T: %w", zero, err)
	}

	call := func(ctx context.Context, args json.RawMessage) (string, error) {
		var input In
		if len(args) > 0 && string(args) != "null" {
			if err := json.Unmarshal(args, &input); err != nil {
				return "", fmt.Errorf("agent: unmarshal tool args: %w", err)
			}
		}
		output, err := handler(ctx, input)
		if err != nil {
			return "", err
		}
		return marshalToolOutput(output)
	}

	return Tool{
		Def:        llm.ToolDef{Name: name, Description: description, Parameters: schema},
		Call:       call,
		Permission: cfg.permission,
	}, nil
}

// MustTypedFunc 同 TypedFunc,出错时 panic。
func MustTypedFunc[In, Out any](name, description string, handler func(context.Context, In) (Out, error), opts ...ToolOption) Tool {
	tool, err := TypedFunc(name, description, handler, opts...)
	if err != nil {
		panic(err)
	}
	return tool
}

func validateToolInputType(t reflect.Type) error {
	if t == nil {
		return fmt.Errorf("agent: tool input type must be struct, got untyped nil")
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("agent: tool input type must be struct, got %s", t.Kind())
	}
	return nil
}

func marshalToolOutput[Out any](output Out) (string, error) {
	if s, ok := any(output).(string); ok {
		return s, nil
	}
	b, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("agent: marshal tool result: %w", err)
	}
	return string(b), nil
}
