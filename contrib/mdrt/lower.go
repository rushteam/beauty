package mdrt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// unitCode 是一个单元已经确定的函数体。
type unitCode struct {
	Body     string
	CacheKey string
	Pending  bool
}

// Lower 把 spec 和每个单元的函数体下降成一个入口脚本。同一输入得到同一字节序列。
func Lower(spec *Spec, bodies []unitCode) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("mdrt: 空 spec")
	}
	if len(bodies) != len(spec.Units) {
		return "", fmt.Errorf("mdrt: 函数体数量与单元数量不一致")
	}
	var b strings.Builder
	b.WriteString("// mdrt generated. spec=")
	b.WriteString(spec.Slug)
	b.WriteString(" dialect=")
	b.WriteString(Dialect)
	b.WriteByte('\n')
	for i, u := range spec.Units {
		code := bodies[i]
		if err := Check(u.Kind, code.Body); err != nil {
			return "", fmt.Errorf("%s 单元 %d: %w", spec.Slug, i, err)
		}
		writeUnit(&b, spec, i, u, code)
	}
	return b.String(), nil
}

func writeUnit(b *strings.Builder, spec *Spec, i int, u Unit, code unitCode) {
	id := ident(spec.Slug, i)
	sum := sha256.Sum256([]byte(code.Body))
	tag := "impl"
	switch {
	case code.Pending:
		tag = "pending"
	case code.CacheKey != "":
		tag = "cache:" + code.CacheKey[:8]
	}
	fmt.Fprintf(b, "// source=%s#L%d sha256=%s behavior=%s\n", spec.Path, u.Line, hex.EncodeToString(sum[:4]), tag)
	fmt.Fprintf(b, "const %s_fn = function (%s) {\n", id, paramList(u.Kind))
	for _, line := range strings.Split(code.Body, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("};\n")
	schema := schemaJSON(u.Inputs)
	allow := allowJSON(spec.HTTPAllow)
	fmt.Fprintf(b, "const %s_schema = %s;\n", id, schema)
	fmt.Fprintf(b, "const %s_allow = %s;\n", id, allow)

	switch u.Kind {
	case KindRoute:
		auth := u.effectiveAuth(spec.Auth)
		fmt.Fprintf(b, "routerAdd(%s, %s, function (e) {\n", strconv.Quote(u.Method), strconv.Quote(u.Pattern))
		b.WriteString("  var uid = __mdrt.bearer(e);\n")
		if auth == AuthBearer {
			b.WriteString("  if (!uid) throw new UnauthorizedError();\n")
		}
		fmt.Fprintf(b, "  var ctx = __mdrt.ctx(%s, %d, uid, %s_allow);\n", strconv.Quote(spec.Slug), i, id)
		fmt.Fprintf(b, "  var input = __mdrt.input(e, %s_schema);\n", id)
		fmt.Fprintf(b, "  __mdrt.respond(e, %s_fn(input, ctx));\n", id)
		b.WriteString("});\n")
	case KindCron:
		fmt.Fprintf(b, "cronAdd(%s, %s, function () {\n", strconv.Quote(u.Name), strconv.Quote(u.CronSpec))
		fmt.Fprintf(b, "  var ctx = __mdrt.ctx(%s, %d, \"\", %s_allow);\n", strconv.Quote(spec.Slug), i, id)
		fmt.Fprintf(b, "  %s_fn(ctx);\n", id)
		b.WriteString("});\n")
	case KindHook:
		fmt.Fprintf(b, "hookAdd(%s, function (event) {\n", strconv.Quote(u.Name))
		fmt.Fprintf(b, "  var ctx = __mdrt.ctx(%s, %d, \"\", %s_allow);\n", strconv.Quote(spec.Slug), i, id)
		fmt.Fprintf(b, "  var input = __mdrt.pick(event, %s_schema);\n", id)
		fmt.Fprintf(b, "  %s_fn(event, input, ctx);\n", id)
		b.WriteString("});\n")
	case KindEvent:
		fmt.Fprintf(b, "eventSubscribe(%s, function (topic, payload) {\n", strconv.Quote(u.Name))
		fmt.Fprintf(b, "  var ctx = __mdrt.ctx(%s, %d, \"\", %s_allow);\n", strconv.Quote(spec.Slug), i, id)
		fmt.Fprintf(b, "  var input = __mdrt.pick(payload || {}, %s_schema);\n", id)
		fmt.Fprintf(b, "  %s_fn(payload, input, ctx);\n", id)
		b.WriteString("});\n")
	}
	fmt.Fprintf(b, "hookAdd(%s, function (ev) { __mdrt.invoke(%s, ev, %s_fn, %s_schema, %s, %d, %s_allow); });\n",
		strconv.Quote(fmt.Sprintf("mdrt:unit:%s:%d", spec.Slug, i)),
		strconv.Quote(u.Kind), id, id, strconv.Quote(spec.Slug), i, id)
}

func ident(slug string, i int) string {
	return "__" + strings.ReplaceAll(slug, "-", "_") + "_u" + strconv.Itoa(i)
}

type fieldSchema struct {
	In       string   `json:"in"`
	Type     string   `json:"type"`
	Optional bool     `json:"optional,omitempty"`
	GT       *float64 `json:"gt,omitempty"`
	GTE      *float64 `json:"gte,omitempty"`
	LT       *float64 `json:"lt,omitempty"`
	LTE      *float64 `json:"lte,omitempty"`
	MinLen   *int     `json:"minLen,omitempty"`
	MaxLen   *int     `json:"maxLen,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Pattern  string   `json:"pattern,omitempty"`
}

func schemaJSON(fields []Field) string {
	obj := make(map[string]fieldSchema, len(fields))
	for _, f := range fields {
		obj[f.Name] = fieldSchema{
			In: f.In, Type: f.Type, Optional: f.Optional,
			GT: f.GT, GTE: f.GTE, LT: f.LT, LTE: f.LTE,
			MinLen: f.MinLen, MaxLen: f.MaxLen,
			Enum: f.Enum, Pattern: f.Pattern,
		}
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func allowJSON(hosts []string) string {
	if hosts == nil {
		hosts = []string{}
	}
	raw, err := json.Marshal(hosts)
	if err != nil {
		return "[]"
	}
	return string(raw)
}
