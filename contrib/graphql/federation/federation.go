// Package federation 提供 GraphQL Federation 网关,支持多子图 (subgraph) 合并执行。
// 每个子图可以是 beauty 注册的服务(通过服务发现解析地址)或外部 URL。
package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// SubgraphConfig 配置一个子图。
type SubgraphConfig struct {
	Name    string            // 子图名
	URL     string            // 子图地址(直连)
	Headers map[string]string // 透传给子图的固定 header
}

// GatewayOption 配置网关。
type GatewayOption func(*Gateway)

// WithHTTPClient 设置用于调子图的 HTTP client。
func WithHTTPClient(client *http.Client) GatewayOption {
	return func(g *Gateway) { g.httpClient = client }
}

// WithTimeout 设置子图调用超时(默认 30s)。
func WithTimeout(d time.Duration) GatewayOption {
	return func(g *Gateway) { g.timeout = d }
}

// Gateway 是 Federation 网关,聚合多个子图的 GraphQL 查询。
type Gateway struct {
	subgraphs  []SubgraphConfig
	httpClient *http.Client
	timeout    time.Duration
}

// NewGateway 创建 Federation 网关。
func NewGateway(subgraphs []SubgraphConfig, opts ...GatewayOption) *Gateway {
	g := &Gateway{
		subgraphs: subgraphs,
		timeout:   30 * time.Second,
	}
	for _, o := range opts {
		o(g)
	}
	if g.httpClient == nil {
		g.httpClient = &http.Client{Timeout: g.timeout}
	}
	return g
}

// GraphQLRequest 是发往子图的请求。
type GraphQLRequest struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
	OperationName string                 `json:"operationName,omitempty"`
}

// GraphQLResponse 是子图返回的响应。
type GraphQLResponse struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Errors json.RawMessage `json:"errors,omitempty"`
}

// QuerySubgraph 向指定子图发送查询。
func (g *Gateway) QuerySubgraph(ctx context.Context, name string, req GraphQLRequest) (*GraphQLResponse, error) {
	var target *SubgraphConfig
	for i := range g.subgraphs {
		if g.subgraphs[i].Name == name {
			target = &g.subgraphs[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("federation: subgraph %q not found", name)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("federation: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", target.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("federation: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range target.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("federation: call subgraph %q: %w", name, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("federation: read response: %w", err)
	}

	var gqlResp GraphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("federation: unmarshal response: %w", err)
	}
	return &gqlResp, nil
}

// QueryAll 并行查询所有子图(同一查询扇出)。
func (g *Gateway) QueryAll(ctx context.Context, req GraphQLRequest) map[string]*GraphQLResponse {
	results := make(map[string]*GraphQLResponse, len(g.subgraphs))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, sg := range g.subgraphs {
		wg.Add(1)
		go func(sg SubgraphConfig) {
			defer wg.Done()
			resp, err := g.QuerySubgraph(ctx, sg.Name, req)
			mu.Lock()
			if err == nil {
				results[sg.Name] = resp
			}
			mu.Unlock()
		}(sg)
	}
	wg.Wait()
	return results
}
