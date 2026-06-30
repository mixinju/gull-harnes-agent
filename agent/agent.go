package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gull-herness-agent/prompt"
	"gull-herness-agent/tool"

	"github.com/openai/openai-go"
)

// Agent 封装了 Builder + Registry + runAgentLoop 的完整执行体。
//
// 使用方式：
//
//	ctx := agent.NewContext(
//	    agent.WithSummarizer(agent.NewLLMSummarizer(client, "deepseek-v4-flash")),
//	    agent.WithThreshold(200_000),
//	)
//	ag := agent.New(client,
//	    agent.WithRegistry(registry),
//	    agent.WithPrompt(pb),
//	    agent.WithContext(ctx),
//	)
//	ag.Run()
type Agent struct {
	client   openai.Client
	registry *tool.Registry
	prompt   *prompt.Builder
	ctx      *Context
	maxIter  int
	logger   *log.Logger
}

// Option 是 Agent 的函数式选项。
type Option func(*Agent)

// New 创建一个 Agent 实例。
func New(client openai.Client, opts ...Option) *Agent {
	a := &Agent{
		client:  client,
		maxIter: 10, // 默认值
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// WithRegistry 设置工具注册表。
func WithRegistry(r *tool.Registry) Option {
	return func(a *Agent) { a.registry = r }
}

// WithPrompt 设置 System Prompt 构建器。
func WithPrompt(pb *prompt.Builder) Option {
	return func(a *Agent) { a.prompt = pb }
}

// WithContext 设置上下文管理器。
//
// 如果不传，Run() 会创建一个默认的 Context（CharRatio 估算器，阈值 200_000）。
func WithContext(c *Context) Option {
	return func(a *Agent) { a.ctx = c }
}

// WithMaxIterations 设置最大迭代次数。
func WithMaxIterations(n int) Option {
	return func(a *Agent) { a.maxIter = n }
}

// WithLogger 设置日志记录器。
func WithLogger(l *log.Logger) Option {
	return func(a *Agent) { a.logger = l }
}

// Run 启动 Agent Loop。
//
// 会先根据 prompt Builder 生成 System Prompt 并前置到 messages 最前面，
// 然后进入主循环：调用 LLM → 处理工具调用 → 回填结果，直到满足终止条件。
func (a *Agent) Run() {
	// 如果没传 Context，创建默认的
	if a.ctx == nil {
		a.ctx = NewContext()
	}

	// 前置 System Prompt
	if a.prompt != nil {
		sysMsg := a.prompt.Build()
		a.ctx.Append(openai.SystemMessage(sysMsg))
	}

	for i := 1; i <= a.maxIter; i++ {
		fmt.Printf("=== iteration %d ===\n", i)

		params := openai.ChatCompletionNewParams{
			Model:    "deepseek-v4-flash",
			Messages: a.ctx.Messages(),
			Tools:    a.registry.ToChatCompletionTools(),
		}

		// 记录请求体
		a.logRequest(i, params)

		resp, err := a.client.Chat.Completions.New(context.Background(), params)
		if err != nil {
			a.handleError(err)
			return
		}

		// 记录响应体
		a.logResponse(i, resp)

		// 用 API 返回的真实 usage 校准 token 总量
		a.ctx.UpdateUsage(resp.Usage)
		fmt.Printf("[usage] prompt=%d completion=%d total=%d (threshold=%d)\n",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens,
			a.ctx.Tokens(), a.ctx.Threshold())

		// token 超阈值：先尝试压缩，压缩后仍超则终止
		if a.ctx.ShouldCompact() {
			log.Printf("[context] token %d 达到阈值 %d，触发压缩（iteration %d）",
				a.ctx.Tokens(), a.ctx.Threshold(), i)
			if err := a.ctx.Compact(context.Background()); err != nil {
				log.Printf("[context] 压缩失败: %v", err)
				return
			}
			// 压缩后重新判断，如果还是超阈值，说明压缩空间有限，终止
			if a.ctx.ShouldCompact() {
				log.Printf("[context] 压缩后 token %d 仍超阈值 %d，终止迭代", a.ctx.Tokens(), a.ctx.Threshold())
				return
			}
		}

		choice := resp.Choices[0]
		msg := choice.Message
		a.ctx.Append(msg.ToParam())

		// finish_reason == "length"：达到 token 上限
		if choice.FinishReason == "length" {
			log.Printf("达到 token 上限，终止迭代（iteration %d）", i)
			if msg.Content != "" {
				fmt.Println(msg.Content)
			}
			return
		}

		// 没有工具调用：模型已给出最终回复
		if len(msg.ToolCalls) == 0 {
			fmt.Println(msg.Content)
			log.Printf("模型未发起工具调用，结束 agent loop（iteration %d）", i)
			return
		}

		// 执行模型请求的所有工具调用，并把结果作为 tool 消息回填
		for _, call := range msg.ToolCalls {
			result := a.dispatch(call)
			fmt.Printf("[tool] %s -> %s\n", call.Function.Name, result)
			a.ctx.Append(openai.ToolMessage(result, call.ID))
		}
	}

	log.Printf("达到最大迭代次数 %d，终止 agent loop", a.maxIter)
}

// dispatch 通过注册表分发工具调用。
func (a *Agent) dispatch(call openai.ChatCompletionMessageToolCall) string {
	result, err := a.registry.Dispatch(call.Function.Name, call.Function.Arguments)
	if err != nil {
		return fmt.Sprintf("工具执行失败: %v", err)
	}
	return result
}

// logRequest 将请求体以 JSON 格式写入日志文件。
func (a *Agent) logRequest(iter int, params openai.ChatCompletionNewParams) {
	if a.logger == nil {
		return
	}
	reqJSON, _ := json.MarshalIndent(params, "", "  ")
	a.logger.Printf("\n========== REQUEST iter=%d (%s) ==========\n%s\n",
		iter, time.Now().Format(time.RFC3339), string(reqJSON))
}

// logResponse 将响应体以 JSON 格式写入日志文件。
func (a *Agent) logResponse(iter int, resp *openai.ChatCompletion) {
	if a.logger == nil {
		return
	}
	respJSON, _ := json.MarshalIndent(resp, "", "  ")
	a.logger.Printf("\n========== RESPONSE iter=%d (%s) ==========\n%s\n",
		iter, time.Now().Format(time.RFC3339), string(respJSON))
}

// handleError 处理 API 调用异常。
func (a *Agent) handleError(err error) {
	log.Printf("调用大模型失败: %v", err)
}

