package redis

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/dlock"
)

func init() {
	dlock.RegisterLocker("redis", func(u *url.URL) (dlock.Locker, error) { return newDLockFromURL(u) })
	dlock.RegisterElector("redis", func(u *url.URL) (dlock.Elector, error) { return newDLockFromURL(u) })
}

// newDLockFromURL 从 URL 构造 redis DLock(同时满足 Locker/Elector)。
// 格式:redis://[:password@]host:port/db?ttl=15s&retry=100ms&prefix=beauty:dlock:
// db 取自 URL path(省略则为 0)。构造时会 Ping 校验可达性。
func newDLockFromURL(u *url.URL) (*DLock, error) {
	c := configFromURL(u)
	q := u.Query()
	var opts []DLockOption
	if p := q.Get("prefix"); p != "" {
		opts = append(opts, WithKeyPrefix(p))
	}
	if ttl := q.Get("ttl"); ttl != "" {
		if d, err := time.ParseDuration(ttl); err == nil {
			opts = append(opts, WithTTL(d))
		}
	}
	if retry := q.Get("retry"); retry != "" {
		if d, err := time.ParseDuration(retry); err == nil {
			opts = append(opts, WithRetryInterval(d))
		}
	}
	return NewDLockFromConfig(c, opts...)
}

// configFromURL 把 redis:// URL 解析为 Config(Addr / Password / DB)。
func configFromURL(u *url.URL) *Config {
	c := &Config{Addr: u.Host}
	if u.User != nil {
		c.Password, _ = u.User.Password()
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		if n, err := strconv.Atoi(db); err == nil {
			c.DB = n
		}
	}
	return c
}
