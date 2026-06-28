// Package metadata 提供服务间调用的透传元数据机制。
//
// 设计目标：
//   - MD 是纯 map，与传输层无关，可在 HTTP / gRPC / Cron 之间无缝传递
//   - 键统一小写，与 HTTP/2 header 和 gRPC metadata 的规范一致
//   - 预定义常用键，但不强制使用，业务可自由扩展
//
// 典型用法：
//
//	// 服务端（从传输层读取并注入 context）
//	ctx = metadata.NewContext(ctx, md)
//
//	// 业务代码（读取）
//	md := metadata.FromContext(ctx)
//	tenantID := md.Get(metadata.KeyTenantID)
//
//	// 客户端（从 context 写入传输层）
//	md := metadata.FromContext(ctx)
//	md.Inject(req.Header)   // HTTP
//	md.InjectGRPC(ctx)      // gRPC outgoing
package metadata

import (
	"context"
	"maps"
	"strings"

	"github.com/rushteam/beauty/pkg/ctxkey"
)

// 预定义的透传键，与 HTTP Header 名称一致（小写）。
// 业务可直接用字符串字面量扩展，不必局限于此。
const (
	KeyTenantID   = "x-tenant-id"   // 租户 ID，多租户场景必传
	KeyCaller     = "x-caller"      // 调用方服务名，链路追踪辅助
	KeyEnv        = "x-env"         // 环境标（prod/staging/dev），灰度路由
	KeyRequestID  = "x-request-id"  // 请求 ID，与 requestid 中间件共享键名
	KeyUserID     = "x-user-id"     // 当前用户 ID，鉴权后透传
)

var mdKey = ctxkey.New[MD]()

// MD 是服务间透传的元数据集合，键统一小写。
// 零值可直接使用。
type MD map[string]string

// New 创建空 MD。
func New() MD { return make(MD) }

// Get 返回键对应的值；键不存在时返回空字符串。
func (m MD) Get(key string) string {
	return m[strings.ToLower(key)]
}

// Set 设置键值对，键自动转小写。
func (m MD) Set(key, value string) {
	m[strings.ToLower(key)] = value
}

// Del 删除键。
func (m MD) Del(key string) {
	delete(m, strings.ToLower(key))
}

// Clone 返回当前 MD 的深拷贝。
func (m MD) Clone() MD {
	return maps.Clone(m)
}

// Merge 将 other 中的键值合并到 m，重复键以 other 为准。
func (m MD) Merge(other MD) {
	maps.Copy(m, other)
}

// NewContext 将 md 附加到 ctx 并返回新 ctx。
// 若 ctx 中已有 MD，新 MD 会与原有 MD 合并（新值覆盖旧值）。
func NewContext(ctx context.Context, md MD) context.Context {
	existing := FromContext(ctx)
	if len(existing) == 0 {
		return ctxkey.With(ctx, mdKey, md.Clone())
	}
	merged := existing.Clone()
	merged.Merge(md)
	return ctxkey.With(ctx, mdKey, merged)
}

// FromContext 从 ctx 中取出 MD；若不存在返回空 MD（非 nil，可直接读写）。
func FromContext(ctx context.Context) MD {
	if md, ok := ctxkey.Get(ctx, mdKey); ok {
		return md
	}
	return MD{}
}
