package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config 是整个 Agent 的配置结构。
//
// 设计原则：
//   - 所有可配置项集中管理，避免散落在环境变量和硬编码中
//   - 敏感字段（api_key、base_url）允许用环境变量覆盖配置文件
//   - 缺失的非关键字段使用默认值
type Config struct {
	// APIKey 是 OpenAI 兼容 API 的密钥。
	// 可被环境变量 GULL_OPENAI_API_KEY 覆盖。
	APIKey string `json:"api_key"`

	// BaseURL 是 API 端点地址。
	// 可被环境变量 GULL_OPENAI_BASE_URL 覆盖。
	BaseURL string `json:"base_url"`

	// Model 是默认使用的模型名称。
	Model string `json:"model"`

	// MaxIterations 是 Agent Loop 的最大迭代次数。
	MaxIterations int `json:"max_iterations"`

	// ContextThreshold 是上下文 token 阈值，超过则触发压缩。
	ContextThreshold int64 `json:"context_threshold"`

	// LogDir 是全量日志的输出目录。
	LogDir string `json:"log_dir"`

	// SessionDir 是历史会话的保存目录。
	SessionDir string `json:"session_dir"`

	// SkillsDir 是 Skill 加载目录。
	SkillsDir string `json:"skills_dir"`

	// MCPConfig 是 MCP 服务配置文件路径。
	MCPConfig string `json:"mcp_config"`
}

// Load 从 JSON 文件加载配置。
//
// 加载顺序：读取文件 → 解析 JSON → 环境变量覆盖 → 默认值填充 → 必填校验。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}

	// 环境变量覆盖（优先级最高，避免密钥写进配置文件）
	if v := os.Getenv("GULL_OPENAI_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("GULL_OPENAI_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}

	// 默认值填充
	if cfg.Model == "" {
		cfg.Model = "deepseek-v4-flash"
	}
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 10
	}
	if cfg.ContextThreshold == 0 {
		cfg.ContextThreshold = 200_000
	}
	if cfg.LogDir == "" {
		cfg.LogDir = "logs"
	}
	if cfg.SessionDir == "" {
		cfg.SessionDir = "sessions"
	}
	if cfg.SkillsDir == "" {
		cfg.SkillsDir = "./skills"
	}
	if cfg.MCPConfig == "" {
		cfg.MCPConfig = "config/mcp.json"
	}

	// 必填校验
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key 未配置（请在 config/config.json 中设置或设置环境变量 GULL_OPENAI_API_KEY）")
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base_url 未配置")
	}

	return &cfg, nil
}

