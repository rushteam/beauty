package pg

import (
	"database/sql"
	"fmt"
	"net/url"
	"time"

	"github.com/rushteam/beauty/pkg/store/dlock"
)

func init() {
	dlock.RegisterLocker("postgres", func(u *url.URL) (dlock.Locker, error) { return newDLockFromURL(u) })
	dlock.RegisterLocker("postgresql", func(u *url.URL) (dlock.Locker, error) { return newDLockFromURL(u) })
	dlock.RegisterElector("postgres", func(u *url.URL) (dlock.Elector, error) { return newDLockFromURL(u) })
	dlock.RegisterElector("postgresql", func(u *url.URL) (dlock.Elector, error) { return newDLockFromURL(u) })
}

// newDLockFromURL 从 URL 构造 DLock。
// 格式:postgres://user:pass@host:5432/dbname?sslmode=disable&prefix=beauty/dlock/&retry=2s&heartbeat=5s
//
// prefix / retry / heartbeat 是 DLock 自定义参数,剥离后其余部分作为 PG 连接 DSN。
// 用户需自行 import PG 驱动(如 _ "github.com/lib/pq" 或 _ "github.com/jackc/pgx/v5/stdlib")。
func newDLockFromURL(u *url.URL) (*DLock, error) {
	q := u.Query()

	var opts []DLockOption
	if p := q.Get("prefix"); p != "" {
		opts = append(opts, WithPrefix(p))
		q.Del("prefix")
	}
	if s := q.Get("retry"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts = append(opts, WithRetryInterval(d))
		}
		q.Del("retry")
	}
	if s := q.Get("heartbeat"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			opts = append(opts, WithHeartbeat(d))
		}
		q.Del("heartbeat")
	}

	// 重建 DSN(已剥离自定义参数)。
	u2 := *u
	u2.RawQuery = q.Encode()
	dsn := u2.String()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("pg dlock: open: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	return NewDLock(db, opts...), nil
}
