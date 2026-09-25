// expr 示例:规则引擎 / 表达式求值。
//
// 演示 pkg/api/expr:把动态业务规则(风控、优惠、打标签)从硬编码 if-else 抽成可配置表达式,
// 编译一次、对不同上下文反复求值。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/api/expr"
)

func main() {
	// ===== 1) 风控规则:命中返回 true 即拦截 =====
	risk, _ := expr.Compile(`amount > 10000 && (country != "CN" || user.age < 18)`)
	users := []expr.Env{
		{"amount": 20000, "country": "US", "user": map[string]any{"age": 25}},
		{"amount": 5000, "country": "CN", "user": map[string]any{"age": 30}},
		{"amount": 15000, "country": "CN", "user": map[string]any{"age": 16}},
	}
	fmt.Println("== 风控规则 ==")
	for i, u := range users {
		hit, _ := risk.EvalBool(u)
		fmt.Printf("交易%d 命中风控? %v\n", i+1, hit)
	}

	// ===== 2) 优惠计算:表达式里调用注入的函数 =====
	env := expr.Env{
		"price": 100.0,
		"vip":   true,
		"min": expr.Func(func(args ...any) (any, error) {
			a := args[0].(float64)
			b := args[1].(float64)
			if a < b {
				return a, nil
			}
			return b, nil
		}),
	}
	discounted, err := expr.Eval(`min(price * 0.8, price - 15)`, env)
	if err == nil {
		fmt.Printf("\n== 优惠计算 ==\nmin(8折, 减15) = %v\n", discounted)
	}

	// ===== 3) 动态打标签:成员判定 in =====
	fmt.Println("\n== 动态打标签 ==")
	tag, _ := expr.Compile(`country in ["CN", "HK", "MO", "TW"]`)
	for _, c := range []string{"CN", "US", "HK"} {
		v, _ := tag.EvalBool(expr.Env{"country": c})
		fmt.Printf("%s 属于大中华区? %v\n", c, v)
	}
}
