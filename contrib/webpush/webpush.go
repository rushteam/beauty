// Package webpush 提供 Web Push（VAPID）订阅生命周期与消息发送。
//
// 面向 PWA：浏览器 PushManager.subscribe 后把订阅 JSON POST 到本包 Handler；
// 业务侧用 Client.Send 向用户设备推送。机制、不政策——鉴权与用户绑定由调用方注入。
//
// 依赖 github.com/SherClockHolmes/webpush-go（RFC 8291 / VAPID）。
package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	webpushlib "github.com/SherClockHolmes/webpush-go"
)

// Subscription 是浏览器 PushSubscription 的 JSON 形态。
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     Keys   `json:"keys"`
}

// Keys 是订阅的加密密钥。
type Keys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// Store 持久化用户的推送订阅。实现方对接内存 / Redis / DB。
type Store interface {
	// Save 保存（或覆盖）一条订阅，userID 可为 ""（匿名设备）。
	Save(ctx context.Context, userID string, sub Subscription) error
	// Delete 按 endpoint 删除订阅。
	Delete(ctx context.Context, endpoint string) error
	// List 列出某用户的全部订阅。
	List(ctx context.Context, userID string) ([]Subscription, error)
}

// MemStore 是进程内 Store，适合开发与单机。
type MemStore struct {
	mu   sync.RWMutex
	byEP map[string]entry // endpoint -> entry
}

type entry struct {
	userID string
	sub    Subscription
}

// NewMemStore 创建内存订阅存储。
func NewMemStore() *MemStore {
	return &MemStore{byEP: make(map[string]entry)}
}

func (s *MemStore) Save(_ context.Context, userID string, sub Subscription) error {
	if sub.Endpoint == "" {
		return errors.New("webpush: empty endpoint")
	}
	s.mu.Lock()
	s.byEP[sub.Endpoint] = entry{userID: userID, sub: sub}
	s.mu.Unlock()
	return nil
}

func (s *MemStore) Delete(_ context.Context, endpoint string) error {
	s.mu.Lock()
	delete(s.byEP, endpoint)
	s.mu.Unlock()
	return nil
}

func (s *MemStore) List(_ context.Context, userID string) ([]Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Subscription
	for _, e := range s.byEP {
		if e.userID == userID {
			out = append(out, e.sub)
		}
	}
	return out, nil
}

// VAPIDKeys 持有 VAPID 密钥对（base64url 编码，与 webpush-go 一致）。
type VAPIDKeys struct {
	Public  string
	Private string
}

// GenerateVAPIDKeys 生成一对新的 VAPID 密钥。
func GenerateVAPIDKeys() (VAPIDKeys, error) {
	priv, pub, err := webpushlib.GenerateVAPIDKeys()
	if err != nil {
		return VAPIDKeys{}, err
	}
	return VAPIDKeys{Public: pub, Private: priv}, nil
}

// Client 发送 Web Push 消息。
type Client struct {
	keys    VAPIDKeys
	subject string // mailto: or https: contact
	ttl     time.Duration
	store   Store
}

// ClientOption 配置 Client。
type ClientOption func(*Client)

// WithTTL 设置推送消息 TTL（默认 60s）。
func WithTTL(d time.Duration) ClientOption {
	return func(c *Client) { c.ttl = d }
}

// WithStore 绑定订阅存储（SendToUser 需要）。
func WithStore(s Store) ClientOption {
	return func(c *Client) { c.store = s }
}

// NewClient 创建推送客户端。subject 形如 "mailto:ops@example.com"。
func NewClient(keys VAPIDKeys, subject string, opts ...ClientOption) *Client {
	c := &Client{keys: keys, subject: subject, ttl: 60 * time.Second}
	for _, o := range opts {
		o(c)
	}
	return c
}

// PublicKey 返回 VAPID 公钥（给浏览器 applicationServerKey）。
func (c *Client) PublicKey() string { return c.keys.Public }

// Send 向单个订阅推送 payload（通常为 JSON 字符串）。
func (c *Client) Send(ctx context.Context, sub Subscription, payload []byte) error {
	s := &webpushlib.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpushlib.Keys{
			P256dh: sub.Keys.P256dh,
			Auth:   sub.Keys.Auth,
		},
	}
	resp, err := webpushlib.SendNotificationWithContext(ctx, payload, s, &webpushlib.Options{
		Subscriber:      c.subject,
		VAPIDPublicKey:  c.keys.Public,
		VAPIDPrivateKey: c.keys.Private,
		TTL:             int(c.ttl.Seconds()),
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
		if c.store != nil {
			_ = c.store.Delete(ctx, sub.Endpoint)
		}
		return fmt.Errorf("webpush: subscription expired (%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webpush: status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// SendToUser 向某用户的全部订阅推送；单个失败不阻断其它。
func (c *Client) SendToUser(ctx context.Context, userID string, payload []byte) error {
	if c.store == nil {
		return errors.New("webpush: store not configured")
	}
	subs, err := c.store.List(ctx, userID)
	if err != nil {
		return err
	}
	var first error
	for _, sub := range subs {
		if err := c.Send(ctx, sub, payload); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// SubscribeHandler 返回订阅/退订 HTTP 处理器。
//
//	POST /push/subscribe   body=Subscription JSON，可选 ?user=u1
//	DELETE /push/subscribe body={"endpoint":"..."} 或 ?endpoint=
func SubscribeHandler(store Store, userFromRequest func(*http.Request) string) http.Handler {
	if userFromRequest == nil {
		userFromRequest = func(r *http.Request) string { return r.URL.Query().Get("user") }
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var sub Subscription
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&sub); err != nil {
				http.Error(w, "invalid subscription", http.StatusBadRequest)
				return
			}
			if err := store.Save(r.Context(), userFromRequest(r), sub); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			ep := r.URL.Query().Get("endpoint")
			if ep == "" {
				var body struct {
					Endpoint string `json:"endpoint"`
				}
				_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
				ep = body.Endpoint
			}
			if ep == "" {
				http.Error(w, "missing endpoint", http.StatusBadRequest)
				return
			}
			_ = store.Delete(r.Context(), ep)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// VAPIDPublicHandler 返回 GET 公钥的简单 handler（前端注册 SW 时用）。
func VAPIDPublicHandler(c *Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"publicKey": c.PublicKey()})
	})
}
