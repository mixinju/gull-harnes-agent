package agent

import (
	"context"

	"gull-herness-agent/hook"
	"gull-herness-agent/logger"
	"gull-herness-agent/observe"
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
//	    agent.WithHooks(hook.New()),
//	    agent.WithTracer(observe.NewTracer()),
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
	hooks     *hook.Chain
	tracer    *observe.Tracer
	metrics   *observe.Metrics
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

// WithHooks 设置生命周期 hook。nil 与不设置相同。
func WithHooks(h *hook.Chain) Option {
	return func(a *Agent) { a.hooks = h }
}

// WithTracer 设置 tracer。nil 时 Run 会自建一个，并把结束时的 span 写入日志。
func WithTracer(t *observe.Tracer) Option {
	return func(a *Agent) { a.tracer = t }
}

// WithMetrics 设置指标累计器。nil 时 Run 会自建一个，并在结束时写入日志。
func WithMetrics(m *observe.Metrics) Option {
	return func(a *Agent) { a.metrics = m }
}

// Run 启动 Agent Loop。等价于 RunContext(context.Background())。
func (a *Agent) Run() {
	a.RunContext(context.Background())
}

// truncate 截断字符串用于日志展示，避免超长输出。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
