package es

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	elastic "github.com/elastic/go-elasticsearch/v8"
	esapi "github.com/elastic/go-elasticsearch/v8/esapi"
)

// Client 封装官方 go-elasticsearch 客户端，提供 gobreath-es 的传输能力。
type Client struct {
	*elastic.Client
}

// ClientOption 配置函数。
type ClientOption func(*elastic.Config)

// WithAddresses 设置 ES 节点地址（http://host:9200）。
func WithAddresses(addrs ...string) ClientOption {
	return func(c *elastic.Config) { c.Addresses = addrs }
}

// WithBasicAuth 设置用户名/密码（HTTP Basic）。
func WithBasicAuth(user, pass string) ClientOption {
	return func(c *elastic.Config) {
		c.Username = user
		c.Password = pass
	}
}

// WithAPIKey 设置 API Key 认证。
func WithAPIKey(key string) ClientOption {
	return func(c *elastic.Config) { c.APIKey = key }
}

// WithCloudID 设置 Elastic Cloud 的 CloudID。
func WithCloudID(id string) ClientOption {
	return func(c *elastic.Config) { c.CloudID = id }
}

// NewClient 创建 ES 客户端。
func NewClient(opts ...ClientOption) (*Client, error) {
	cfg := elastic.Config{}
	for _, o := range opts {
		o(&cfg)
	}
	c, err := elastic.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("gobreath-es: 创建 ES 客户端失败: %w", err)
	}
	return &Client{Client: c}, nil
}

// Ping 探活，确认集群可达。
func (c *Client) Ping(ctx context.Context) (string, error) {
	res, err := c.Client.Ping(c.Client.Ping.WithContext(ctx))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.IsError() {
		return "", fmt.Errorf("gobreath-es: ping 失败 status=%d", res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	return string(b), nil
}

// decodeResponse 读取并解析 ES 响应：检查 IsError，并把 body 解析为 map。
func decodeResponse(res *esapi.Response, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, rerr := io.ReadAll(res.Body)
	if rerr != nil {
		return nil, rerr
	}
	if res.IsError() {
		return nil, fmt.Errorf("gobreath-es: ES 返回错误 status=%d body=%s", res.StatusCode, string(body))
	}
	var m map[string]any
	if len(body) > 0 {
		if jerr := json.Unmarshal(body, &m); jerr != nil {
			return nil, fmt.Errorf("gobreath-es: 解析响应失败: %w (body=%s)", jerr, string(body))
		}
	}
	return m, nil
}
