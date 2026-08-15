// Package complexity 提供 GraphQL 查询复杂度限制和深度限制,
// 实现为 gqlgen HandlerExtension,在 Validate 阶段拦截恶意查询。
package complexity

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Config 配置复杂度限制。
type Config struct {
	MaxComplexity int            // 单次查询最大复杂度(默认 200)
	MaxDepth      int            // 最大嵌套深度(默认 15,0=不限)
	FieldWeights  map[string]int // 自定义字段权重("Type.field" → weight)
	OnReject      func(ctx context.Context, stats Stats)
}

// Stats 是单次查询的复杂度统计。
type Stats struct {
	Complexity int
	Depth      int
	Rejected   bool
	Reason     string
}

// Extension 创建复杂度限制 extension。
func Extension(cfg Config) graphql.HandlerExtension {
	if cfg.MaxComplexity <= 0 {
		cfg.MaxComplexity = 200
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 15
	}
	return &complexityExt{cfg: cfg}
}

type complexityExt struct {
	cfg Config
}

func (e *complexityExt) ExtensionName() string { return "ComplexityLimit" }

func (e *complexityExt) Validate(_ graphql.ExecutableSchema) error { return nil }

func (e *complexityExt) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	oc := graphql.GetOperationContext(ctx)
	if oc == nil || oc.Operation == nil {
		return next(ctx)
	}

	depth := calcDepth(oc.Operation.SelectionSet, 0)
	complexity := calcComplexity(oc.Operation.SelectionSet, e.cfg.FieldWeights)

	if e.cfg.MaxDepth > 0 && depth > e.cfg.MaxDepth {
		stats := Stats{Complexity: complexity, Depth: depth, Rejected: true, Reason: "max depth exceeded"}
		if e.cfg.OnReject != nil {
			e.cfg.OnReject(ctx, stats)
		}
		return func(ctx context.Context) *graphql.Response {
			return &graphql.Response{
				Errors: gqlerror.List{{
					Message: fmt.Sprintf("query depth %d exceeds maximum allowed depth %d", depth, e.cfg.MaxDepth),
					Extensions: map[string]interface{}{
						"code":  "QUERY_TOO_DEEP",
						"depth": depth,
						"max":   e.cfg.MaxDepth,
					},
				}},
			}
		}
	}

	if complexity > e.cfg.MaxComplexity {
		stats := Stats{Complexity: complexity, Depth: depth, Rejected: true, Reason: "max complexity exceeded"}
		if e.cfg.OnReject != nil {
			e.cfg.OnReject(ctx, stats)
		}
		return func(ctx context.Context) *graphql.Response {
			return &graphql.Response{
				Errors: gqlerror.List{{
					Message: fmt.Sprintf("query complexity %d exceeds maximum allowed complexity %d", complexity, e.cfg.MaxComplexity),
					Extensions: map[string]interface{}{
						"code":       "QUERY_TOO_COMPLEX",
						"complexity": complexity,
						"max":        e.cfg.MaxComplexity,
					},
				}},
			}
		}
	}

	return next(ctx)
}

func calcDepth(ss ast.SelectionSet, current int) int {
	if len(ss) == 0 {
		return current
	}
	max := current
	for _, sel := range ss {
		var childSS ast.SelectionSet
		switch s := sel.(type) {
		case *ast.Field:
			childSS = s.SelectionSet
		case *ast.InlineFragment:
			childSS = s.SelectionSet
		case *ast.FragmentSpread:
			if s.Definition != nil {
				childSS = s.Definition.SelectionSet
			}
		}
		d := calcDepth(childSS, current+1)
		if d > max {
			max = d
		}
	}
	return max
}

func calcComplexity(ss ast.SelectionSet, weights map[string]int) int {
	total := 0
	for _, sel := range ss {
		switch s := sel.(type) {
		case *ast.Field:
			cost := 1
			if weights != nil {
				if w, ok := weights[s.Name]; ok {
					cost = w
				}
				if s.ObjectDefinition != nil {
					key := s.ObjectDefinition.Name + "." + s.Name
					if w, ok := weights[key]; ok {
						cost = w
					}
				}
			}
			total += cost
			total += calcComplexity(s.SelectionSet, weights)
		case *ast.InlineFragment:
			total += calcComplexity(s.SelectionSet, weights)
		case *ast.FragmentSpread:
			if s.Definition != nil {
				total += calcComplexity(s.Definition.SelectionSet, weights)
			}
		}
	}
	return total
}
