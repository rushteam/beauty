# Expression Evaluator / Rule Engine expr

Move dynamic business rules (risk control, coupon stacking, dynamic tagging, gray release, approval routing) out of hard-coded if-else into configurable expression strings — compiled to an AST once and evaluated against many contexts.

- Package: [`pkg/api/expr`](../pkg/api/expr) — example [expr](../examples/expr)

Pure stdlib, zero deps, mechanism-not-policy: this package only "safely computes the expression"; where rules come from (config center/DB), how variables are populated, and how results are used are upstream policy.

## Quick start

```go
import "github.com/rushteam/beauty/pkg/api/expr"

// Compile once, evaluate many times
p, err := expr.Compile(`user.age >= 18 && country in ["CN", "US"]`)
ok, err := p.EvalBool(expr.Env{
    "user":    map[string]any{"age": 20},
    "country": "CN",
})

// One-shot
v, err := expr.Eval(`amount * 0.9`, expr.Env{"amount": 100})
```

A compiled `*Program` is read-only and safe for concurrent `Eval`; each `Eval` only reads the given `Env` and is stateless.

## Supported syntax

| Category | Syntax |
|---|---|
| Literals | numbers `123` `3.14`, strings `"a"` `'a'`, bool `true`/`false`, `nil` |
| Variables | identifiers from `Env`; dotted access into nested maps: `user.age` |
| Arithmetic | `+ - * / %` (`+` also concatenates strings) |
| Comparison | `== != < <= > >=` (numbers by value, strings lexicographically) |
| Logical | `&& \|\| !` (short-circuit, operands by truthiness) |
| Membership | `in`: `x in [1,2,3]`, `"lo" in "hello"` |
| Grouping/array | `( )`, `[a, b, c]` |
| Functions | `fn(args...)`, taken from `Env` (value of type `expr.Func`) |

Truthiness (`EvalBool`): `nil`/`false`/`0`/empty string/empty array are `false`, else `true`.

## Typical use

Risk rule (hit = block):

```go
risk, _ := expr.Compile(`amount > 10000 && (country != "CN" || user.age < 18)`)
hit, _ := risk.EvalBool(env)
```

Injected functions for discounts:

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

Dynamic tagging (membership):

```go
tag, _ := expr.Compile(`country in ["CN", "HK", "MO", "TW"]`)
v, _ := tag.EvalBool(expr.Env{"country": "HK"}) // true
```

## Notes

- Numbers are computed as `float64` (`%` uses `math.Mod`), sufficient for rule scenarios.
- Runtime errors (undefined variable, divide-by-zero, type mismatch) are returned as `error`, never panic.
- No ternary `?:`; use `&&`/`||` or functions for equivalent logic.
