package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
)

// Message 是可序列化的消息格式。
//
// openai-go 的 ChatCompletionMessageParamUnion 是结构体 union（含多个指针字段），
// 直接 Marshal 会产生大量空字段，且格式不稳定。这里用简单结构体规避序列化难题，
// 只保留持久化和回放所需的必要信息。
type Message struct {
	Role       string     `json:"role"`                  // system / user / assistant / tool
	Content    string     `json:"content,omitempty"`     // 文本内容
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // 仅 assistant 消息有
	ToolCallID string     `json:"tool_call_id,omitempty"` // 仅 tool 消息有
}

// ToolCall 记录一次模型发起的工具调用。
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// Session 记录一次完整的 Agent 运行。
//
// 每次 Run() 对应一个 Session，包含元数据（模型、耗时、token 消耗）
// 和完整的消息历史。会话以 JSON 文件持久化到 SessionDir 目录下。
type Session struct {
	ID          string    `json:"id"`            // 时间戳格式：20260630-143052
	CreatedAt   time.Time `json:"created_at"`
	Model       string    `json:"model"`
	UserPrompt  string    `json:"user_prompt"`
	Status      string    `json:"status"`        // running / completed / error
	Iterations  int       `json:"iterations"`
	TotalTokens int64     `json:"total_tokens"`
	Duration    string    `json:"duration"`
	Messages    []Message `json:"messages"`
}

// New 创建一个新的会话实例。
func New(model, userPrompt string) *Session {
	now := time.Now()
	return &Session{
		ID:         now.Format("20060102-150405"),
		CreatedAt:  now,
		Model:      model,
		UserPrompt: userPrompt,
		Status:     "running",
		Messages:   []Message{},
	}
}

// AddMessage 将一条 openai-go 消息转换为可序列化格式并追加到会话中。
func (s *Session) AddMessage(msg openai.ChatCompletionMessageParamUnion) {
	s.Messages = append(s.Messages, toSessionMessage(msg))
}

// SetStatus 更新会话状态。
func (s *Session) SetStatus(status string) {
	s.Status = status
}

// SetMetrics 记录运行结束时的指标。
func (s *Session) SetMetrics(iterations int, tokens int64, duration time.Duration) {
	s.Iterations = iterations
	s.TotalTokens = tokens
	s.Duration = duration.String()
}

// Save 将会话序列化为 JSON 并保存到 dir 目录下。
func (s *Session) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	path := filepath.Join(dir, s.ID+".json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入会话文件失败: %w", err)
	}
	return nil
}

// Load 从 JSON 文件加载一个历史会话。
func Load(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// List 列出指定目录下的所有历史会话（按文件名排序）。
func List(dir string) ([]*Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var sessions []*Session
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		s, err := Load(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// toSessionMessage 把 openai-go 的 union 消息转为简单可序列化结构。
//
// 逐层判断 OfSystem / OfUser / OfAssistant / OfTool 哪个字段有值，
// 然后提取对应的文本内容、工具调用等信息。
func toSessionMessage(msg openai.ChatCompletionMessageParamUnion) Message {
	var m Message

	if !param.IsOmitted(msg.OfSystem) {
		m.Role = "system"
		m.Content = systemContentToString(msg.OfSystem.Content)
		return m
	}
	if !param.IsOmitted(msg.OfUser) {
		m.Role = "user"
		m.Content = userContentToString(msg.OfUser.Content)
		return m
	}
	if !param.IsOmitted(msg.OfAssistant) {
		m.Role = "assistant"
		if !param.IsOmitted(msg.OfAssistant.Content.OfString) {
			m.Content = msg.OfAssistant.Content.OfString.Value
		}
		for _, call := range msg.OfAssistant.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, ToolCall{
				ID:   call.ID,
				Name: call.Function.Name,
				Args: call.Function.Arguments,
			})
		}
		return m
	}
	if !param.IsOmitted(msg.OfTool) {
		m.Role = "tool"
		m.Content = toolContentToString(msg.OfTool.Content)
		m.ToolCallID = msg.OfTool.ToolCallID
		return m
	}

	m.Role = "unknown"
	return m
}

// 以下 contentToString 辅助函数从 openai-go 的 union Content 中提取文本。
// 与 agent/estimator.go 中的同名函数逻辑一致，但独立维护以避免包间耦合。

func systemContentToString(c openai.ChatCompletionSystemMessageParamContentUnion) string {
	if !param.IsOmitted(c.OfString) {
		return c.OfString.Value
	}
	var sb []byte
	for _, part := range c.OfArrayOfContentParts {
		sb = append(sb, part.Text...)
	}
	return string(sb)
}

func userContentToString(c openai.ChatCompletionUserMessageParamContentUnion) string {
	if !param.IsOmitted(c.OfString) {
		return c.OfString.Value
	}
	var sb []byte
	for _, part := range c.OfArrayOfContentParts {
		if !param.IsOmitted(part.OfText) {
			sb = append(sb, part.OfText.Text...)
		}
	}
	return string(sb)
}

func toolContentToString(c openai.ChatCompletionToolMessageParamContentUnion) string {
	if !param.IsOmitted(c.OfString) {
		return c.OfString.Value
	}
	var sb []byte
	for _, part := range c.OfArrayOfContentParts {
		sb = append(sb, part.Text...)
	}
	return string(sb)
}

