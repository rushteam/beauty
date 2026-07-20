// Package openfga 用 OpenFGA(Zanzibar 式关系授权 ReBAC)实现 beauty 的 authz.Enforcer,
// 适合"X 是文档 Y 的编辑者"这类细粒度、基于关系的权限。作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/openfga),薄封装官方 openfga/go-sdk,通过 Check API 判定。
//
// 映射(authz → OpenFGA 关系元组):默认 user = "user:"+Subject.ID、relation = action、
// object = resource;可用 WithMapper 自定义(如按类型加前缀 "document:"+id)。授权模型
// (类型定义、关系)与关系元组写入都在 OpenFGA 侧管理——本模块只做 Check。
//
// 需要一个可达的 OpenFGA 服务(ApiUrl + StoreId)。
package openfga

import (
	"context"
	"fmt"

	fga "github.com/openfga/go-sdk/client"

	"github.com/rushteam/beauty/pkg/authz"
)

// Mapper 把授权请求映射成 OpenFGA 的 (user, relation, object) 关系元组。
type Mapper func(sub authz.Subject, action, resource string) (user, relation, object string)

// Enforcer 用 OpenFGA Check 实现 authz.Enforcer。
type Enforcer struct {
	c      *fga.OpenFgaClient
	mapper Mapper
}

// Option 配置 Enforcer。
type Option func(*Enforcer)

// WithMapper 自定义 authz→OpenFGA 元组映射。
func WithMapper(m Mapper) Option { return func(e *Enforcer) { e.mapper = m } }

// New 连接 OpenFGA。apiURL 如 "http://127.0.0.1:8080";storeID 是目标 store 的 ID。
func New(apiURL, storeID string, opts ...Option) (*Enforcer, error) {
	c, err := fga.NewSdkClient(&fga.ClientConfiguration{ApiUrl: apiURL, StoreId: storeID})
	if err != nil {
		return nil, fmt.Errorf("openfga: new client: %w", err)
	}
	e := &Enforcer{c: c, mapper: defaultMapper}
	for _, o := range opts {
		o(e)
	}
	return e, nil
}

var _ authz.Enforcer = (*Enforcer)(nil)

// Client 返回底层 *OpenFgaClient,供写关系元组、管理模型等高级操作。
func (e *Enforcer) Client() *fga.OpenFgaClient { return e.c }

// Authorize 实现 authz.Enforcer:按映射发起一次 Check,allowed 放行,否则 ErrDenied。
func (e *Enforcer) Authorize(ctx context.Context, sub authz.Subject, action, resource string) error {
	user, relation, object := e.mapper(sub, action, resource)
	resp, err := e.c.Check(ctx).Body(fga.ClientCheckRequest{
		User:     user,
		Relation: relation,
		Object:   object,
	}).Execute()
	if err != nil {
		return fmt.Errorf("openfga: check: %w", err)
	}
	if resp.GetAllowed() {
		return nil
	}
	return authz.ErrDenied
}

func defaultMapper(sub authz.Subject, action, resource string) (string, string, string) {
	return "user:" + sub.ID, action, resource
}
