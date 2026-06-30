package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"gull-herness-agent/tool"
)

// Config 是 MCP 服务的 JSON 配置文件结构。
//
// 传输方式由字段推断：
//   - 有 command → stdio（启动子进程通信）
//   - 有 url → HTTP 传输，type 区分 "sse" / "streamable"（默认）
//
// 示例 mcp.json：
//
//	{
//	  "mcpServers": {
//	    "filesystem": {
//	      "command": "npx",
//	      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
//	    },
//	    "amap": {
//	      "type": "streamable",
//	      "url": "https://mcp.amap.com/mcp?key=YOUR_KEY"
//	    }
//	  }
//	}
type Config struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// ServerConfig 描述一个 MCP server 的启动方式。
//
// 以下两种方式二选一：
//   - stdio：设置 Command（和可选的 Args），启动子进程通信
//   - HTTP：设置 URL，通过 SSE 或 Streamable HTTP 连接远程端点
//     Type 字段区分传输类型："sse" 或 "streamable"（默认）
type ServerConfig struct {
	// Command 是 stdio 模式的可执行程序名（如 "npx"、"python"）。
	Command string `json:"command,omitempty"`
	// Args 是 stdio 模式的命令行参数列表。
	Args []string `json:"args,omitempty"`
	// URL 是 HTTP 模式的 MCP 端点地址。
	URL string `json:"url,omitempty"`
	// Type 是 HTTP 传输类型："sse" 或 "streamable"（默认）。
	// 仅当 URL 非空时生效，用于区分两种 HTTP 传输协议。
	Type string `json:"type,omitempty"`
}

// LoadConfig 从 JSON 文件加载 MCP 服务配置。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}

	if len(config.MCPServers) == 0 {
		return nil, fmt.Errorf("配置文件中没有 mcpServers 定义")
	}

	return &config, nil
}

// LoadClients 根据配置文件启动所有 MCP server 并返回对应的 Client 列表。
//
// 任何 server 启动失败都不会阻止其他 server 的启动（降级策略）。
// 返回的 clients 只包含成功启动的，调用方负责逐个 Close。
func LoadClients(path string) ([]*Client, error) {
	config, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}

	var clients []*Client
	for name, sc := range config.MCPServers {
		var client *Client
		var err error

		if sc.URL != "" {
			// 有 url → HTTP 传输，根据 type 选择 SSE 或 Streamable HTTP
			switch sc.Type {
			case "sse":
				client, err = NewSSEClient(sc.URL)
			default: // "" 或 "streamable"
				client, err = NewStreamableClient(sc.URL)
			}
		} else {
			// 有 command → stdio 传输
			client, err = NewClient(sc.Command, sc.Args...)
		}

		if err != nil {
			log.Printf("警告: MCP server %q 启动失败: %v", name, err)
			continue
		}
		clients = append(clients, client)
	}

	return clients, nil
}

// RegisterAllFromConfig 从配置文件加载所有 MCP server，并将所有工具注册到 registry。
//
// 返回的 clients 列表需要在程序退出前 Close（通常用 defer）。
// 推荐使用 LoadAll，它返回一个 cleanup 函数，调用方只需 defer cleanup()。
func RegisterAllFromConfig(registry *tool.Registry, configPath string) ([]*Client, error) {
	clients, err := LoadClients(configPath)
	if err != nil {
		return nil, err
	}

	for _, client := range clients {
		tools, err := client.ListTools(context.Background())
		if err != nil {
			log.Printf("警告: 获取 MCP 工具列表失败: %v", err)
			continue
		}
		for _, t := range tools {
			registry.Register(NewAdapter(client, t))
		}
		log.Printf("已注册 %d 个 MCP 工具", len(tools))
	}

	return clients, nil
}

// LoadAll 从配置文件加载 MCP 工具到 registry，返回一个 cleanup 函数。
//
// 降级策略：配置文件不存在、解析失败或任何 MCP server 不可用时，
// 只记录日志，不影响主程序和内置工具。调用方需 defer 调用返回的 cleanup。
//
// 用法：
//
//	defer mcp.LoadAll(registry, "config/mcp.json")()
func LoadAll(registry *tool.Registry, configPath string) func() {
	clients, err := RegisterAllFromConfig(registry, configPath)
	if err != nil {
		log.Printf("MCP 配置加载失败，跳过 MCP 工具: %v", err)
		return func() {}
	}
	return func() {
		for _, c := range clients {
			if err := c.Close(); err != nil {
				log.Printf("关闭 MCP client 失败: %v", err)
			}
		}
	}
}

