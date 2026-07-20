# contrib/mcp —— Model Context Protocol 集成(独立模块)

把你的服务能力暴露成 **MCP 工具/资源/提示**,让 AI 客户端(Claude、IDE、各类 Agent)调用;
也可作为 **MCP 客户端**消费别的 server。薄封装官方 `github.com/modelcontextprotocol/go-sdk`——
工具入参/出参的 **JSON Schema 由 SDK 泛型自动反射生成**,协议/传输/会话跟随官方演进。不 import beauty 核心。

```bash
go get github.com/rushteam/beauty/contrib/mcp@latest
```

## Server:暴露工具(struct→schema 自动反射)

```go
import "github.com/rushteam/beauty/contrib/mcp"

srv := mcp.NewServer("beauty-exchange", "1.0.0")

type PriceIn struct {
    Symbol string `json:"symbol" jsonschema:"交易对,如 BTC_USDT"`
}
mcp.AddTool(srv, &mcp.Tool{Name: "get_price", Description: "查询交易对最新价"},
    func(ctx context.Context, req *mcp.CallToolRequest, in PriceIn) (*mcp.CallToolResult, any, error) {
        return mcp.Text(lookup(in.Symbol)), nil, nil
    })
// In 结构体的字段(json/jsonschema 标签)→ SDK 自动生成入参 JSON Schema,无需手写。
```

## 两种部署

**远程(Streamable HTTP)**——挂到 beauty webserver:

```go
mux.Handle("/mcp", mcp.HTTPHandler(srv)) // 鉴权由外层中间件负责(policy)
app := beauty.New(beauty.WithWebServer(":8080", mux))
```

**本地工具(stdio)**——被 Claude Desktop 等拉起;`Service` 结构上满足 `beauty.Service`:

```go
app := beauty.New(beauty.WithService(mcp.NewStdioService(srv, "exchange")))
// 或直接:mcp.NewStdioService(srv, "x").Start(ctx)
```

## Client:消费别的 MCP server

```go
sess, _ := mcp.DialHTTP(ctx, "my-agent", "https://host/mcp")   // 远程
// sess, _ := mcp.DialCommand(ctx, "my-agent", exec.Command("some-mcp-server"))  // 拉子进程
defer sess.Close()

tools, _ := sess.ListTools(ctx, nil)
res, _ := sess.CallTool(ctx, &mcp.CallToolParams{Name: "get_price", Arguments: map[string]any{"symbol": "BTC_USDT"}})
```

## 边界

工具/资源的业务逻辑、鉴权(HTTP 端点外层套 auth 中间件 / OAuth)、具体 schema 都是 policy;
本包只做"协议 + 注册 + 传输 + beauty 挂载"。单测用 SDK 的 InMemory 传输 + httptest 做端到端
(注册/列举/调用 + struct→schema 反射),`-race` 通过。
