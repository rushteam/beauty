package mdrt

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
)

// hostAllow 是当前一代允许出网的域名并集。
type hostAllow struct {
	hosts map[string]struct{}
}

func allowOf(specs []*Spec) *hostAllow {
	hosts := map[string]struct{}{}
	for _, spec := range specs {
		if spec == nil {
			continue
		}
		for _, h := range spec.HTTPAllow {
			h = strings.ToLower(strings.TrimSpace(h))
			if h != "" {
				hosts[h] = struct{}{}
			}
		}
	}
	return &hostAllow{hosts: hosts}
}

func (a *hostAllow) permits(host string) bool {
	if a == nil {
		return false
	}
	_, ok := a.hosts[strings.ToLower(host)]
	return ok
}

// allowTransport 在进程边界拒绝未声明的域名。
type allowTransport struct {
	base  http.RoundTripper
	allow *atomic.Pointer[hostAllow]
	log   *slog.Logger
}

func (t *allowTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	host := ""
	if r.URL != nil {
		host = r.URL.Hostname()
	}
	var current *hostAllow
	if t.allow != nil {
		current = t.allow.Load()
	}
	if !current.permits(host) {
		if t.log != nil {
			t.log.Warn("mdrt: 拒绝未声明的出网", slog.String("host", host))
		}
		return nil, fmt.Errorf("mdrt: host %q is not allowed", host)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

// stubTransport 按当前例子的桩返回响应,没有桩时返回 599。
type stubTransport struct {
	cur atomic.Pointer[[]Stub]
}

func (t *stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	stubs := t.cur.Load()
	url := ""
	if r.URL != nil {
		url = r.URL.String()
	}
	var list []Stub
	if stubs != nil {
		list = *stubs
	}
	for _, st := range list {
		if st.Method == r.Method && st.URL == url {
			return stubResponse(r, st.Status, st.Body), nil
		}
	}
	return stubResponse(r, 599, "mdrt: no stub"), nil
}

func stubResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       r,
	}
}
