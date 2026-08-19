package signals

import (
	"context"
	"time"
)

// defaultDetachTimeout 是 DetachTimeout 的默认超时时间。
const defaultDetachTimeout = 5 * time.Second

// DetachTimeout 创建一个与 parent 脱钩的、带超时的 context。
// 用于优雅关闭阶段：parent 已取消但仍需完成收尾操作（如刷日志、注销注册、
// 发送最后一条消息等），同时用 timeout 防止收尾挂死。
//
// 底层使用 context.WithoutCancel 保留 parent 的 Values（logger、trace 等），
// 但不继承 parent 的 Done 信号。
//
//	// shutdown handler 内：
//	ctx, cancel := signals.DetachTimeout(ctx, 3*time.Second)
//	defer cancel()
//	registry.Deregister(ctx)
func DetachTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		timeout = defaultDetachTimeout
	}
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}
