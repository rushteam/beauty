package mdrt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

const systemPrompt = `你是 mdrt 的函数体编译器。只输出一个 JSON 对象 {"body":"...","notes":"..."}，不要 Markdown。
body 是 JavaScript 函数体，不是完整函数，不要包 function。
宿主已经按单元类型声明了参数:
- route: input, ctx。返回 {status, body}。
- hook: event, input, ctx。直接修改 event。
- event: payload, input, ctx。
- cron: ctx。
ctx.uid 是字符串。
ctx.store.get/set/has/remove 使用不带模块前缀的短键。
ctx.http.send({url, method, headers, body}) 返回 {statusCode, json, raw}。
ctx.log.info/warn/error(msg)。
ctx.now() 返回毫秒。ctx.id() 返回随机串。
ctx.fail(status, message, data) 抛出错误。
ctx.trigger(name, event) 同步调用 hook，并允许修改 event。
禁止 async/await、Promise、require、eval、Function、globalThis、直接访问 $app $http $os $filesystem $security，也禁止注册路由或定时任务。
用户给出的例子会被真实执行。错误和边界例子也必须满足。`

type behaviorOut struct {
	Body  string `json:"body"`
	Notes string `json:"notes"`
}

func (rt *Runtime) generate(ctx context.Context, spec *Spec, unit Unit, hint string) (string, llm.Usage, error) {
	if rt.cfg.llm == nil {
		return "", llm.Usage{}, fmt.Errorf("mdrt: 没有配置模型")
	}
	rt.sem <- struct{}{}
	defer func() { <-rt.sem }()

	key := CacheKey(rt.cfg.model, unit) + "\n" + hint
	body, err, _ := rt.flight.Do(key, func() (string, error) {
		user := unitPrompt(spec, unit, hint)
		req := llm.Request{
			Model:              rt.cfg.model,
			System:             systemPrompt,
			Temperature:        0,
			SystemCacheControl: "ephemeral",
			Messages: []llm.Message{{
				Role:    llm.User,
				Content: user,
			}},
		}
		if rt.typed != nil {
			rt.typed.ApplyTo(&req)
		}
		resp, err := rt.cfg.llm.Generate(ctx, req)
		if err != nil {
			return "", err
		}
		rt.llmCalls.Add(1)
		if resp != nil {
			rt.inTokens.Add(uint64(resp.Usage.InputTokens))
			rt.outTokens.Add(uint64(resp.Usage.OutputTokens))
		}
		text := ""
		if resp != nil {
			text = resp.Content
		}
		parsed, err := parseModelBody(text)
		if err != nil {
			return "", err
		}
		if err := Check(unit.Kind, parsed); err != nil {
			return "", err
		}
		return parsed, nil
	})
	if err != nil {
		return "", llm.Usage{}, err
	}
	return body, llm.Usage{}, nil
}

func unitPrompt(spec *Spec, unit Unit, hint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "模块 %s,单元类型 %s。\n", spec.Slug, unit.Kind)
	if unit.Kind == KindRoute {
		fmt.Fprintf(&b, "路由 %s %s,鉴权 %s。\n", unit.Method, unit.Pattern, unit.effectiveAuth(spec.Auth))
	} else if unit.Name != "" {
		fmt.Fprintf(&b, "名字 %s。\n", unit.Name)
	}
	fmt.Fprintf(&b, "允许的外部域名: %s。\n", strings.Join(spec.HTTPAllow, ","))
	fmt.Fprintf(&b, "入参: %s\n", schemaJSON(unit.Inputs))
	b.WriteString("行为:\n")
	b.WriteString(unit.Behavior.Body)
	b.WriteString("\n例子:\n")
	for _, ex := range unit.Examples {
		fmt.Fprintf(&b, "- %s\n", ex.Title)
		if ex.Req != nil {
			fmt.Fprintf(&b, "  请求 %s %s %s\n", ex.Req.Method, ex.Req.URL, ex.Req.Body)
		}
		if ex.Res != nil {
			fmt.Fprintf(&b, "  响应 %d %s\n", ex.Res.Status, ex.Res.Body)
		}
		if ex.Event != "" {
			fmt.Fprintf(&b, "  事件 %s\n", ex.Event)
		}
		if ex.EventAfter != "" {
			fmt.Fprintf(&b, "  事件之后 %s\n", ex.EventAfter)
		}
	}
	if hint != "" {
		b.WriteString("\n上一次结果没有通过,原因:\n")
		b.WriteString(hint)
		b.WriteString("\n请重新输出整个函数体。")
	}
	return b.String()
}

func parseModelBody(content string) (string, error) {
	content = strings.TrimSpace(content)
	content = stripFence(content)
	var out behaviorOut
	if err := json.Unmarshal([]byte(content), &out); err != nil || strings.TrimSpace(out.Body) == "" {
		return "", fmt.Errorf("模型输出不是 {\"body\":...}")
	}
	return out.Body, nil
}

func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

// 编译时间戳只用于缓存文件,测试不依赖它。
func nowStamp() time.Time { return time.Now().UTC() }
