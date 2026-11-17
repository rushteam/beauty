// 示例:用 OpenAI 兼容接口编译 Markdown 里的「行为」段,并热切换。
//
//	cd contrib/mdrt
//	export LLM_API_KEY=sk-...          # 也可改用 OPENAI_API_KEY
//	export LLM_MODEL=gpt-4o-mini
//	# export LLM_BASE_URL=https://api.deepseek.com/v1
//	go run ./example/llm
//
// 启动后先返回 503。日志出现「mdrt: 换代」且 example/llm/.mdrt/diagnostics.json
// 里 quote 的 state 为 ok 之后,再请求:
//
//	curl -s -d '{"amount":1200}' localhost:8080/quote
//
// 改 example/llm/specs/quote.md 里的「行为」会再次调用模型。只改例子标题不会。
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/llm/openai"
	"github.com/rushteam/beauty/contrib/mdrt"
)

func main() {
	key := firstEnv("LLM_API_KEY", "OPENAI_API_KEY")
	model := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if key == "" || model == "" {
		log.Fatal("需要 LLM_API_KEY（或 OPENAI_API_KEY）以及 LLM_MODEL")
	}
	var clientOpts []openai.Option
	if base := strings.TrimSpace(os.Getenv("LLM_BASE_URL")); base != "" {
		clientOpts = append(clientOpts, openai.WithBaseURL(base))
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "dev-secret"
	}

	rt, err := mdrt.New(
		mdrt.WithSpecDir("example/llm/specs"),
		mdrt.WithStateDir("example/llm/.mdrt"),
		mdrt.WithLockFile("example/llm/mdrt.lock.json"),
		mdrt.WithMode(mdrt.ModeDev),
		mdrt.WithLLM(openai.New(key, clientOpts...), model),
		mdrt.WithJWTSecret(secret),
		mdrt.WithLogger(slog.Default()),
	)
	if err != nil {
		log.Fatal(err)
	}
	app := beauty.New(
		beauty.WithService(rt),
		beauty.WithWebServer(":8080", rt),
	)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("mdrt: 监听 example/llm/specs,模型 %s;编译完成前 /quote 返回 503", model)
	if err := app.Start(ctx); err != nil {
		log.Fatal(err)
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}
