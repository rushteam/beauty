package expr

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

// node 是 AST 节点:对给定 env 求值出一个 any。
type node interface {
	eval(env Env) (any, error)
}

// literalNode 字面量(数字/字符串/布尔/nil)。
type literalNode struct{ val any }

func (n literalNode) eval(Env) (any, error) { return n.val, nil }

// identNode 变量引用。
type identNode struct{ name string }

func (n identNode) eval(env Env) (any, error) {
	v, ok := env[n.name]
	if !ok {
		return nil, fmt.Errorf("expr: undefined variable %q", n.name)
	}
	return v, nil
}

// memberNode 成员访问 obj.field(obj 求值须为 map[string]any 或 Env)。
type memberNode struct {
	obj   node
	field string
}

func (n memberNode) eval(env Env) (any, error) {
	o, err := n.obj.eval(env)
	if err != nil {
		return nil, err
	}
	switch m := o.(type) {
	case map[string]any:
		v, ok := m[n.field]
		if !ok {
			return nil, fmt.Errorf("expr: no field %q", n.field)
		}
		return v, nil
	case Env:
		v, ok := m[n.field]
		if !ok {
			return nil, fmt.Errorf("expr: no field %q", n.field)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("expr: cannot access field %q on %T", n.field, o)
	}
}

// arrayNode 数组字面量。
type arrayNode struct{ elems []node }

func (n arrayNode) eval(env Env) (any, error) {
	out := make([]any, len(n.elems))
	for i, e := range n.elems {
		v, err := e.eval(env)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// unaryNode 一元运算(- ! +)。
type unaryNode struct {
	op      tokenKind
	operand node
}

func (n unaryNode) eval(env Env) (any, error) {
	v, err := n.operand.eval(env)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case tMinus:
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("expr: cannot negate %T", v)
		}
		return -f, nil
	case tPlus:
		if _, ok := toFloat(v); !ok {
			return nil, fmt.Errorf("expr: unary + on non-number %T", v)
		}
		return v, nil
	case tNot:
		return !truthy(v), nil
	}
	return nil, fmt.Errorf("expr: bad unary op")
}

// logicalNode 短路逻辑(&& ||)。
type logicalNode struct {
	op          tokenKind
	left, right node
}

func (n logicalNode) eval(env Env) (any, error) {
	l, err := n.left.eval(env)
	if err != nil {
		return nil, err
	}
	lt := truthy(l)
	switch n.op {
	case tAnd:
		if !lt {
			return false, nil // 短路
		}
	case tOr:
		if lt {
			return true, nil // 短路
		}
	}
	r, err := n.right.eval(env)
	if err != nil {
		return nil, err
	}
	return truthy(r), nil
}

// binaryNode 二元运算(算术 / 比较 / in)。
type binaryNode struct {
	op          tokenKind
	left, right node
}

func (n binaryNode) eval(env Env) (any, error) {
	l, err := n.left.eval(env)
	if err != nil {
		return nil, err
	}
	r, err := n.right.eval(env)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case tPlus:
		// 字符串拼接优先
		if ls, ok := l.(string); ok {
			if rs, ok := r.(string); ok {
				return ls + rs, nil
			}
		}
		return arith(n.op, l, r)
	case tMinus, tStar, tSlash, tPercent:
		return arith(n.op, l, r)
	case tEq:
		return equalValues(l, r), nil
	case tNeq:
		return !equalValues(l, r), nil
	case tLt, tLte, tGt, tGte:
		return compare(n.op, l, r)
	case tIn:
		return contains(r, l)
	}
	return nil, fmt.Errorf("expr: bad binary op")
}

// callNode 函数调用 name(args...),函数从 env[name] 取(须为 Func)。
type callNode struct {
	name string
	args []node
}

func (n callNode) eval(env Env) (any, error) {
	fv, ok := env[n.name]
	if !ok {
		return nil, fmt.Errorf("expr: undefined function %q", n.name)
	}
	fn, ok := fv.(Func)
	if !ok {
		return nil, fmt.Errorf("expr: %q is not a function (%T)", n.name, fv)
	}
	args := make([]any, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(env)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	return fn(args...)
}

// ===== 运行时辅助 =====

// arith 执行数值算术。
func arith(op tokenKind, l, r any) (any, error) {
	lf, lok := toFloat(l)
	rf, rok := toFloat(r)
	if !lok || !rok {
		return nil, fmt.Errorf("expr: arithmetic on non-number (%T, %T)", l, r)
	}
	switch op {
	case tPlus:
		return lf + rf, nil
	case tMinus:
		return lf - rf, nil
	case tStar:
		return lf * rf, nil
	case tSlash:
		if rf == 0 {
			return nil, fmt.Errorf("expr: division by zero")
		}
		return lf / rf, nil
	case tPercent:
		if rf == 0 {
			return nil, fmt.Errorf("expr: modulo by zero")
		}
		return math.Mod(lf, rf), nil
	}
	return nil, fmt.Errorf("expr: bad arithmetic op")
}

// compare 执行 < <= > >=(数字或字符串)。
func compare(op tokenKind, l, r any) (any, error) {
	if lf, lok := toFloat(l); lok {
		if rf, rok := toFloat(r); rok {
			return cmpResult(op, cmpFloat(lf, rf)), nil
		}
	}
	if ls, lok := l.(string); lok {
		if rs, rok := r.(string); rok {
			return cmpResult(op, strings.Compare(ls, rs)), nil
		}
	}
	return nil, fmt.Errorf("expr: cannot compare %T and %T", l, r)
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpResult(op tokenKind, c int) bool {
	switch op {
	case tLt:
		return c < 0
	case tLte:
		return c <= 0
	case tGt:
		return c > 0
	case tGte:
		return c >= 0
	}
	return false
}

// contains 判断 elem 是否在 collection([]any / 字符串子串)里。
func contains(collection, elem any) (any, error) {
	switch c := collection.(type) {
	case []any:
		for _, item := range c {
			if equalValues(item, elem) {
				return true, nil
			}
		}
		return false, nil
	case string:
		if s, ok := elem.(string); ok {
			return strings.Contains(c, s), nil
		}
		return nil, fmt.Errorf("expr: 'in' on string requires string element, got %T", elem)
	default:
		// 反射兜底:任意切片
		rv := reflect.ValueOf(collection)
		if rv.Kind() == reflect.Slice {
			for i := 0; i < rv.Len(); i++ {
				if equalValues(rv.Index(i).Interface(), elem) {
					return true, nil
				}
			}
			return false, nil
		}
		return nil, fmt.Errorf("expr: 'in' requires array/string on right, got %T", collection)
	}
}

// equalValues 相等判定:数字按值(跨 int/float),其余用 DeepEqual。
func equalValues(a, b any) bool {
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	return reflect.DeepEqual(a, b)
}

// truthy 真值性:nil/false/0/空串/空数组为 false,其余为 true。
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	}
	if f, ok := toFloat(v); ok {
		return f != 0
	}
	return true
}

// toFloat 把常见数值类型转 float64。
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}
