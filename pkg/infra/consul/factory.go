package consul

import (
	"net/url"
	"time"

	"github.com/rushteam/beauty/pkg/conf"
	"github.com/rushteam/beauty/pkg/dlock"
)

func init() {
	conf.RegisterFactory("consul", newConfigCenterFromURL)
	dlock.RegisterLocker("consul", func(u *url.URL) (dlock.Locker, error) { return newDLockFromURL(u) })
	dlock.RegisterElector("consul", func(u *url.URL) (dlock.Elector, error) { return newDLockFromURL(u) })
}

// newDLockFromURL 从 URL 构造 consul DLock(同时满足 Locker/Elector)。
// 格式:consul://[:token@]host:port/?ttl=15s&prefix=beauty/dlock/&datacenter=dc1&identity=host-a
func newDLockFromURL(u *url.URL) (*DLock, error) {
	c := &Config{Addr: u.Host}
	if u.User != nil {
		c.Token, _ = u.User.Password()
	}
	q := u.Query()
	c.Datacenter = q.Get("datacenter")
	c.Namespace = q.Get("namespace")
	c.Partition = q.Get("partition")
	var opts []DLockOption
	if p := q.Get("prefix"); p != "" {
		opts = append(opts, WithLockKeyPrefix(p))
	}
	if ttl := q.Get("ttl"); ttl != "" {
		if d, err := time.ParseDuration(ttl); err == nil {
			opts = append(opts, WithLockSessionTTL(d))
		}
	}
	if id := q.Get("identity"); id != "" {
		opts = append(opts, WithLockIdentity(id))
	}
	return NewDLockFromConfig(c, opts...)
}

// newConfigCenterFromURL 从 URL 构造 consul ConfigCenter。
// 格式：consul://[token@]host:port/kv/path?datacenter=dc1&namespace=ns
// token 通过 URL 的 password 字段传入：consul://:mytoken@host:8500/...
func newConfigCenterFromURL(u *url.URL) (conf.ConfigCenter, error) {
	c := &Config{
		Addr: u.Host,
	}
	if u.User != nil {
		c.Token, _ = u.User.Password()
	}
	q := u.Query()
	c.Datacenter = q.Get("datacenter")
	c.Namespace = q.Get("namespace")
	c.Partition = q.Get("partition")
	return NewConfigCenter(c)
}
