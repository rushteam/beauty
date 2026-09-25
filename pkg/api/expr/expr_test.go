package expr

import (
	"math"
	"testing"
)

func evalOK(t *testing.T, input string, env Env) any {
	t.Helper()
	v, err := Eval(input, env)
	if err != nil {
		t.Fatalf("Eval(%q) error: %v", input, err)
	}
	return v
}

func TestArithmetic(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"1 + 2 * 3", 7},
		{"(1 + 2) * 3", 9},
		{"10 / 4", 2.5},
		{"10 % 3", 1},
		{"-5 + 3", -2},
		{"2 * -3", -6},
		{"3.14 * 2", 6.28},
	}
	for _, c := range cases {
		got := evalOK(t, c.in, nil).(float64)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%q = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestComparisonAndLogic(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"1 < 2", true},
		{"2 <= 2", true},
		{"3 > 5", false},
		{"5 >= 5", true},
		{"1 == 1", true},
		{"1 != 2", true},
		{"true && false", false},
		{"true || false", true},
		{"!false", true},
		{"1 < 2 && 3 < 4", true},
		{"1 > 2 || 3 < 4", true},
		{`"abc" < "abd"`, true},
		{`"a" == "a"`, true},
	}
	for _, c := range cases {
		got := evalOK(t, c.in, nil)
		if got != c.want {
			t.Errorf("%q = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestVariablesAndMembers(t *testing.T) {
	env := Env{
		"age":     20,
		"country": "CN",
		"user":    map[string]any{"vip": true, "level": 3},
	}
	p, err := Compile(`age >= 18 && user.vip && user.level > 2 && country in ["CN", "US"]`)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := p.EvalBool(env)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("rule should match")
	}
	// 改成不满足
	env["age"] = 16
	ok, _ = p.EvalBool(env)
	if ok {
		t.Fatal("rule should not match when age<18")
	}
}

func TestStringConcatAndIn(t *testing.T) {
	if got := evalOK(t, `"hello " + "world"`, nil); got != "hello world" {
		t.Errorf("concat = %v", got)
	}
	if got := evalOK(t, `"lo" in "hello"`, nil); got != true {
		t.Errorf("substring in = %v", got)
	}
	if got := evalOK(t, `3 in [1, 2, 3]`, nil); got != true {
		t.Errorf("member in = %v", got)
	}
	if got := evalOK(t, `4 in [1, 2, 3]`, nil); got != false {
		t.Errorf("member not in = %v", got)
	}
}

func TestFunctions(t *testing.T) {
	env := Env{
		"amount": 100.0,
		"max": Func(func(args ...any) (any, error) {
			a, _ := toFloat(args[0])
			b, _ := toFloat(args[1])
			return math.Max(a, b), nil
		}),
		"discount": Func(func(args ...any) (any, error) {
			a, _ := toFloat(args[0])
			return a * 0.9, nil
		}),
	}
	got := evalOK(t, `discount(amount)`, env).(float64)
	if math.Abs(got-90) > 1e-9 {
		t.Errorf("discount = %v want 90", got)
	}
	got = evalOK(t, `max(amount, 200)`, env).(float64)
	if got != 200 {
		t.Errorf("max = %v want 200", got)
	}
}

func TestShortCircuit(t *testing.T) {
	// 右侧引用未定义变量;左侧短路后不应求值右侧,故不报错
	if got := evalOK(t, `false && undefinedVar`, nil); got != false {
		t.Errorf("&& short-circuit = %v", got)
	}
	if got := evalOK(t, `true || undefinedVar`, nil); got != true {
		t.Errorf("|| short-circuit = %v", got)
	}
}

func TestEvalBoolTruthiness(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0", false},
		{"1", true},
		{`""`, false},
		{`"x"`, true},
		{"nil", false},
		{"[]", false},
		{"[1]", true},
	}
	for _, c := range cases {
		p, err := Compile(c.in)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := p.EvalBool(nil)
		if got != c.want {
			t.Errorf("EvalBool(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

func TestErrors(t *testing.T) {
	bad := []string{
		"1 +",
		"(1 + 2",
		"1 = 2",
		"foo(",
		"& 1",
		`"unterminated`,
		"1 2 3",
	}
	for _, in := range bad {
		if _, err := Compile(in); err == nil {
			t.Errorf("Compile(%q) expected error", in)
		}
	}
	// 运行期错误
	if _, err := Eval("undefinedVar + 1", nil); err == nil {
		t.Error("expected undefined variable error")
	}
	if _, err := Eval("1 / 0", nil); err == nil {
		t.Error("expected division by zero error")
	}
}

func TestReuseProgram(t *testing.T) {
	p, err := Compile(`score * weight`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		v, err := p.Eval(Env{"score": i, "weight": 2})
		if err != nil {
			t.Fatal(err)
		}
		if v.(float64) != float64(i*2) {
			t.Errorf("reuse eval %d = %v", i, v)
		}
	}
}
