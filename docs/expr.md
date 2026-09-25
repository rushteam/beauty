# 表达式求值器 / 规则引擎 expr

把动态业务规则(风控策略、优惠叠加、动态打标签、灰度、审批路由)从硬编码的 if-else 抽离成可配置的表达式字符串,编译成 AST 后针对不同上下文反复求值。

- 包:[`pkg/api/expr`](../pkg/api/expr) — 示例 [expr](../examples/expr)

纯标准库、零依赖,契合"机制而非策略":本包只负责"把表达式安全算出结果",规则从哪来(配置中心/DB)、变量怎么装、结果怎么用都是上层策略。

## 快速开始

```go
import "github.com/rushteam/beauty/pkg/api/expr"

// 编译一次,反复求值
p, err := expr.Compile(`user.age >= 18 && country in ["CN", "US"]`)
ok, err := p.EvalBool(expr.Env{
    "user":    map[string]any{"age": 20},
    "country": "CN",
})

// 一次性求值
v, err := expr.Eval(`amount * 0.9`, expr.Env{"amount": 100})
```

`Compile` 返回的 `*Program` 编译后只读,可被多 goroutine 并发 `Eval`;每次 `Eval` 只读传入的 `Env`,自身无状态。

## 支持的语法

| 类别 | 语法 |
|---|---|
| 字面量 | 数字 `123` `3.14`、字符串 `"a"` `'a'`、布尔 `true`/`false`、`nil` |
| 变量 | 标识符从 `Env` 取值;点号访问嵌套 map:`user.age` |
| 算术 | `+ - * / %`(`+` 兼作字符串拼接) |
| 比较 | `== != < <= > >=`(数字按值,字符串按字典序) |
| 逻辑 | `&& \|\| !`(短路求值,操作数按"真值性") |
| 成员 | `in`:`x in [1,2,3]`、`"lo" in "hello"` |
| 分组/数组 | `( )`、`[a, b, c]` |
| 函数 | `fn(args...)`,函数从 `Env` 取(值为 `expr.Func`) |

真值性(`EvalBool`):`nil`/`false`/`0`/空串/空数组为 `false`,其余为 `true`。

## 典型场景

风控规则(命中即拦截):

```go
risk, _ := expr.Compile(`amount > 10000 && (country != "CN" || user.age < 18)`)
hit, _ := risk.EvalBool(env)
```

注入函数做优惠计算:

```go
env := expr.Env{
    "price": 100.0,
    "min": expr.Func(func(args ...any) (any, error) {
        a, b := args[0].(float64), args[1].(float64)
        if a < b { return a, nil }
        return b, nil
    }),
}
v, _ := expr.Eval(`min(price * 0.8, price - 15)`, env) // → 80
```

动态打标签(成员判定):

```go
tag, _ := expr.Compile(`country in ["CN", "HK", "MO", "TW"]`)
v, _ := tag.EvalBool(expr.Env{"country": "HK"}) // true
```

## 说明

- 数值统一按 `float64` 参与运算(`%` 用 `math.Mod`),规则场景够用。
- 运行期错误(未定义变量、除零、类型不匹配)以 `error` 返回,不 panic。
- 不支持三元 `?:`;用 `&&`/`||` 或函数表达等价逻辑。
