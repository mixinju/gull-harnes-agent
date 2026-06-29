package mcp

import (
	"context"
	"fmt"

	"gull-herness-agent/tool"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go"
)

// Adapter 将 MCP server 的工具适配为本地 tool.Tool 接口。
//
// 持有 MCP Client 和 SDK 的 Tool 元数据（tools/list 返回），
// 让 Agent Loop 可以通过 Registry 无差别调用 MCP 工具。
type Adapter struct {
	client  *Client
	sdkTool *sdkmcp.Tool
}

// NewAdapter 创建一个 Adapter 适配器。
func NewAdapter(client *Client, t *sdkmcp.Tool) *Adapter {
	return &Adapter{client: client, sdkTool: t}
}

// Name 返回工具名称。
func (a *Adapter) Name() string {
	return a.sdkTool.Name
}

// Description 返回工具描述。
func (a *Adapter) Description() string {
	if a.sdkTool.Description != "" {
		return a.sdkTool.Description
	}
	return a.sdkTool.Name
}

// Schema 将 MCP Tool 的 InputSchema 转换为 OpenAI FunctionDefinitionParam。
//
// SDK Tool 的 InputSchema 类型为 any，实际是 map[string]any
// 形式的 JSON Schema 对象，可以直接作为 FunctionParameters 使用。
func (a *Adapter) Schema() openai.FunctionDefinitionParam {
	schema := a.sdkTool.InputSchema
	if schema == nil {
		return openai.FunctionDefinitionParam{
			Name:        a.sdkTool.Name,
			Description: openai.String(a.Description()),
			Parameters: openai.FunctionParameters{
				"type":       "object",
				"properties": map[string]any{},
			},
		}
	}

	params, _ := schema.(map[string]any)
	if params == nil {
		params = map[string]any{"type": "object", "properties": map[string]any{}}
	}

	return openai.FunctionDefinitionParam{
		Name:        a.sdkTool.Name,
		Description: openai.String(a.Description()),
		Parameters:  params,
	}
}

// Execute 将工具调用委托给 MCP server。
func (a *Adapter) Execute(args map[string]any) (string, error) {
	result, err := a.client.CallTool(context.Background(), a.sdkTool.Name, args)
	if err != nil {
		return "", fmt.Errorf("mcp tool %q 执行失败: %w", a.sdkTool.Name, err)
	}
	return result, nil
}

// 编译期检查 Adapter 实现了 tool.Tool 接口。
var _ tool.Tool = (*Adapter)(nil)

