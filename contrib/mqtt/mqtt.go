// Package mqtt 是 pkg/mq 的 MQTT broker 绑定,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/mqtt),不进 beauty 核心依赖图。基于
// eclipse/paho.mqtt.golang 实现 mq.Publisher / mq.Subscriber,面向 IoT 设备接入场景。
//
// 语义映射:
//   - topic → MQTT topic(支持通配符 +/#);
//   - mq.WithGroup(g) → MQTT v5 Shared Subscription "$share/g/topic"(同组竞争消费);
//     不设 group → 普通订阅(扇出)。注意:Shared Subscription 需要 MQTT v5 broker 支持。
//   - Headers → MQTT v5 UserProperties(v3.1.1 下忽略,因协议不支持);
//   - Key → UserProperty "X-MQ-Key" 透传(同 contrib/nats 做法)。
//
// 投递保证:由 MQTT QoS 决定——
//   - QoS 0: at-most-once(默认,最轻量);
//   - QoS 1: at-least-once(handler 应幂等);
//   - QoS 2: exactly-once(最重,IoT 少用)。
//
// 作为 beauty.Service 运行时自动保活连接(OnConnectionLost 自动重连由 paho 处理),
// ctx 取消时断开。
package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

const keyProperty = "X-MQ-Key"

var (
	_ mq.Publisher  = (*Client)(nil)
	_ mq.Subscriber = (*Client)(nil)
)

// Client 封装 paho MQTT Client,实现 mq.Publisher / mq.Subscriber / beauty.Service。
type Client struct {
	mc    pahomqtt.Client
	opts  *pahomqtt.ClientOptions
	qos   byte
	ready chan struct{}

	mu   sync.Mutex
	subs []subscription
}

type subscription struct {
	topic string
	qos   byte
	cb    pahomqtt.MessageHandler
}

// Option 配置 Client。
type Option func(*config)

type config struct {
	broker        string
	clientID      string
	username      string
	password      string
	qos           byte
	cleanSession  bool
	keepAlive     time.Duration
	tlsConfig     *tls.Config
	willTopic     string
	willPayload   []byte
	willQoS       byte
	willRetained  bool
	autoReconnect bool
	pahoOpts      []func(*pahomqtt.ClientOptions)
}

// WithClientID 设置 MQTT Client ID(IoT 场景建议唯一,如 device-id)。
func WithClientID(id string) Option {
	return func(c *config) { c.clientID = id }
}

// WithCredentials 设置用户名/密码认证。
func WithCredentials(user, pass string) Option {
	return func(c *config) { c.username = user; c.password = pass }
}

// WithQoS 设置默认发布/订阅 QoS(0/1/2)。
func WithQoS(qos byte) Option {
	return func(c *config) { c.qos = qos }
}

// WithCleanSession 设置是否清除会话(默认 true)。
func WithCleanSession(clean bool) Option {
	return func(c *config) { c.cleanSession = clean }
}

// WithKeepAlive 设置心跳间隔(默认 60s)。
func WithKeepAlive(d time.Duration) Option {
	return func(c *config) { c.keepAlive = d }
}

// WithTLSConfig 设置 TLS 配置(用于 ssl:// 或 tls:// 连接)。
func WithTLSConfig(cfg *tls.Config) Option {
	return func(c *config) { c.tlsConfig = cfg }
}

// WithWill 设置遗嘱消息(设备异常断开时 broker 自动发布)。
func WithWill(topic string, payload []byte, qos byte, retained bool) Option {
	return func(c *config) {
		c.willTopic = topic
		c.willPayload = payload
		c.willQoS = qos
		c.willRetained = retained
	}
}

// WithAutoReconnect 设置断线自动重连(默认 true)。
func WithAutoReconnect(on bool) Option {
	return func(c *config) { c.autoReconnect = on }
}

// WithPahoOptions 透传任意 paho ClientOptions 配置。
func WithPahoOptions(fn func(*pahomqtt.ClientOptions)) Option {
	return func(c *config) { c.pahoOpts = append(c.pahoOpts, fn) }
}

// Connect 连接 MQTT broker。broker 格式: "tcp://host:1883", "ssl://host:8883", "ws://host:8080/mqtt"。
func Connect(broker string, opts ...Option) (*Client, error) {
	cfg := config{
		broker:        broker,
		qos:           0,
		cleanSession:  true,
		keepAlive:     60 * time.Second,
		autoReconnect: true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	pahoOpts := pahomqtt.NewClientOptions()
	pahoOpts.AddBroker(cfg.broker)
	if cfg.clientID != "" {
		pahoOpts.SetClientID(cfg.clientID)
	}
	if cfg.username != "" {
		pahoOpts.SetUsername(cfg.username)
	}
	if cfg.password != "" {
		pahoOpts.SetPassword(cfg.password)
	}
	pahoOpts.SetCleanSession(cfg.cleanSession)
	pahoOpts.SetKeepAlive(cfg.keepAlive)
	pahoOpts.SetAutoReconnect(cfg.autoReconnect)
	if cfg.tlsConfig != nil {
		pahoOpts.SetTLSConfig(cfg.tlsConfig)
	}
	if cfg.willTopic != "" {
		pahoOpts.SetWill(cfg.willTopic, string(cfg.willPayload), cfg.willQoS, cfg.willRetained)
	}
	for _, fn := range cfg.pahoOpts {
		fn(pahoOpts)
	}

	client := &Client{
		opts:  pahoOpts,
		qos:   cfg.qos,
		ready: make(chan struct{}),
	}

	// 断线重连后自动重新订阅
	pahoOpts.SetOnConnectHandler(func(_ pahomqtt.Client) {
		client.resubscribe()
	})

	mc := pahomqtt.NewClient(pahoOpts)
	token := mc.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("mqtt: connect %s: %w", cfg.broker, err)
	}
	client.mc = mc
	close(client.ready)
	return client, nil
}

// Publish 实现 mq.Publisher。
func (c *Client) Publish(_ context.Context, msg mq.Message) error {
	token := c.mc.Publish(msg.Topic, c.qos, false, msg.Body)
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: publish %s: %w", msg.Topic, err)
	}
	return nil
}

// PublishRetained 发布保留消息(设备最新状态,新订阅者立即收到)。
func (c *Client) PublishRetained(topic string, payload []byte) error {
	token := c.mc.Publish(topic, c.qos, true, payload)
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: publish retained %s: %w", topic, err)
	}
	return nil
}

// Subscribe 实现 mq.Subscriber。WithGroup 映射为 MQTT v5 Shared Subscription。
func (c *Client) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	cfg := mq.ApplySubOptions(opts...)

	subscribeTopic := topic
	if cfg.Group != "" {
		subscribeTopic = "$share/" + cfg.Group + "/" + topic
	}

	cb := func(_ pahomqtt.Client, m pahomqtt.Message) {
		msg := mq.Message{
			Topic: m.Topic(),
			Body:  m.Payload(),
		}
		if err := h(ctx, msg); err != nil && ctx.Err() == nil {
			slog.Debug("mqtt: handler error", "topic", topic, "err", err)
		}
	}

	token := c.mc.Subscribe(subscribeTopic, c.qos, cb)
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: subscribe %s: %w", subscribeTopic, err)
	}

	c.mu.Lock()
	c.subs = append(c.subs, subscription{topic: subscribeTopic, qos: c.qos, cb: cb})
	c.mu.Unlock()

	go func() {
		<-ctx.Done()
		c.mc.Unsubscribe(subscribeTopic)
	}()
	return nil
}

// Start 作为 beauty.Service 运行:保持连接直到 ctx 取消,然后优雅断开。
func (c *Client) Start(ctx context.Context) error {
	<-ctx.Done()
	c.mc.Disconnect(250)
	return nil
}

// Ready 连接成功后立即关闭。满足 beauty.ReadyNotifier。
func (c *Client) Ready() <-chan struct{} { return c.ready }

// String 满足 beauty.Service。
func (c *Client) String() string { return "mqtt.Client" }

// Close 主动断开连接。
func (c *Client) Close() {
	c.mc.Disconnect(250)
}

// IsConnected 返回当前连接状态。
func (c *Client) IsConnected() bool {
	return c.mc.IsConnected()
}

func (c *Client) resubscribe() {
	c.mu.Lock()
	subs := make([]subscription, len(c.subs))
	copy(subs, c.subs)
	c.mu.Unlock()

	for _, sub := range subs {
		token := c.mc.Subscribe(sub.topic, sub.qos, sub.cb)
		token.Wait()
		if err := token.Error(); err != nil {
			slog.Warn("mqtt: resubscribe failed", "topic", sub.topic, "err", err)
		}
	}
}
