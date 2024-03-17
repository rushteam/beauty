package discover

import (
	"context"
	"io"
)

// func BuildTarget(scheme string, endpoints []string, key string) string {
// 	return fmt.Sprintf("%s://%s/%s", scheme, strings.Join(endpoints, ","), key)
// }

// type baseResolver struct{}

// func (baseResolver) ResolveNow(resolver.ResolveNowOptions) {}

// func (baseResolver) Close() {}

type Discovery interface {
	Find(ctx context.Context, serviceName string) ([]Service, error)
	Watch(ctx context.Context, serviceName string) (Watcher, error)
}

type Watcher interface {
	Next() ([]*Service, error)
	io.Closer
}
