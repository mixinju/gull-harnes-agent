package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/openai/openai-go"
)

// Context 管理对话历史和 token 用量。
//
// 它对 Agent 透明：Agent 只管 Append 消息、读 Messages、拿 Tokens 判断是否该压缩。
// token 计数策略：totalTokens = 上次 API 返回的 usage + 本次新追加消息的估算增量。
//   - 每次 Append 时，增量 = estimator.Estimate(新消息)，累加到 totalTokens
//   - 每次 API 返回后，用 resp.Usage.TotalTokens 校准 totalTokens（消除估算误差）
//   - Compact（压缩）后，totalTokens 重置为摘要消息的估算值
//
// 这样既能在"发请求前"预判，又能在"请求回来后"用真实值纠偏。
type Context struct {
	messages   []openai.ChatCompletionMessageParamUnion
	estimator  TokenEstimator
	summarizer Summarizer
	threshold  int64 // token 阈值，超过则触发压缩

	// totalTokens 当前上下文的 token 总量估算。
	// 初始为 0，Append 时累加估算增量，UpdateUsage 时被 API 真实值校准。
	totalTokens int64
}

// ContextOption 是 Context 的函数式选项。
type ContextOption func(*Context)

// NewContext 创建一个 Context 实例。
//
// 默认使用 CharRatioEstimator，阈值 200_000。
// 如需 LLM 摘要压缩，用 WithSummarizer 传入。
func NewContext(opts ...ContextOption) *Context {
	c := &Context{
		estimator: NewCharRatioEstimator(),
		threshold: 200_000,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithEstimator 设置 token 估算器。
func WithEstimator(e TokenEstimator) ContextOption {
	return func(c *Context) { c.estimator = e }
}

// WithSummarizer 设置摘要器（启用 Compact 能力的前提）。
func WithSummarizer(s Summarizer) ContextOption {
	return func(c *Context) { c.summarizer = s }
}

// WithThreshold 设置 token 阈值。
func WithThreshold(n int64) ContextOption {
	return func(c *Context) { c.threshold = n }
}

// WithInitialMessages 设置初始消息列表（可变参数）。
func WithInitialMessages(msgs ...openai.ChatCompletionMessageParamUnion) ContextOption {
	return func(c *Context) { c.messages = msgs }
}

// Append 追加一条消息，并累加估算的 token 增量。
func (c *Context) Append(msg openai.ChatCompletionMessageParamUnion) {
	c.messages = append(c.messages, msg)
	c.totalTokens += int64(c.estimator.Estimate(msg))
}

// AppendMany 批量追加消息。
func (c *Context) AppendMany(msgs ...openai.ChatCompletionMessageParamUnion) {
	for _, m := range msgs {
		c.Append(m)
	}
}

// Messages 返回当前消息列表（只读视图，调用方不应修改）。
func (c *Context) Messages() []openai.ChatCompletionMessageParamUnion {
	return c.messages
}

// Tokens 返回当前 token 总量估算。
func (c *Context) Tokens() int64 {
	return c.totalTokens
}

// Threshold 返回 token 阈值。
func (c *Context) Threshold() int64 {
	return c.threshold
}

// UpdateUsage 用 API 返回的真实 usage 校准 token 总量。
//
// 为什么需要校准？因为 Append 时用的是估算值，会有误差。
// 每次 API 请求回来后，usage 包含了精确的 prompt_tokens + completion_tokens，
// 用它覆盖 totalTokens，消除累积误差。
//
// 调用时机：Agent 每次收到 LLM 响应后调用。
func (c *Context) UpdateUsage(usage openai.CompletionUsage) {
	c.totalTokens = usage.TotalTokens
}

// ShouldCompact 判断是否需要压缩上下文。
func (c *Context) ShouldCompact() bool {
	return c.totalTokens >= c.threshold
}

// Compact 压缩上下文：把历史消息替换成一条摘要。
//
// 流程：
//  1. 保留第一条 system prompt（它通常是角色设定，不该被压缩）
//  2. 把其余消息交给 Summarizer 生成摘要
//  3. 用 [system prompt, 摘要] 替换整个 messages
//  4. totalTokens 重置为估算值（后续会被下一次 API usage 校准）
//
// 如果没配置 Summarizer，则降级为"删除最早的一半消息"的粗暴策略。
func (c *Context) Compact(ctx context.Context) error {
	if len(c.messages) <= 2 {
		return nil // 消息太少，不压缩
	}

	// 保留第一条 system 消息（如果有）
	var head []openai.ChatCompletionMessageParamUnion
	rest := c.messages
	if isSystemMessage(c.messages[0]) {
		head = c.messages[:1]
		rest = c.messages[1:]
	}

	var newMessages []openai.ChatCompletionMessageParamUnion
	newMessages = append(newMessages, head...)

	if c.summarizer != nil {
		summary, err := c.summarizer.Summarize(ctx, rest)
		if err != nil {
			return fmt.Errorf("压缩上下文失败: %w", err)
		}
		newMessages = append(newMessages, summary)
		log.Printf("[context] 已压缩 %d 条历史消息为 1 条摘要", len(rest))
	} else {
		// 降级策略：只保留最近一半消息
		half := len(rest) / 2
		newMessages = append(newMessages, rest[half:]...)
		log.Printf("[context] 无摘要器，降级裁剪：删除最早 %d 条消息", half)
	}

	c.messages = newMessages
	// 重新估算总 token
	c.totalTokens = 0
	for _, m := range c.messages {
		c.totalTokens += int64(c.estimator.Estimate(m))
	}
	return nil
}

// Clear 清空所有消息和 token 计数。
func (c *Context) Clear() {
	c.messages = nil
	c.totalTokens = 0
}

// isSystemMessage 判断消息是否为 system 角色。
func isSystemMessage(msg openai.ChatCompletionMessageParamUnion) bool {
	return msg.OfSystem != nil
}

