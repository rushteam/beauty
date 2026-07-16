package etcd

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/conf"
	"github.com/rushteam/beauty/pkg/dlock"
)

func init() {
	conf.RegisterFactory("etcd", newConfigCenterFromURL)
	conf.RegisterFactory("etcdv3", newConfigCenterFromURL)
	for _, s := range []string{"etcd", "etcdv3"} {
		dlock.RegisterLocker(s, func(u *url.URL) (dlock.Locker, error) { return newDLockFromURL(u) })
		dlock.RegisterElector(s, func(u *url.URL) (dlock.Elector, error) { return newDLockFromURL(u) })
	}
}

// newDLockFromURL 从 URL 构造 etcd DLock(同时满足 Locker/Elector)。
// 格式:etcd://[user:pass@]host1,host2/?ttl=10s&prefix=/beauty/dlock/&dial_ms=3000
func newDLockFromURL(u *url.URL) (*DLock, error) {
	c := &Config{Endpoints: strings.Split(u.Host, ","), DialMS: 3000}
	if u.User != nil {
		c.Username = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	q := u.Query()
	if ms := q.Get("dial_ms"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil {
			c.DialMS = v
		}
	}
	var opts []DLockOption
	if p := q.Get("prefix"); p != "" {
		opts = append(opts, WithKeyPrefix(p))
	}
	if ttl := q.Get("ttl"); ttl != "" {
		if d, err := time.ParseDuration(ttl); err == nil {
			opts = append(opts, WithSessionTTL(int(d/time.Second)))
		}
	}
	return NewDLockFromConfig(c, opts...)
}

// newConfigCenterFromURL 从 URL 构造 etcd ConfigCenter。
// 格式：etcd://[user:pass@]host1,host2/key?dial_ms=3000
func newConfigCenterFromURL(u *url.URL) (conf.ConfigCenter, error) {
	c := &Config{
		Endpoints: strings.Split(u.Host, ","),
		DialMS:    3000,
	}
	if u.User != nil {
		c.Username = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	if ms := u.Query().Get("dial_ms"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil {
			c.DialMS = v
		}
	}
	return NewConfigCenter(c)
}
