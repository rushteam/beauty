# contrib/mdrt —— Markdown 即代码

监听 `specs/*.md`,把受限方言下降成 [jsvm](../jsvm) 脚本并热切换。路由、入参、鉴权和例子由解析器生成;只有「行为」小节交给 `llm.Client` 写函数体。例子不过就继续跑上一版。请求路径上不调用模型。

```bash
cd contrib/mdrt
go test ./...
go run ./example          # 离线:只跑「实现」段
go run ./example/llm      # 开发模式:用模型编译「行为」段
```

设计说明见 [docs/markdown-runtime.md](../../docs/markdown-runtime.md)。

## 运行

```go
client := openai.New(os.Getenv("LLM_API_KEY")) // 兼容网关再加 openai.WithBaseURL(...)
rt, err := mdrt.New(
    mdrt.WithSpecDir("specs"),
    mdrt.WithStateDir(".mdrt"),
    mdrt.WithLockFile("mdrt.lock.json"),
    mdrt.WithMode(mdrt.ModeDev),
    mdrt.WithLLM(client, os.Getenv("LLM_MODEL")),
    mdrt.WithJWTSecret(os.Getenv("JWT_SECRET")),
)
app := beauty.New(
    beauty.WithService(rt),
    beauty.WithWebServer(":8080", rt),
)
_ = app.Start(ctx)
```

`WithLLM` 接受任意 `llm.Client`。上面用的是 OpenAI 兼容客户端,`LLM_BASE_URL` 可换成 DeepSeek、Kimi、智谱、通义等兼容网关(常量见 `contrib/llm/openai`)。模型必须接受 `response_format: json_schema`;不支持时编译失败,旧脚本继续服务,诊断写在 `.mdrt/diagnostics.json`。

不配 `WithLLM`,或模式是 `ModeOffline` 时不调用模型。手写的 `## 实现` 直接下降;没有缓存的行为段先返回 503。

开发模式启动后,行为段在后台编译。日志打出 `mdrt: 换代`,并且该文件的 `state` 变成 `ok` 之后,请求才离开 503。请求路径上不会再打到模型。改「行为」正文会重新编译;只改例子标题命中缓存,调用次数为 0。

`mdrt.Build` 把通过验收的脚本写到 `.mdrt/gen`,并更新锁文件。生产进程可以只嵌入这些脚本,用 `jsvm.WithFS` 运行,不必链接本包或 `contrib/llm`。`example/frozen` 就是这个形态。

## 接上真实模型

`example/llm` 监听 `example/llm/specs/quote.md`。这份 spec 没有 `## 实现`,九折规则写在 `## 行为` 里,由模型生成函数体,再用文件里的三个例子验收。

在 `contrib/mdrt` 目录执行:

```bash
export LLM_API_KEY=sk-...          # 也可改用 OPENAI_API_KEY
export LLM_MODEL=gpt-4o-mini
# export LLM_BASE_URL=https://api.deepseek.com/v1
go run ./example/llm
```

等到日志里的换代记录,再请求:

```bash
curl -s -d '{"amount":1200}' localhost:8080/quote
# {"amount":1200,"pay":1080}
curl -s -d '{"amount":0}' localhost:8080/quote
# 400
```

生成的脚本在 `example/llm/.mdrt/gen/quote.js`,缓存和锁文件也在这个目录下,默认不进 git。改 `quote.md` 的行为段后保存,运行时会去抖、再编译、例子通过才换代。

`example` 不接模型,三份 spec 全是 `## 实现`,用来看热更新和跨文件 `ctx.trigger`。`example/frozen` 只嵌入已经生成的脚本。

## 方言

一个文件一个模块,文件名就是 `name`。

```markdown
---
dialect: mdrt/v0
name: orders
auth: bearer
http: [api.example.com]
---

# POST /orders

## 输入
- amount: number, > 0

## 行为
大于 1000 打九折,写入 store,返回 201。

## 例子

### 满 1000 打九折
```req
POST /orders
Authorization: Bearer $token(u1)

{"amount": 1200}
```
```res
201
{"pay": 1080}
```

### 金额非法
```req
POST /orders
Authorization: Bearer $token(u1)

{"amount": 0}
```
```res
400
```
```

一级标题还可以是 `cron <name> <spec>`、`hook <name>`、`event <topic>`。`## 实现` 里的 js 代码块会跳过模型。行为段至少两个例子,其中一个是错误或边界(状态码 ≥ 400,或标题里写明)。

外部 HTTP 桩写在例子里:

````markdown
```stub
GET https://api.example.com/price
200
{"discount": 0.9}
```
````

脚本只能通过 `ctx` 使用 store、HTTP、日志、时间和 `ctx.trigger`。`$app`、`$http`、`$os` 出现在函数体里会被静态检查拒绝。
