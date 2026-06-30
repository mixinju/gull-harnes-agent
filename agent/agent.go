package agent

import (
	"context"
	"fmt"
	"time"

	"gull-herness-agent/logger"
	"gull-herness-agent/prompt"
	"gull-herness-agent/session"
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
//	    agent.WithModel("deepseek-v4-flash"),
//	    agent.WithRegistry(registry),
//	    agent.WithPrompt(pb),
//	    agent.WithContext(ctx),
//	    agent.WithUserInput("帮我看看 main.go"),
//	    agent.WithSession(sess),
//	    agent.WithLogger(lg),
//	)
//	ag.Run()
type Agent struct {
	client    openai.Client
	registry  *tool.Registry
	prompt    *prompt.Builder
	ctx       *Context
	maxIter   int
	model     string
	userInput string
	logger    *logger.Logger
	session   *session.Session
}

// Option 是 Agent 的函数式选项。
type Option func(*Agent)

// New 创建一个 Agent 实例。
func New(client openai.Client, opts ...Option) *Agent {
	a := &Agent{
		client:  client, 
		model:   "deepseek-v4-flash",
		maxIter: 10,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// WithModel 设置使用的模型名称。
func WithModel(m string) Option {
	return func(a *Agent) { a.model = m }
}

// WithRegistry 设置工具注册表。
func WithRegistry(r *tool.Registry) Option {
	return func(a *Agent) { a.registry = r }
}

// WithPrompt 设置 System Prompt 构建器。
// Run() 时会自动调用 Build() 并作为 system 消息注入到 context 最前面。
func WithPrompt(pb *prompt.Builder) Option {
	return func(a *Agent) { a.prompt = pb }
}

// WithContext 设置上下文管理器。
// 如果不传，Run() 会创建一个默认的 Context。
func WithContext(c *Context) Option {
	return func(a *Agent) { a.ctx = c }
}

// WithUserInput 设置用户输入。
// Run() 时会作为一条 user 消息注入到 context 中（在 system prompt 之后）。
func WithUserInput(input string) Option {
	return func(a *Agent) { a.userInput = input }
}

// WithMaxIterations 设置最大迭代次数。
func WithMaxIterations(n int) Option {
	return func(a *Agent) { a.maxIter = n }
}

// WithLogger 设置通用日志器。
func WithLogger(l *logger.Logger) Option {
	return func(a *Agent) { a.logger = l }
}

// WithSession 设置会话管理器。
// 运行过程中的所有消息都会记录到 session 中，便于后续回放和审计。
func WithSession(s *session.Session) Option {
	return func(a *Agent) { a.session = s }
}

// Run 启动 Agent Loop。
//
// 流程：
//  1. 注入 System Prompt（如果配置了 prompt Builder）
//  2. 注入用户输入（作为 user 消息）
//  3. 进入主循环：调用 LLM → 处理工具调用 → 回填结果，直到满足终止条件
//
// 终止条件：
//   - 模型未发起工具调用（已给出最终回复）
//   - 达到最大迭代次数
//   - finish_reason == "length"（达到 token 上限）
//   - 上下文压缩后仍超阈值
//   - API 调用失败
func (a *Agent) Run() {
	if a.ctx == nil {
		a.ctx = NewContext()
	}

	// 注入 System Prompt
	if a.prompt != nil {
		sysMsg := openai.SystemMessage(a.prompt.Build())
		a.ctx.Append(sysMsg)
		if a.session != nil {
			a.session.AddMessage(sysMsg)
		}
	}

	// 注入用户输入
	if a.userInput != "" {
		userMsg := openai.UserMessage(a.userInput)
		a.ctx.Append(userMsg)
		if a.session != nil {
			a.session.AddMessage(userMsg)
		}
	}

	startTime := time.Now()
	a.logger.Info("模型: %s | token 阈值: %d | 最大迭代: %d",
		a.model, a.ctx.Threshold(), a.maxIter)
	a.logger.Info("用户输入: %s", truncate(a.userInput, 200))
	a.logger.Info("")

	var cumulativeTokens int64

	for i := 1; i <= a.maxIter; i++ {
		a.logger.Step("=== 第 %d 轮迭代 ===", i)

		params := openai.ChatCompletionNewParams{
			Model:    a.model,
			Messages: a.ctx.Messages(),
			Tools:    a.registry.ToChatCompletionTools(),
		}

		// 全量请求体写入日志文件
		a.logger.JSON("REQUEST", params)
		a.logger.Step("[LLM] 发起请求 (messages=%d, tools=%d)",
			len(a.ctx.Messages()), len(params.Tools))

		resp, err := a.client.Chat.Completions.New(context.Background(), params)
		if err != nil {
			a.logger.Error("调用大模型失败: %v", err)
			a.finalize("error", i-1, cumulativeTokens, time.Since(startTime))
			return
		}

		// 全量响应体写入日志文件
		a.logger.JSON("RESPONSE", resp)

		// 用 API 返回的真实 usage 校准 token 总量
		a.ctx.UpdateUsage(resp.Usage)
		cumulativeTokens += resp.Usage.TotalTokens

		choice := resp.Choices[0]
		msg := choice.Message
		a.logger.Step("[LLM] 响应: finish_reason=%s, prompt=%d, completion=%d, total=%d",
			choice.FinishReason, resp.Usage.PromptTokens,
			resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
		a.logger.Step("[LLM] 当前上下文 token: %d | 累计消耗: %d / 阈值 %d",
			a.ctx.Tokens(), cumulativeTokens, a.ctx.Threshold())

		// token 超阈值：先尝试压缩，压缩后仍超则终止
		if a.ctx.ShouldCompact() {
			a.logger.Step("[决策] token %d 达到阈值 %d，触发上下文压缩",
				a.ctx.Tokens(), a.ctx.Threshold())
			if err := a.ctx.Compact(context.Background()); err != nil {
				a.logger.Error("上下文压缩失败: %v", err)
				a.finalize("error", i-1, cumulativeTokens, time.Since(startTime))
				return
			}
			if a.ctx.ShouldCompact() {
				a.logger.Step("[决策] 压缩后 token %d 仍超阈值 %d，终止迭代",
					a.ctx.Tokens(), a.ctx.Threshold())
				a.finalize("error", i-1, cumulativeTokens, time.Since(startTime))
				return
			}
			a.logger.Step("[决策] 压缩完成，当前 token: %d", a.ctx.Tokens())
		}

		// 追加 assistant 消息
		a.ctx.Append(msg.ToParam())
		if a.session != nil {
			a.session.AddMessage(msg.ToParam())
		}

		// finish_reason == "length"：达到 token 上限
		if choice.FinishReason == "length" {
			a.logger.Step("[决策] 达到模型 token 上限，终止迭代（第 %d 轮）", i)
			if msg.Content != "" {
				a.logger.Step("=== 部分回复 ===")
				a.logger.Step("%s", msg.Content)
			}
			a.finalize("completed", i, cumulativeTokens, time.Since(startTime))
			return
		}

		// 没有工具调用：模型已给出最终回复
		if len(msg.ToolCalls) == 0 {
			a.logger.Step("[决策] 模型未发起工具调用，输出最终回复（第 %d 轮）", i)
			a.logger.Step("")
			a.logger.Step("=== 最终回复 ===")
			a.logger.Step("%s", msg.Content)
			a.finalize("completed", i, cumulativeTokens, time.Since(startTime))
			return
		}

		// 执行模型请求的所有工具调用，并把结果作为 tool 消息回填
		a.logger.Step("[决策] 模型选择调用 %d 个工具", len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			a.logger.Step("[TOOL] %s(%s)", call.Function.Name, truncate(call.Function.Arguments, 200))
			toolStart := time.Now()
			result := a.dispatch(call)
			elapsed := time.Since(toolStart)
			a.logger.Step("[TOOL] 耗时 %v | 结果(%d 字符): %s",
				elapsed, len([]rune(result)), truncate(result, 300))
			toolMsg := openai.ToolMessage(result, call.ID)
			a.ctx.Append(toolMsg)
			if a.session != nil {
				a.session.AddMessage(toolMsg)
			}
		}
		a.logger.Step("")
	}

	a.logger.Step("[决策] 达到最大迭代次数 %d，终止 agent loop", a.maxIter)
	a.finalize("completed", a.maxIter, cumulativeTokens, time.Since(startTime))
}

// dispatch 通过注册表分发工具调用。
func (a *Agent) dispatch(call openai.ChatCompletionMessageToolCall) string {
	result, err := a.registry.Dispatch(call.Function.Name, call.Function.Arguments)
	if err != nil {
		return fmt.Sprintf("工具执行失败: %v", err)
	}
	return result
}

// finalize 记录最终指标到 session。
func (a *Agent) finalize(status string, iterations int, tokens int64, duration time.Duration) {
	if a.session == nil {
		return
	}
	a.session.SetMetrics(iterations, tokens, duration)
	a.session.SetStatus(status)
}

// truncate 截断字符串用于日志展示，避免超长输出。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
