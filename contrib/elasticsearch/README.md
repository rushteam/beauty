# contrib/elasticsearch —— Elasticsearch 集成(独立模块)

薄封装官方 `go-elasticsearch/v8`:按 beauty 约定建客户端,给出健康探测 / 搜索 / 写入便捷方法。
独立模块,不 import beauty 核心,可脱离框架单用。

```bash
go get github.com/rushteam/beauty/contrib/elasticsearch@latest
```

## 用法

```go
import es "github.com/rushteam/beauty/contrib/elasticsearch"

client, _ := es.New(es.Config{
    Addresses: []string{"http://127.0.0.1:9200"},
    Username:  "elastic", Password: "***", // 或 APIKey / CloudID
})

client.Ping(ctx) // 健康检查

raw, _ := client.Search(ctx, "users", []byte(`{"query":{"match":{"name":"alice"}}}`))
// raw 是原始响应 JSON,自行解析 hits

client.Index(ctx, "users", "1", []byte(`{"name":"alice"}`))

client.ES() // 拿底层官方客户端用完整 API(bulk、聚合、typed client 等)
```

## 边界

索引 mapping、查询 DSL、聚合、分页都在使用方——本模块只把官方客户端接好并暴露原始 JSON,
不发明查询构造器。端到端需真实 ES 集群;单测用 httptest 打桩覆盖 Ping/Search 请求与响应处理。
