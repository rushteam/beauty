// Package expr 提供一个轻量表达式求值器 / 规则引擎:把动态的业务条件(风控规则、
// 优惠券叠加、动态打标签、特性开关灰度、审批路由)从硬编码的 if-else 抽离成可配置的
// 表达式字符串,编译成 AST 后针对不同上下文(变量集合)反复求值。
//
// 纯标准库、零依赖,契合 core "机制而非策略"的定位:本包只负责"把表达式安全地算出结果",
// 规则从哪来(配置中心 / DB)、变量怎么装、结果怎么用都是上层策略。
//
// 支持的语法:
//   - 字面量:数字(123、3.14)、字符串("a" 或 'a')、布尔(true/false)、nil;
//   - 变量:标识符从 Env 取值,支持点号访问嵌套 map(user.age、order.amount);
//   - 算术:+ - * / %(+ 兼作字符串拼接);
//   - 比较:== != < <= > >=(数字按值、字符串按字典序);
//   - 逻辑:&& || !(短路求值,操作数按"真值性"判定);
//   - 成员:in(x in [1,2,3] 或 x in listVar);
//   - 分组:( );数组字面量:[a, b, c];
//   - 函数调用:fn(args...),函数从 Env 里取(值为 Func 类型)。
//
// 用法:
//
//	p, err := expr.Compile(`user.age >= 18 && country in ["CN","US"]`)
//	ok, err := p.EvalBool(expr.Env{"user": map[string]any{"age": 20}, "country": "CN"})
//
// 或一次性:
//
//	v, err := expr.Eval(`amount * 0.9`, expr.Env{"amount": 100})
//
// 并发安全:Compile 返回的 *Program 编译后只读,可被多 goroutine 并发 Eval;
// 每次 Eval 只读传入的 Env,自身无状态。零值 *Program 不可用,用 Compile 构造。
package expr

// Env 是求值上下文:变量名 → 值。值可为数字、字符串、布尔、nil、嵌套 map[string]any、
// 切片([]any 供 in 使用),或 Func(供函数调用)。
type Env map[string]any

// Func 是可在表达式中调用的函数。参数已求值,返回值与错误。
type Func func(args ...any) (any, error)

// Program 是编译后的表达式。只读、并发安全。
type Program struct {
	root   node
	source string
}

// Source 返回原始表达式文本。
func (p *Program) Source() string { return p.source }

// Compile 把表达式文本解析编译为 Program。语法错误返回 error。
func Compile(input string) (*Program, error) {
	toks, err := lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: toks}
	root, err := p.parse()
	if err != nil {
		return nil, err
	}
	return &Program{root: root, source: input}, nil
}

// Eval 编译并对 env 求值,返回结果。用于一次性求值(频繁复用同一表达式应先 Compile 再 Eval)。
func Eval(input string, env Env) (any, error) {
	p, err := Compile(input)
	if err != nil {
		return nil, err
	}
	return p.Eval(env)
}

// Eval 对 env 求值,返回结果(any)。env 为 nil 视为空上下文。
func (p *Program) Eval(env Env) (any, error) {
	if env == nil {
		env = Env{}
	}
	return p.root.eval(env)
}

// EvalBool 对 env 求值并按"真值性"转为 bool(nil/false/0/空串/空数组为 false)。
// 适合规则判定:命中返回 true。
func (p *Program) EvalBool(env Env) (bool, error) {
	v, err := p.Eval(env)
	if err != nil {
		return false, err
	}
	return truthy(v), nil
}
