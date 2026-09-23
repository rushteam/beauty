package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// DefaultIsFailure 判定是否计入熔断失败统计。
// 覆盖:锁等待超时(1205)、死锁(1213)、查询 deadline、连接类错误。
// 不含 context.Canceled(多为上游取消,非 DB 自身故障)。
func DefaultIsFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errCircuitOpen) || errors.Is(err, ErrPoolSaturated) || errors.Is(err, ErrConcurrencyLimited) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, sql.ErrConnDone) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "lock wait timeout") ||
		strings.Contains(msg, "error 1205") ||
		strings.Contains(msg, "deadlock") ||
		strings.Contains(msg, "error 1213") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "too many connections") {
		return true
	}
	return false
}
