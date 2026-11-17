package observe

import (
	"sync"
	"time"
)

// Snapshot 是一次或多次 Run 累计的指标。
// 延迟用毫秒累计值，token 用 API 返回的 usage 累加，工具错误只计 Execute 返回的 error。
// hook 短路替换的工具结果不是错误。
type Snapshot struct {
	SessionDurationMs int64 `json:"session_duration_ms"`
	Turns             int64 `json:"turns"`
	TurnDurationMs    int64 `json:"turn_duration_ms"`
	ModelCalls        int64 `json:"model_calls"`
	ModelLatencyMs    int64 `json:"model_latency_ms"`
	ModelErrors       int64 `json:"model_errors"`
	PromptTokens      int64 `json:"prompt_tokens"`
	CompletionTokens  int64 `json:"completion_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	ToolCalls         int64 `json:"tool_calls"`
	ToolErrors        int64 `json:"tool_errors"`
	ToolLatencyMs     int64 `json:"tool_latency_ms"`
	Handoffs          int64 `json:"handoffs"`
}

// Metrics 是线程安全的累计器。
type Metrics struct {
	mu   sync.Mutex
	snap Snapshot
}

// NewMetrics 创建一个空的累计器。
func NewMetrics() *Metrics {
	return &Metrics{}
}

// Snapshot 返回当前副本。
func (m *Metrics) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap
}

// ObserveSession 记录会话耗时。
func (m *Metrics) ObserveSession(d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.snap.SessionDurationMs = d.Milliseconds()
	m.mu.Unlock()
}

// ObserveTurn 记录一轮迭代的耗时。
func (m *Metrics) ObserveTurn(d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.snap.Turns++
	m.snap.TurnDurationMs += d.Milliseconds()
	m.mu.Unlock()
}

// ObserveModel 记录一次模型调用。failed 为 true 时不累加 token（响应里没有可用的 usage）。
func (m *Metrics) ObserveModel(latency time.Duration, prompt, completion, total int64, failed bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.snap.ModelCalls++
	m.snap.ModelLatencyMs += latency.Milliseconds()
	if failed {
		m.snap.ModelErrors++
	} else {
		m.snap.PromptTokens += prompt
		m.snap.CompletionTokens += completion
		m.snap.TotalTokens += total
	}
	m.mu.Unlock()
}

// ObserveTool 记录一次工具调用。failed 表示工具实现返回了 error。
func (m *Metrics) ObserveTool(latency time.Duration, failed bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.snap.ToolCalls++
	m.snap.ToolLatencyMs += latency.Milliseconds()
	if failed {
		m.snap.ToolErrors++
	}
	m.mu.Unlock()
}

// ObserveHandoff 记录一次实际发生的上下文移交。
func (m *Metrics) ObserveHandoff() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.snap.Handoffs++
	m.mu.Unlock()
}
