// Package main 演示基于 contrib/graphql + gqlgen schema-first 的完整 BFF。
//
// 能力:
//   - gql.New 作为 beauty.Service
//   - DataLoader 批量加载 User
//   - Bearer 认证提取与下游透传
//   - 查询复杂度 / 深度限制
//   - WebSocket Subscription
//
// 重新生成 GraphQL 代码:
//
//	go install github.com/99designs/gqlgen@v0.17.73
//	gqlgen generate
package main

import (
	"context"
	"log/slog"

	"github.com/rushteam/beauty"
	gql "github.com/rushteam/beauty/contrib/graphql"
	gqlauth "github.com/rushteam/beauty/contrib/graphql/auth"
	"github.com/rushteam/beauty/contrib/graphql/complexity"
	"github.com/rushteam/beauty/contrib/graphql/subscription"
	"github.com/rushteam/beauty/examples/graphql-bff/graph"
)

func main() {
	store := graph.NewStore()
	es := graph.NewExecutableSchema(graph.Config{
		Resolvers: &graph.Resolver{Store: store},
	})

	app := beauty.New(
		beauty.WithService(gql.New(":8080", es,
			gql.WithName("graphql-bff"),
			gql.WithPlayground(true),
			gql.WithPlaygroundPath("/"),
			gql.WithGraphQLPath("/query"),
			gql.WithMiddleware(
				// 认证: Authorization: Bearer <token>
				gqlauth.HTTPMiddleware(
					gqlauth.BearerExtractor(),
					func(ctx context.Context, token string) (gqlauth.UserInfo, error) {
						return gqlauth.UserInfo{
							ID:       "demo-user",
							Username: "demo",
							Token:    token,
						}, nil
					},
				),
				// DataLoader: 每请求新实例
				graph.NewLoadersMiddleware(store),
			),
			gql.WithExtension(complexity.Extension(complexity.Config{
				MaxComplexity: 200,
				MaxDepth:      10,
				OnReject: func(ctx context.Context, stats complexity.Stats) {
					slog.Warn("graphql query rejected",
						"complexity", stats.Complexity,
						"depth", stats.Depth,
						"reason", stats.Reason,
					)
				},
			})),
			gql.WithTransport(subscription.WSTransport()),
			gql.WithTransport(subscription.SSETransport()),
		)),
	)

	slog.Info("graphql-bff ready",
		"playground", "http://localhost:8080/",
		"endpoint", "http://localhost:8080/query",
	)
	if err := app.Start(context.Background()); err != nil {
		slog.Error("app exited", "err", err)
	}
}
