// Package elasticsearch 是 beauty 的 Elasticsearch 集成,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/elasticsearch),不进 beauty 核心依赖图。薄封装官方
// go-elasticsearch/v8:按 beauty 约定建客户端,并给出健康探测 / 搜索 / 写入的便捷方法。
// 不 import beauty 核心,可脱离框架单用。
//
// 边界(机制而非策略):索引 mapping、查询 DSL、聚合、分页都在使用方——本模块只负责把官方
// 客户端接好并暴露原始 JSON,不发明查询构造器。要更强类型可直接用 Client.ES() 拿底层客户端。
//
// 注意:端到端需要真实 ES 集群;本模块单测用 httptest 打桩覆盖 Ping/Search/Index 的请求与
// 响应处理,集群互操作请在具备 ES 的环境验证。
package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/elastic/go-elasticsearch/v8"
)

// Config 是连接配置。
type Config struct {
	Addresses []string // ES 节点地址,如 []string{"http://127.0.0.1:9200"}
	Username  string   // 基础认证用户名(可空)
	Password  string   // 基础认证密码(可空)
	APIKey    string   // API Key 认证(可空,与用户名密码二选一)
	CloudID   string   // Elastic Cloud ID(可空)
}

// Client 包一层官方 *elasticsearch.Client,补健康/搜索/写入便捷方法。
type Client struct {
	es *elasticsearch.Client
}

// New 按 Config 建客户端。
func New(cfg Config) (*Client, error) {
	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
		APIKey:    cfg.APIKey,
		CloudID:   cfg.CloudID,
	})
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: new client: %w", err)
	}
	return &Client{es: es}, nil
}

// ES 返回底层官方客户端,供需要完整 API(bulk、aggregations、typed client 等)时使用。
func (c *Client) ES() *elasticsearch.Client { return c.es }

// Ping 探测集群可用性(可用于健康检查)。
func (c *Client) Ping(ctx context.Context) error {
	res, err := c.es.Info(c.es.Info.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("elasticsearch: ping: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch: ping status %s", res.Status())
	}
	return nil
}

// Search 对 index 执行查询(query 是完整的搜索请求体 JSON,如 {"query":{...}}),返回原始响应 JSON。
func (c *Client) Search(ctx context.Context, index string, query []byte) (json.RawMessage, error) {
	res, err := c.es.Search(
		c.es.Search.WithContext(ctx),
		c.es.Search.WithIndex(index),
		c.es.Search.WithBody(bytes.NewReader(query)),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: search %s: %w", index, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: read search response: %w", err)
	}
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch: search %s status %s: %s", index, res.Status(), body)
	}
	return body, nil
}

// Index 写入(或按 docID 覆盖)一个文档。docID 为空则由 ES 生成 ID。
func (c *Client) Index(ctx context.Context, index, docID string, body []byte) error {
	res, err := c.es.Index(
		index,
		bytes.NewReader(body),
		c.es.Index.WithContext(ctx),
		c.es.Index.WithDocumentID(docID),
	)
	if err != nil {
		return fmt.Errorf("elasticsearch: index %s: %w", index, err)
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("elasticsearch: index %s status %s: %s", index, res.Status(), b)
	}
	return nil
}
