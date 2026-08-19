package mysql

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/store/dlock"
)

func init() {
	dlock.RegisterLocker("mysql", func(u *url.URL) (dlock.Locker, error) { return newDLockFromURL(u) })
	dlock.RegisterElector("mysql", func(u *url.URL) (dlock.Elector, error) { return newDLockFromURL(u) })
}

// newDLockFromURL 从 URL 构造 DLock。
//
// 格式：mysql://user:pass@host:3306/dbname?charset=utf8mb4&prefix=beauty:dlock:&retry=2s&heartbeat=5s
//
// prefix / retry / heartbeat 是 DLock 自定义参数，剥离后其余部分转为 go-sql-driver/mysql
// DSN 格式（user:pass@tcp(host:port)/dbname?params）。
// 用户需自行 import MySQL 驱动（如 _ "github.com/go-sql-driver/mysql"）。
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

	dsn := urlToMySQLDSN(u, q)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql dlock: open: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	return NewDLock(db, opts...), nil
}

// urlToMySQLDSN 将 URL 转为 go-sql-driver/mysql DSN 格式。
// mysql://user:pass@host:3306/dbname?params → user:pass@tcp(host:3306)/dbname?params
func urlToMySQLDSN(u *url.URL, q url.Values) string {
	var b strings.Builder

	if u.User != nil {
		b.WriteString(u.User.String())
		b.WriteByte('@')
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "3306"
	}
	fmt.Fprintf(&b, "tcp(%s:%s)", host, port)

	b.WriteString(u.Path) // /dbname

	if params := q.Encode(); params != "" {
		b.WriteByte('?')
		b.WriteString(params)
	}

	return b.String()
}
