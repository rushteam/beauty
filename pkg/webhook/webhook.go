// Package webhook 提供事件驱动的 Webhook 通知：按事件类型过滤、自定义 header、
// 可选 body 模板、可选 HMAC 签名，异步触发并带指数退避重试。
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"text/template"
	"time"

	"github.com/rushteam/beauty/pkg/safe"
)

// Event 是一次通知的事件：Type 用于端点过滤，Payload 作为默认 body 与模板数据。
type Event struct {
	Type    string
	Payload any
}

// Endpoint 描述一个 Webhook 端点。
type Endpoint struct {
	URL string
	// Events 为空表示接收所有事件；否则仅接收 Type 在列表中的事件。
	Events []string
	// Headers 附加请求头。
	Headers map[string]string
	// Secret 非空时对 body 做 HMAC-SHA256 签名，写入 X-Webhook-Signature: sha256=<hex>。
	Secret string
	// BodyTemplate 为空时 body = JSON(Payload)；否则用 text/template 渲染整个 Event
	//（可用 {{.Type}} / {{.Payload.Field}}）。
	BodyTemplate string

	tmpl *template.Template
}

func (e *Endpoint) accepts(eventType string) bool {
	return len(e.Events) == 0 || slices.Contains(e.Events, eventType)
}

// Notifier 管理一组端点并触发通知。
type Notifier struct {
	client    *http.Client
	endpoints []Endpoint
	retries   int
	backoff   time.Duration
	onError   func(ep Endpoint, ev Event, err error)
}

// Option 配置 Notifier。
type Option func(*Notifier)

// WithTimeout 设置单次请求超时，默认 10s。
func WithTimeout(d time.Duration) Option {
	return func(n *Notifier) { n.client = &http.Client{Timeout: d} }
}

// WithRetries 设置失败重试次数（额外次数），默认 2。
func WithRetries(n int) Option {
	return func(no *Notifier) { no.retries = n }
}

// WithBackoff 设置首次重试退避（指数增长），默认 200ms。
func WithBackoff(d time.Duration) Option {
	return func(n *Notifier) { n.backoff = d }
}

// WithErrorHandler 设置最终失败回调（用于日志/告警）。
func WithErrorHandler(fn func(ep Endpoint, ev Event, err error)) Option {
	return func(n *Notifier) { n.onError = fn }
}

// New 创建一个 Notifier。
func New(opts ...Option) *Notifier {
	n := &Notifier{
		client:  &http.Client{Timeout: 10 * time.Second},
		retries: 2,
		backoff: 200 * time.Millisecond,
	}
	for _, o := range opts {
		o(n)
	}
	return n
}

// Add 注册端点；若带 BodyTemplate 会预编译，模板非法则返回错误。
func (n *Notifier) Add(ep Endpoint) error {
	if ep.BodyTemplate != "" {
		t, err := template.New("webhook").Option("missingkey=zero").Parse(ep.BodyTemplate)
		if err != nil {
			return fmt.Errorf("webhook: parse body template for %s: %w", ep.URL, err)
		}
		ep.tmpl = t
	}
	n.endpoints = append(n.endpoints, ep)
	return nil
}

// Notify 异步把 ev 投递给所有匹配的端点（按 Type 过滤），每个端点独立重试。
// 立即返回，不阻塞调用方；失败通过 WithErrorHandler 上报。
func (n *Notifier) Notify(ctx context.Context, ev Event) {
	for _, ep := range n.endpoints {
		if !ep.accepts(ev.Type) {
			continue
		}
		ep := ep
		safe.Go(func() {
			if err := n.deliver(ctx, ep, ev); err != nil && n.onError != nil {
				n.onError(ep, ev, err)
			}
		}, func(err error) {
			if n.onError != nil {
				n.onError(ep, ev, err)
			}
		})
	}
}

func (n *Notifier) deliver(ctx context.Context, ep Endpoint, ev Event) error {
	body, err := renderBody(ep, ev)
	if err != nil {
		return err
	}
	delay := n.backoff
	var lastErr error
	for attempt := 0; attempt <= n.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
		lastErr = n.post(ctx, ep, body)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("webhook: %s failed after %d attempts: %w", ep.URL, n.retries+1, lastErr)
}

func (n *Notifier) post(ctx context.Context, ep Endpoint, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range ep.Headers {
		req.Header.Set(k, v)
	}
	if ep.Secret != "" {
		req.Header.Set("X-Webhook-Signature", sign(ep.Secret, body))
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func renderBody(ep Endpoint, ev Event) ([]byte, error) {
	if ep.tmpl == nil {
		return json.Marshal(ev.Payload)
	}
	var buf bytes.Buffer
	if err := ep.tmpl.Execute(&buf, ev); err != nil {
		return nil, fmt.Errorf("webhook: render body: %w", err)
	}
	return buf.Bytes(), nil
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
