package mcp

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client 封装了 MCP SDK 的客户端和会话管理。
//
// 支持三种传输方式：
//   - stdio：通过 CommandTransport 与子进程通信（NewClient）
//   - SSE：通过 SSEClientTransport 与 HTTP 端点通信（NewSSEClient）
//   - Streamable HTTP：通过 StreamableClientTransport（NewStreamableClient）
type Client struct {
	session *sdkmcp.ClientSession
}

// NewClient 启动 MCP server 子进程（stdio 传输）并完成初始化握手。
func NewClient(command string, args ...string) (*Client, error) {
	cmd := exec.Command(command, args...)

	session, err := connectClient(&sdkmcp.CommandTransport{Command: cmd})
	if err != nil {
		return nil, err
	}
	return &Client{session: session}, nil
}

// NewSSEClient 通过 SSE（Server-Sent Events）传输连接到 MCP server。
//
// endpoint 是 SSE 端点 URL，例如 "http://localhost:8080/sse"。
func NewSSEClient(endpoint string) (*Client, error) {
	return NewSSEClientWithHTTP(endpoint, nil)
}

// NewSSEClientWithHTTP 通过 SSE 连接到 MCP server，使用指定的 HTTP 客户端。
func NewSSEClientWithHTTP(endpoint string, httpClient *http.Client) (*Client, error) {
	transport := &sdkmcp.SSEClientTransport{
		Endpoint:   endpoint,
		HTTPClient: httpClient,
	}
	session, err := connectClient(transport)
	if err != nil {
		return nil, err
	}
	return &Client{session: session}, nil
}

// NewStreamableClient 通过 Streamable HTTP 传输连接到 MCP server。
//
// endpoint 是 Streamable HTTP 端点 URL，例如 "http://localhost:9090/mcp"。
func NewStreamableClient(endpoint string) (*Client, error) {
	return NewStreamableClientWithHTTP(endpoint, nil)
}

// NewStreamableClientWithHTTP 通过 Streamable HTTP 连接到 MCP server，使用指定的 HTTP 客户端。
func NewStreamableClientWithHTTP(endpoint string, httpClient *http.Client) (*Client, error) {
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: httpClient,
	}
	session, err := connectClient(transport)
	if err != nil {
		return nil, err
	}
	return &Client{session: session}, nil
}

// connectClient 是统一的连接辅助函数，减少三种构造函数的重复代码。
func connectClient(transport sdkmcp.Transport) (*sdkmcp.ClientSession, error) {
	sdkClient := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "gull-herness-agent",
		Version: "0.1.0",
	}, nil)

	session, err := sdkClient.Connect(context.Background(), transport, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 MCP server 失败: %w", err)
	}
	return session, nil
}

// ListTools 获取 MCP server 暴露的所有工具。
func (c *Client) ListTools(ctx context.Context) ([]*sdkmcp.Tool, error) {
	result, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("列出工具失败: %w", err)
	}
	return result.Tools, nil
}

// CallTool 调用 MCP server 的指定工具，返回文本结果。
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	result, err := c.session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("调用工具 %q 失败: %w", name, err)
	}

	// 拼接所有 text content 返回
	var text string
	for _, content := range result.Content {
		if tc, ok := content.(*sdkmcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, nil
}

// Close 关闭与 MCP server 的连接并终止子进程。
func (c *Client) Close() error {
	return c.session.Close()
}

