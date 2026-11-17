package mdrt

import (
	"fmt"
	"reflect"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
	"github.com/dop251/goja/parser"
)

// 生成代码只能通过 ctx 使用宿主能力。这些标识符一旦出现就拒绝。
var forbiddenIdent = map[string]bool{
	"routerAdd": true, "routerUse": true,
	"cronAdd": true, "cronRemove": true,
	"hookAdd": true, "eventSubscribe": true,
	"onStart": true, "onStop": true, "onReload": true,
	"require": true, "eval": true, "Function": true,
	"Promise": true, "globalThis": true, "sleep": true,
	"$app": true, "$http": true, "$os": true,
	"$filesystem": true, "$security": true,
}

// Check 解析函数体并拒绝宿主 API、async/await 和提前闭合函数。
func Check(kind, body string) error {
	if len(body) > maxBodyBytes {
		return fmt.Errorf("函数体超过 %d 字节", maxBodyBytes)
	}
	src := "(function (" + paramList(kind) + ") {\n" + body + "\n})"
	prog, err := parser.ParseFile(&file.FileSet{}, "mdrt-unit.js", src, 0)
	if err != nil {
		return fmt.Errorf("无法解析函数体: %s", err.Error())
	}
	if prog == nil || len(prog.Body) != 1 {
		return fmt.Errorf("函数体提前结束或包含多余语句")
	}
	if err := walkNode(prog, func(n ast.Node) error {
		switch x := n.(type) {
		case *ast.FunctionLiteral:
			if x.Async || x.Generator {
				return fmt.Errorf("禁止 async 或生成器")
			}
		case *ast.ArrowFunctionLiteral:
			if x.Async {
				return fmt.Errorf("禁止 async")
			}
		case *ast.AwaitExpression:
			return fmt.Errorf("禁止 await")
		case *ast.YieldExpression:
			return fmt.Errorf("禁止 yield")
		case *ast.WithStatement:
			return fmt.Errorf("禁止 with")
		case *ast.Identifier:
			if forbiddenIdent[x.Name.String()] {
				return fmt.Errorf("禁止使用 %s", x.Name.String())
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func paramList(kind string) string {
	switch kind {
	case KindHook:
		return "event, input, ctx"
	case KindEvent:
		return "payload, input, ctx"
	case KindCron:
		return "ctx"
	default:
		return "input, ctx"
	}
}

func walkNode(n ast.Node, fn func(ast.Node) error) error {
	if n == nil || reflect.ValueOf(n).Kind() == reflect.Pointer && reflect.ValueOf(n).IsNil() {
		return nil
	}
	if err := fn(n); err != nil {
		return err
	}
	if dot, ok := n.(*ast.DotExpression); ok {
		return walkNode(dot.Left, fn)
	}
	v := reflect.ValueOf(n)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	for i := 0; i < v.NumField(); i++ {
		if err := walkValue(v.Field(i), fn); err != nil {
			return err
		}
	}
	return nil
}

func walkValue(v reflect.Value, fn func(ast.Node) error) error {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		if n, ok := asNode(v); ok {
			return walkNode(n, fn)
		}
		if v.Kind() == reflect.Pointer {
			return walkValue(v.Elem(), fn)
		}
		return nil
	case reflect.Struct:
		if n, ok := asNode(v); ok {
			return walkNode(n, fn)
		}
		for i := 0; i < v.NumField(); i++ {
			if err := walkValue(v.Field(i), fn); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := walkValue(v.Index(i), fn); err != nil {
				return err
			}
		}
	}
	return nil
}

func asNode(v reflect.Value) (ast.Node, bool) {
	if !v.IsValid() || !v.CanInterface() {
		return nil, false
	}
	if n, ok := v.Interface().(ast.Node); ok && n != nil {
		return n, true
	}
	if v.Kind() != reflect.Pointer && v.CanAddr() && v.Addr().CanInterface() {
		if n, ok := v.Addr().Interface().(ast.Node); ok && n != nil {
			return n, true
		}
	}
	return nil, false
}
