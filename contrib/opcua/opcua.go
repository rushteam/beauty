// Package opcua 是基于 gopcua/opcua 的 OPC-UA 客户端,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/opcua),不进 beauty 核心依赖图。面向工业 IoT
// 场景——PLC/SCADA/DCS 数据采集与控制。
//
// 提供三种使用模式:
//   - Client — OPC-UA 客户端,直接 Read/Write 节点(命令式);
//   - Subscriber — 基于 OPC-UA Subscription(MonitoredItems)的值变化通知,
//     实现 mq.Subscriber 接口(topic = NodeID 字符串);
//   - Poller — 周期性轮询读取一组节点(类似 Modbus Collector)。
//
// 三者都可作为 beauty.Service 运行,生命周期绑定 ctx。
package opcua

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/monitor"
	"github.com/gopcua/opcua/ua"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

// === Client ===

// Option 配置 Client。
type Option func(*clientConfig)

type clientConfig struct {
	securityPolicy string
	securityMode   ua.MessageSecurityMode
	certFile       string
	keyFile        string
	username       string
	password       string
	opcuaOpts      []opcua.Option
}

// WithSecurityPolicy 设置安全策略(如 "None", "Basic256Sha256")。
func WithSecurityPolicy(policy string) Option {
	return func(c *clientConfig) { c.securityPolicy = policy }
}

// WithSecurityMode 设置消息安全模式。
func WithSecurityMode(mode ua.MessageSecurityMode) Option {
	return func(c *clientConfig) { c.securityMode = mode }
}

// WithCertificate 设置 X.509 证书(PEM 文件路径)。
func WithCertificate(certFile, keyFile string) Option {
	return func(c *clientConfig) { c.certFile = certFile; c.keyFile = keyFile }
}

// WithUserPassword 设置用户名/密码认证。
func WithUserPassword(user, pass string) Option {
	return func(c *clientConfig) { c.username = user; c.password = pass }
}

// WithOPCUAOptions 透传任意 gopcua Option。
func WithOPCUAOptions(opts ...opcua.Option) Option {
	return func(c *clientConfig) { c.opcuaOpts = append(c.opcuaOpts, opts...) }
}

// Client 封装 gopcua 连接,实现 beauty.Service。
type Client struct {
	endpoint string
	oc       *opcua.Client
	ready    chan struct{}
	cfg      clientConfig
}

// NewClient 创建 OPC-UA 客户端(不立即连接)。endpoint 格式: "opc.tcp://host:4840"。
func NewClient(endpoint string, opts ...Option) *Client {
	cfg := clientConfig{
		securityPolicy: "None",
		securityMode:   ua.MessageSecurityModeNone,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Client{
		endpoint: endpoint,
		cfg:      cfg,
		ready:    make(chan struct{}),
	}
}

// Connect 建立连接。
func (c *Client) Connect(ctx context.Context) error {
	opcOpts := []opcua.Option{
		opcua.SecurityPolicy(c.cfg.securityPolicy),
		opcua.SecurityMode(c.cfg.securityMode),
	}
	if c.cfg.certFile != "" {
		opcOpts = append(opcOpts, opcua.CertificateFile(c.cfg.certFile), opcua.PrivateKeyFile(c.cfg.keyFile))
	}
	if c.cfg.username != "" {
		opcOpts = append(opcOpts, opcua.AuthUsername(c.cfg.username, c.cfg.password))
	}
	opcOpts = append(opcOpts, c.cfg.opcuaOpts...)

	oc, err := opcua.NewClient(c.endpoint, opcOpts...)
	if err != nil {
		return fmt.Errorf("opcua: new client: %w", err)
	}
	if err := oc.Connect(ctx); err != nil {
		return fmt.Errorf("opcua: connect %s: %w", c.endpoint, err)
	}
	c.oc = oc
	return nil
}

// Start 连接并保活直到 ctx 取消。满足 beauty.Service。
func (c *Client) Start(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	close(c.ready)
	slog.Info("opcua client connected", "endpoint", c.endpoint)
	<-ctx.Done()
	if err := c.oc.Close(context.Background()); err != nil {
		slog.Warn("opcua: close error", "err", err)
	}
	slog.Info("opcua client disconnected")
	return nil
}

// Ready 满足 beauty.ReadyNotifier。
func (c *Client) Ready() <-chan struct{} { return c.ready }

// String 满足 beauty.Service。
func (c *Client) String() string { return "opcua.Client(" + c.endpoint + ")" }

// OPC 返回底层 gopcua Client,供高级操作使用。
func (c *Client) OPC() *opcua.Client { return c.oc }

// NodeValue 表示一个节点的读取结果。
type NodeValue struct {
	NodeID    string
	Value     any
	Status    ua.StatusCode
	Timestamp time.Time
}

// Read 批量读取节点值。
func (c *Client) Read(ctx context.Context, nodeIDs ...string) ([]NodeValue, error) {
	if c.oc == nil {
		return nil, fmt.Errorf("opcua: not connected")
	}
	req := &ua.ReadRequest{
		MaxAge:             2000,
		TimestampsToReturn: ua.TimestampsToReturnBoth,
		NodesToRead:        make([]*ua.ReadValueID, len(nodeIDs)),
	}
	for i, nid := range nodeIDs {
		id, err := ua.ParseNodeID(nid)
		if err != nil {
			return nil, fmt.Errorf("opcua: parse node id %q: %w", nid, err)
		}
		req.NodesToRead[i] = &ua.ReadValueID{NodeID: id}
	}

	resp, err := c.oc.Read(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("opcua: read: %w", err)
	}

	results := make([]NodeValue, len(resp.Results))
	for i, r := range resp.Results {
		nv := NodeValue{
			NodeID:    nodeIDs[i],
			Status:    r.Status,
			Timestamp: r.SourceTimestamp,
		}
		if r.Value != nil {
			nv.Value = r.Value.Value()
		}
		results[i] = nv
	}
	return results, nil
}

// Write 写入单个节点值。
func (c *Client) Write(ctx context.Context, nodeID string, value any) error {
	if c.oc == nil {
		return fmt.Errorf("opcua: not connected")
	}
	id, err := ua.ParseNodeID(nodeID)
	if err != nil {
		return fmt.Errorf("opcua: parse node id %q: %w", nodeID, err)
	}
	v, err := ua.NewVariant(value)
	if err != nil {
		return fmt.Errorf("opcua: new variant: %w", err)
	}
	req := &ua.WriteRequest{
		NodesToWrite: []*ua.WriteValue{{
			NodeID:      id,
			AttributeID: ua.AttributeIDValue,
			Value: &ua.DataValue{
				EncodingMask: ua.DataValueValue,
				Value:        v,
			},
		}},
	}
	resp, err := c.oc.Write(ctx, req)
	if err != nil {
		return fmt.Errorf("opcua: write: %w", err)
	}
	if len(resp.Results) > 0 && resp.Results[0] != ua.StatusOK {
		return fmt.Errorf("opcua: write status: %s", resp.Results[0])
	}
	return nil
}

// === Subscriber (OPC-UA Subscription → mq.Subscriber) ===

// Subscriber 基于 OPC-UA MonitoredItems 实现 mq.Subscriber。
// topic 为 NodeID 字符串(如 "ns=2;s=Temperature")。
type Subscriber struct {
	client   *Client
	interval time.Duration
}

var _ mq.Subscriber = (*Subscriber)(nil)

// SubscriberOption 配置 Subscriber。
type SubscriberOption func(*Subscriber)

// WithSubscriptionInterval 设置 OPC-UA Subscription 的发布间隔(默认 500ms)。
func WithSubscriptionInterval(d time.Duration) SubscriberOption {
	return func(s *Subscriber) { s.interval = d }
}

// NewSubscriber 创建 OPC-UA 订阅者。client 必须已连接。
func NewSubscriber(client *Client, opts ...SubscriberOption) *Subscriber {
	s := &Subscriber{client: client, interval: 500 * time.Millisecond}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Subscribe 为 NodeID 创建 MonitoredItem,值变化时调用 handler。
// msg.Topic = NodeID, msg.Body = fmt.Sprintf("%v", value)。
func (s *Subscriber) Subscribe(ctx context.Context, topic string, h mq.Handler, opts ...mq.SubscribeOption) error {
	if s.client.oc == nil {
		return fmt.Errorf("opcua: client not connected")
	}

	m, err := monitor.NewNodeMonitor(s.client.oc)
	if err != nil {
		return fmt.Errorf("opcua: new monitor: %w", err)
	}

	ch := make(chan *monitor.DataChangeMessage, 16)
	sub, err := m.ChanSubscribe(ctx, &opcua.SubscriptionParameters{Interval: s.interval}, ch, topic)
	if err != nil {
		return fmt.Errorf("opcua: subscribe %s: %w", topic, err)
	}

	go func() {
		defer sub.Unsubscribe(context.Background())
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if msg.Error != nil {
					slog.Debug("opcua: monitor error", "node", topic, "err", msg.Error)
					continue
				}
				var body []byte
				if msg.DataValue != nil && msg.DataValue.Value != nil {
					body = []byte(fmt.Sprintf("%v", msg.DataValue.Value.Value()))
				}
				mqMsg := mq.Message{
					Topic: topic,
					Body:  body,
					Headers: map[string]string{
						"opcua-status":    fmt.Sprintf("%d", msg.DataValue.Status),
						"opcua-timestamp": msg.DataValue.SourceTimestamp.Format(time.RFC3339Nano),
					},
				}
				if err := h(ctx, mqMsg); err != nil && ctx.Err() == nil {
					slog.Debug("opcua: handler error", "node", topic, "err", err)
				}
			}
		}
	}()
	return nil
}

// === Poller ===

// PollConfig 配置轮询参数。
type PollConfig struct {
	NodeIDs      []string
	PollInterval time.Duration
}

// PollHandler 处理轮询结果。
type PollHandler func(ctx context.Context, values []NodeValue) error

// Poller 周期性读取一组 OPC-UA 节点。满足 beauty.Service。
type Poller struct {
	client  *Client
	cfg     PollConfig
	handler PollHandler
	ready   chan struct{}
}

// NewPoller 创建轮询器。client 应先通过 beauty.WithService 启动或手动 Connect。
func NewPoller(client *Client, cfg PollConfig, handler PollHandler) *Poller {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	return &Poller{client: client, cfg: cfg, handler: handler, ready: make(chan struct{})}
}

// Start 等待 client 就绪后开始周期轮询,ctx 取消时停止。
func (p *Poller) Start(ctx context.Context) error {
	// 等待客户端就绪
	select {
	case <-p.client.Ready():
	case <-ctx.Done():
		return ctx.Err()
	}

	close(p.ready)
	slog.Info("opcua poller started", "nodes", len(p.cfg.NodeIDs), "interval", p.cfg.PollInterval)

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("opcua poller stopped")
			return nil
		case <-ticker.C:
			values, err := p.client.Read(ctx, p.cfg.NodeIDs...)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				slog.Warn("opcua: poll read failed", "err", err)
				continue
			}
			if err := p.handler(ctx, values); err != nil {
				slog.Warn("opcua: poll handler error", "err", err)
			}
		}
	}
}

// Ready 满足 beauty.ReadyNotifier。
func (p *Poller) Ready() <-chan struct{} { return p.ready }

// String 满足 beauty.Service。
func (p *Poller) String() string { return "opcua.Poller" }

// === MQBridge ===

// MQBridge 将轮询结果桥接到 mq.Publisher。
func MQBridge(pub mq.Publisher, topicFn func(NodeValue) string) PollHandler {
	return func(ctx context.Context, values []NodeValue) error {
		for _, v := range values {
			body := []byte(fmt.Sprintf("%v", v.Value))
			msg := mq.Message{
				Topic: topicFn(v),
				Key:   v.NodeID,
				Body:  body,
				Headers: map[string]string{
					"opcua-status":    fmt.Sprintf("%d", v.Status),
					"opcua-timestamp": v.Timestamp.Format(time.RFC3339Nano),
				},
			}
			if err := pub.Publish(ctx, msg); err != nil {
				return err
			}
		}
		return nil
	}
}

// === BatchHandler ===

// BatchCollect 收集多个 Poller 的结果到 channel(用于流式处理)。
func BatchCollect(ch chan<- []NodeValue) PollHandler {
	return func(_ context.Context, values []NodeValue) error {
		ch <- values
		return nil
	}
}
