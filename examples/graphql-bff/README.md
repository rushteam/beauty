# GraphQL BFF Example (gqlgen schema-first)

完整 BFF 示例：用 `contrib/graphql` 把 gqlgen 生成的 schema 挂成 `beauty.Service`。

## 演示能力

- `gql.New` 作为 beauty 一等公民服务
- DataLoader（`Order.user` / `Query.user` 批量合并）
- Bearer 认证提取 + `OutgoingMetadata` 下游透传
- 查询复杂度 / 深度限制
- WebSocket / SSE Subscription

## 运行

```bash
cd examples/graphql-bff
go run .
```

打开 http://localhost:8080/（Playground），endpoint 为 `/query`。

## 重新生成

修改 `schema.graphql` 后：

```bash
go install github.com/99designs/gqlgen@v0.17.73
gqlgen generate
```

## 示例查询

```graphql
# 用户 + 订单（User.orders 走 field resolver）
query {
  user(id: "1") {
    id
    name
    email
    orders {
      id
      total
      status
      user { id name }   # Order.user 走 DataLoader
    }
  }
}

query {
  users(limit: 10) { id name }
}

mutation {
  createOrder(input: {
    userId: "1"
    items: ["item-a", "item-b"]
    total: 99.9
  }) {
    id
    status
  }
}

# Playground 需切到 Subscription，走 WebSocket
subscription {
  orderStatus(orderId: "order-1") {
    id
    status
    updatedAt
  }
}
```

带认证（可选）：

```http
Authorization: Bearer demo-token
```

## 目录

```
schema.graphql          # schema-first 定义
gqlgen.yml              # codegen 配置
graph/
  generated.go         # gqlgen 生成
  models_gen.go
  schema.resolvers.go  # resolver 实现（可手改，重新 generate 会保留）
  resolver.go          # 依赖注入
  store.go             # 假后端
  loaders.go           # DataLoader 中间件
main.go                 # beauty + contrib/graphql 启动
```
