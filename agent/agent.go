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
//	ag := agent.New(client,
//	    agent.WithRegistry(registry),
//	    agent.WithPrompt(pb),
//	    agent.WithMessages(messages),
//	)
//	ag.Run()
type Agent struct {
	client      openai.Client
	registry    *tool.Registry
	prompt      *prompt.Builder
	messages    []openai.ChatCompletionMessageParamUnion
	maxIter     int
	tokenThresh int
	logger      *log.Logger
}

// Option 是 Agent 的函数式选项。
type Option func(*Agent)

// New 创建一个 Agent 实例。
func New(client openai.Client, opts ...Option) *Agent {
	a := &Agent{
		client:      client,
		maxIter:     10,        // 默认值
		tokenThresh: 200_000,   // 默认值
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

// WithMessages 设置初始消息列表（不包含 System Prompt，Run() 会自动前置）。
func WithMessages(msg []openai.ChatCompletionMessageParamUnion) Option {
	return func(a *Agent) { a.messages = msg }
}

// WithMaxIterations 设置最大迭代次数。
func WithMaxIterations(n int) Option {
	return func(a *Agent) { a.maxIter = n }
}

// WithTokenThreshold 设置 token 用量阈值。
func WithTokenThreshold(n int) Option {
	return func(a *Agent) { a.tokenThresh = n }
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
	// 前置 System Prompt
	if a.prompt != nil {
		sysMsg := a.prompt.Build()
		a.messages = append(
			[]openai.ChatCompletionMessageParamUnion{openai.SystemMessage(sysMsg)},
			a.messages...,
		)
	}

	for i := 1; i <= a.maxIter; i++ {
		fmt.Printf("=== iteration %d ===\n", i)

		params := openai.ChatCompletionNewParams{
			Model:    "deepseek-v4-flash",
			Messages: a.messages,
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

		// 从返回的 usage 中计算 token 用量，超过阈值则提前终止
		used := resp.Usage.TotalTokens
		fmt.Printf("[usage] prompt=%d completion=%d total=%d (threshold=%d)\n",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, used, a.tokenThresh)
		if used >= int64(a.tokenThresh) {
			log.Printf("token 用量 %d 达到阈值 %d，终止迭代（iteration %d）", used, a.tokenThresh, i)
			return
		}

		choice := resp.Choices[0]
		msg := choice.Message
		a.messages = append(a.messages, msg.ToParam())

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
			a.messages = append(a.messages, openai.ToolMessage(result, call.ID))
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
	log.Printf("调用大模型失败" + err.Error())
}

