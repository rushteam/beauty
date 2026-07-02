// Package webhook 提供事件驱动的 Webhook 通知：按事件类型过滤、自定义 header、
// 可选 body 模板、可选 HMAC 签名，异步触发并带指数退避重试。
//
// 可靠投递增强(可选,通过 WithStore/WithDLQ 启用):
//   - 幂等去重:Event.EventID 非空时,同一 (endpoint, eventID) 只投递一次;
//   - 投递状态追踪:Store 记录每次投递的最终状态(delivered/failed);
//   - DLQ:重试耗尽后投递入死信队列,供后续重放(Replay)。
//
// at-least-once + 去重语义。
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
	"sync"
	"text/template"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
	"github.com/rushteam/beauty/pkg/safe"
)

// Event 是一次通知的事件：Type 用于端点过滤，Payload 作为默认 body 与模板数据。
// EventID 非空时启用幂等去重(同一 endpoint+EventID 只投递一次)。
type Event struct {
	Type    string
	Payload any
	EventID string // 业务幂等键;为空则不去重
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

// DeliveryStatus 投递最终状态。
type DeliveryStatus string

const (
	StatusDelivered DeliveryStatus = "delivered"
	StatusFailed    DeliveryStatus = "failed"
)

// DeliveryRecord 一次投递的最终记录(成功或重试耗尽后)。
type DeliveryRecord struct {
	EventID     string
	EndpointURL string
	Status      DeliveryStatus
	Attempts    int
	LastErr     string
	At          time.Time
}

// Store 持久化投递状态与幂等去重。实现方对接 Redis/DB/内存。
// 并发安全由实现方保证。
type Store interface {
	// MarkDelivered 标记 (endpoint, eventID) 已成功投递。
	// 返回 true 表示本次标记生效(之前未投递过),false 表示已投递过(应跳过)。
	MarkDelivered(endpointURL, eventID string) bool
	// RecordFailed 记录一次最终失败的投递(用于状态追踪)。
	RecordFailed(rec DeliveryRecord)
	// RecordDelivered 记录一次成功的投递(用于状态追踪,与 MarkDelivered 区别:
	// MarkDelivered 负责去重判断,RecordDelivered 负责状态留痕)。
	RecordDelivered(rec DeliveryRecord)
}

// DLQ 死信队列:收集重试耗尽的投递,供后续重放。
type DLQ interface {
	// Push 入队一条死信。
	Push(rec DeliveryRecord)
	// Pop 取出最早一条死信(无则返回 ok=false)。
	Pop() (DeliveryRecord, bool)
	// Len 当前死信数量。
	Len() int
}

// Notifier 管理一组端点并触发通知。
type Notifier struct {
	client    *http.Client
	endpoints []Endpoint
	retries   int
	backoff   time.Duration
	onError   func(ep Endpoint, ev Event, err error)

	store Store // 可为 nil:不启用去重/状态追踪
	dlq   DLQ   // 可为 nil:失败不入死信
	now   func() time.Time
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

// WithStore 启用幂等去重 + 投递状态追踪。
func WithStore(s Store) Option { return func(n *Notifier) { n.store = s } }

// WithDLQ 启用死信队列:重试耗尽后投递入队,供 Replay 重放。
func WithDLQ(q DLQ) Option { return func(n *Notifier) { n.dlq = q } }

// New 创建一个 Notifier。
func New(opts ...Option) *Notifier {
	n := &Notifier{
		client:  &http.Client{Timeout: 10 * time.Second},
		retries: 2,
		backoff: 200 * time.Millisecond,
		now:     time.Now,
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
// 若启用 Store 且 ev.EventID 非空:同一 (endpoint, eventID) 已投递过则跳过。
func (n *Notifier) Notify(ctx context.Context, ev Event) {
	for _, ep := range n.endpoints {
		if !ep.accepts(ev.Type) {
			continue
		}
		ep := ep
		// 幂等去重:已投递过则跳过。
		if n.store != nil && ev.EventID != "" && !n.store.MarkDelivered(ep.URL, ev.EventID) {
			continue
		}
		safe.Go(func() {
			if err := n.deliver(ctx, ep, ev); err != nil {
				if n.store != nil {
					n.store.RecordFailed(DeliveryRecord{
						EventID: ev.EventID, EndpointURL: ep.URL,
						Status: StatusFailed, Attempts: n.retries + 1,
						LastErr: err.Error(), At: n.now(),
					})
				}
				if n.dlq != nil {
					n.dlq.Push(DeliveryRecord{
						EventID: ev.EventID, EndpointURL: ep.URL,
						Status: StatusFailed, Attempts: n.retries + 1,
						LastErr: err.Error(), At: n.now(),
					})
				}
				if n.onError != nil {
					n.onError(ep, ev, err)
				}
				return
			}
			if n.store != nil {
				n.store.RecordDelivered(DeliveryRecord{
					EventID: ev.EventID, EndpointURL: ep.URL,
					Status: StatusDelivered, Attempts: 1, At: n.now(),
				})
			}
		}, func(err error) {
			if n.onError != nil {
				n.onError(ep, ev, err)
			}
		})
	}
}

// Replay 从 DLQ 取一条死信重新投递。返回 (是否取到, 投递结果)。
// 无 DLQ 时返回 false。
func (n *Notifier) Replay(ctx context.Context) (bool, error) {
	if n.dlq == nil {
		return false, nil
	}
	rec, ok := n.dlq.Pop()
	if !ok {
		return false, nil
	}
	// 找回原 endpoint 重新投递。
	var ep *Endpoint
	for i := range n.endpoints {
		if n.endpoints[i].URL == rec.EndpointURL {
			ep = &n.endpoints[i]
			break
		}
	}
	if ep == nil {
		return true, fmt.Errorf("webhook: replay: endpoint %s no longer registered", rec.EndpointURL)
	}
	// Replay 不再走 Store 去重(死信本身就是去重后的失败重试)。
	// 重建 Event:EventID 保留,Payload 无法还原——replay 需要业务侧自备 Payload 来源。
	// 这里只重发已知字段,业务若需完整 Payload 应自行维护 event 存储。
	ev := Event{EventID: rec.EventID}
	if err := n.deliver(ctx, *ep, ev); err != nil {
		// 重放仍失败:重新入队。
		n.dlq.Push(rec)
		return true, err
	}
	return true, nil
}

func (n *Notifier) deliver(ctx context.Context, ep Endpoint, ev Event) error {
	body, err := renderBody(ep, ev)
	if err != nil {
		return err
	}
	// 退避序列复用 pkg/backoff:base=n.backoff、factor=2、无抖动、不封顶,
	// 与历史行为(delay 每次翻倍)一致。第 attempt 次重试(attempt>=1)等待 Duration(attempt-1)。
	policy := backoff.New(
		backoff.WithBase(n.backoff),
		backoff.WithFactor(2),
		backoff.WithJitter(backoff.JitterNone),
		backoff.WithMax(0),
	)
	var lastErr error
	for attempt := 0; attempt <= n.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(policy.Duration(attempt - 1)):
			}
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

// ---- 内存默认实现 ----

// MemStore 是 Store 的内存实现:用 map 做 (endpoint,eventID) 去重 + 状态记录。
// 适合单进程;多进程需用 Redis/DB 实现 Store 接口。
type MemStore struct {
	mu        sync.Mutex
	delivered map[string]struct{} // "endpoint:eventID"
	records   []DeliveryRecord
}

// NewMemStore 创建空内存 store。
func NewMemStore() *MemStore {
	return &MemStore{delivered: make(map[string]struct{})}
}

func (s *MemStore) key(endpointURL, eventID string) string {
	return endpointURL + ":" + eventID
}

func (s *MemStore) MarkDelivered(endpointURL, eventID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.key(endpointURL, eventID)
	if _, ok := s.delivered[k]; ok {
		return false
	}
	s.delivered[k] = struct{}{}
	return true
}

func (s *MemStore) RecordDelivered(rec DeliveryRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, rec)
}

func (s *MemStore) RecordFailed(rec DeliveryRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, rec)
}

// Records 返回所有投递记录的快照(成功+失败)。
func (s *MemStore) Records() []DeliveryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeliveryRecord, len(s.records))
	copy(out, s.records)
	return out
}

// MemDLQ 是 DLQ 的内存实现:用 slice 做 FIFO 队列。
type MemDLQ struct {
	mu    sync.Mutex
	queue []DeliveryRecord
}

// NewMemDLQ 创建空内存死信队列。
func NewMemDLQ() *MemDLQ { return &MemDLQ{} }

func (q *MemDLQ) Push(rec DeliveryRecord) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queue = append(q.queue, rec)
}

func (q *MemDLQ) Pop() (DeliveryRecord, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.queue) == 0 {
		return DeliveryRecord{}, false
	}
	rec := q.queue[0]
	q.queue = q.queue[1:]
	return rec, true
}

func (q *MemDLQ) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}
