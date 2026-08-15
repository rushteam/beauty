package graph

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/rushteam/beauty/contrib/graphql/dataloader"
)

const userLoaderKey = "users"

// NewLoadersMiddleware 每请求创建新的 DataLoader 实例,避免跨请求缓存污染。
func NewLoadersMiddleware(store *Store) func(http.Handler) http.Handler {
	return dataloader.Middleware(func() *dataloader.Registry {
		reg := &dataloader.Registry{}
		dataloader.Register(reg, userLoaderKey, dataloader.NewLoader(
			func(ctx context.Context, ids []string) (map[string]*User, error) {
				slog.Info("dataloader: batch users", "ids", ids)
				return store.GetUsersByIDs(ids), nil
			},
			dataloader.WithBatchSize(50),
			dataloader.WithBatchWait(2*time.Millisecond),
		))
		return reg
	})
}

func loadUser(ctx context.Context, store *Store, id string) (*User, error) {
	loader, ok := dataloader.Get[string, *User](ctx, userLoaderKey)
	if !ok {
		return store.GetUser(id), nil
	}
	return loader.Load(ctx, id)
}
