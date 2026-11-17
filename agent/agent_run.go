package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gull-herness-agent/hook"
	"gull-herness-agent/observe"
	"gull-herness-agent/session"

	"github.com/openai/openai-go"
)

// RunContext 启动 Agent Loop。ctx 被取消或超时时，运行以 cancelled 结束，
// 进行中的模型调用和实现了 tool.ContextExecutor 的工具会一起停。
//
// 终止条件与原来相同：
//   - 模型未发起工具调用（已给出最终回复）
//   - 达到最大迭代次数
//   - finish_reason == "length"（达到 token 上限）
//   - 上下文压缩后仍超阈值，或压缩失败
//   - API 调用失败
//   - hook 按上述路径短路，或 hook 自身返回错误
func (a *Agent) RunContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.ctx == nil {
		a.ctx = NewContext()
	}
	if a.tracer == nil {
		a.tracer = observe.NewTracer()
	}
	if a.metrics == nil {
		a.metrics = observe.NewMetrics()
	}

	if a.prompt != nil {
		sysMsg := openai.SystemMessage(a.prompt.Build())
		a.ctx.Append(sysMsg)
		if a.session != nil {
			a.session.AddMessage(sysMsg)
		}
	}
	if a.userInput != "" {
		userMsg := openai.UserMessage(a.userInput)
		a.ctx.Append(userMsg)
		if a.session != nil {
			a.session.AddMessage(userMsg)
		}
	}

	startTime := time.Now()
	ctx, sessionSpan := a.tracer.Start(ctx, "session",
		observe.String("model", a.model),
		observe.String("session_id", a.sessionID()),
	)
	if a.session != nil {
		a.session.SetTraceID(sessionSpan.TraceID)
	}

	var (
		cumulative int64
		closed     bool
		endStatus  = session.StatusError
		endIter    int
	)
	finish := func(status string, iterations int) {
		if closed {
			return
		}
		closed = true
		endStatus = status
		endIter = iterations
		a.finalize(status, iterations, cumulative, time.Since(startTime))
	}
	defer func() {
		if !closed {
			finish(session.StatusError, endIter)
		}
		a.metrics.ObserveSession(time.Since(startTime))
		a.observe(ctx, a.hooks.SessionEnd(ctx, a.sessionEvent(sessionSpan.TraceID)))
		sessionSpan.End(
			observe.String("status", endStatus),
			observe.Int("iterations", endIter),
			observe.Int64("tokens", cumulative),
		)
		a.debugJSON(ctx, "TRACE", a.tracer.Finished())
		a.debugJSON(ctx, "METRICS", a.metrics.Snapshot())
	}()

	a.info(ctx, "模型: %s | token 阈值: %d | 最大迭代: %d",
		a.model, a.ctx.Threshold(), a.maxIter)
	a.info(ctx, "用户输入: %s", truncate(a.userInput, 200))
	a.info(ctx, "")
	a.observe(ctx, a.hooks.SessionStart(ctx, a.sessionEvent(sessionSpan.TraceID)))

	for i := 1; i <= a.maxIter; i++ {
		if err := ctx.Err(); err != nil {
			a.noteCancel(ctx, err)
			finish(session.StatusCancelled, i-1)
			return
		}
		turnStart := time.Now()
		turnCtx, turnSpan := a.tracer.Start(ctx, "turn", observe.Int("iteration", i))
		stop := a.runTurn(turnCtx, i, &cumulative, finish)
		turnSpan.End(observe.Int("iteration", i))
		a.metrics.ObserveTurn(time.Since(turnStart))
		if stop {
			return
		}
	}

	a.step(ctx, "[决策] 达到最大迭代次数 %d，终止 agent loop", a.maxIter)
	finish(session.StatusCompleted, a.maxIter)
}

// runTurn 执行一轮。返回 true 表示 Run 应该停止；停止前已经调用 finish。
func (a *Agent) runTurn(ctx context.Context, i int, cumulative *int64, finish func(string, int)) bool {
	a.step(ctx, "=== 第 %d 轮迭代 ===", i)

	dec, err := a.hooks.BeforeTurn(ctx, hook.TurnEvent{
		Iteration:     i,
		MaxIterations: a.maxIter,
		Tokens:        a.ctx.Tokens(),
	})
	if err != nil {
		a.failHook(ctx, hook.PhaseTurnStart, i, err, finish)
		return true
	}
	if dec.Halt {
		a.halt(ctx, hook.PhaseTurnStart, i-1, dec, finish)
		return true
	}

	modelName := a.model
	msgs := copyMessages(a.ctx.Messages())
	tools := a.toolParams()
	modelEv := hook.ModelEvent{
		Iteration: i,
		Model:     modelName,
		Messages:  msgs,
		ToolCount: len(tools),
	}
	modelEv, dec, err = a.hooks.BeforeModel(ctx, modelEv)
	if err != nil {
		a.failHook(ctx, hook.PhaseModelBefore, i, err, finish)
		return true
	}
	if dec.Halt {
		a.halt(ctx, hook.PhaseModelBefore, i-1, dec, finish)
		return true
	}

	params := openai.ChatCompletionNewParams{
		Model:    modelEv.Model,
		Messages: modelEv.Messages,
		Tools:    tools,
	}
	modelCtx, modelSpan := a.tracer.Start(ctx, "model",
		observe.String("model", modelEv.Model),
		observe.Int("messages", len(modelEv.Messages)),
		observe.Int("tools", len(tools)),
	)
	a.debugJSON(modelCtx, "REQUEST", params)
	a.step(modelCtx, "[LLM] 发起请求 (messages=%d, tools=%d)",
		len(modelEv.Messages), len(params.Tools))

	modelStart := time.Now()
	resp, err := a.client.Chat.Completions.New(modelCtx, params)
	modelLatency := time.Since(modelStart)
	if err != nil {
		a.metrics.ObserveModel(modelLatency, 0, 0, 0, !isCancel(err))
		modelSpan.EndError(err)
		a.failCall(modelCtx, hook.PhaseModelBefore, i, "", err, finish)
		return true
	}
	if len(resp.Choices) == 0 {
		empty := fmt.Errorf("模型响应没有 choices")
		a.metrics.ObserveModel(modelLatency, 0, 0, 0, true)
		modelSpan.EndError(empty)
		a.failCall(modelCtx, hook.PhaseModelAfter, i, "", empty, finish)
		return true
	}

	a.debugJSON(modelCtx, "RESPONSE", resp)
	a.ctx.UpdateUsage(resp.Usage)
	*cumulative += resp.Usage.TotalTokens
	a.metrics.ObserveModel(modelLatency, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens, false)

	choice := resp.Choices[0]
	msg := choice.Message
	a.step(modelCtx, "[LLM] 响应: finish_reason=%s, prompt=%d, completion=%d, total=%d",
		choice.FinishReason, resp.Usage.PromptTokens,
		resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	a.step(modelCtx, "[LLM] 当前上下文 token: %d | 累计消耗: %d / 阈值 %d",
		a.ctx.Tokens(), *cumulative, a.ctx.Threshold())
	modelSpan.End(
		observe.String("finish_reason", choice.FinishReason),
		observe.Int64("tokens.prompt", resp.Usage.PromptTokens),
		observe.Int64("tokens.completion", resp.Usage.CompletionTokens),
		observe.Int64("tokens.total", resp.Usage.TotalTokens),
	)
	a.observe(modelCtx, a.hooks.AfterModel(modelCtx, hook.ModelEvent{
		Iteration:        i,
		Model:            modelEv.Model,
		FinishReason:     choice.FinishReason,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
		Content:          msg.Content,
		ToolCallCount:    len(msg.ToolCalls),
	}))

	if a.ctx.ShouldCompact() {
		if a.handoff(ctx, i, finish) {
			return true
		}
	}

	a.append(msg.ToParam())

	if choice.FinishReason == "length" {
		a.step(ctx, "[决策] 达到模型 token 上限，终止迭代（第 %d 轮）", i)
		if msg.Content != "" {
			a.step(ctx, "=== 部分回复 ===")
			a.step(ctx, "%s", msg.Content)
		}
		a.observe(ctx, a.hooks.AfterTurn(ctx, hook.TurnEvent{
			Iteration: i, MaxIterations: a.maxIter, Tokens: a.ctx.Tokens(), Outcome: "length",
		}))
		finish(session.StatusCompleted, i)
		return true
	}

	if len(msg.ToolCalls) == 0 {
		a.step(ctx, "[决策] 模型未发起工具调用，输出最终回复（第 %d 轮）", i)
		a.step(ctx, "")
		a.step(ctx, "=== 最终回复 ===")
		a.step(ctx, "%s", msg.Content)
		a.observe(ctx, a.hooks.AfterTurn(ctx, hook.TurnEvent{
			Iteration: i, MaxIterations: a.maxIter, Tokens: a.ctx.Tokens(), Outcome: "completed",
		}))
		finish(session.StatusCompleted, i)
		return true
	}

	a.step(ctx, "[决策] 模型选择调用 %d 个工具", len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		if err := ctx.Err(); err != nil {
			a.noteCancel(ctx, err)
			finish(session.StatusCancelled, i-1)
			return true
		}
		if a.runTool(ctx, i, call) {
			finish(session.StatusError, i-1)
			return true
		}
		if err := ctx.Err(); err != nil {
			a.noteCancel(ctx, err)
			finish(session.StatusCancelled, i-1)
			return true
		}
	}
	a.step(ctx, "")
	a.observe(ctx, a.hooks.AfterTurn(ctx, hook.TurnEvent{
		Iteration: i, MaxIterations: a.maxIter, Tokens: a.ctx.Tokens(), Outcome: "continue",
	}))
	return false
}

// handoff 在 token 超阈值时把历史移交给摘要器。返回 true 表示 Run 应停止。
func (a *Agent) handoff(ctx context.Context, i int, finish func(string, int)) bool {
	a.step(ctx, "[决策] token %d 达到阈值 %d，触发上下文压缩",
		a.ctx.Tokens(), a.ctx.Threshold())
	ev := hook.HandoffEvent{
		Kind:         hook.HandoffCompact,
		Iteration:    i,
		Tokens:       a.ctx.Tokens(),
		Threshold:    a.ctx.Threshold(),
		MessageCount: len(a.ctx.Messages()),
	}
	dec, err := a.hooks.BeforeHandoff(ctx, ev)
	if err != nil {
		a.failHook(ctx, hook.PhaseHandoffBefore, i, err, finish)
		return true
	}
	if dec.Halt {
		// 拒绝移交就不能带着超窗上下文继续，和压缩失败同一条终止路径。
		a.halt(ctx, hook.PhaseHandoffBefore, i-1, dec, finish)
		return true
	}

	hctx, span := a.tracer.Start(ctx, "handoff",
		observe.String("kind", hook.HandoffCompact),
		observe.Int64("tokens.before", a.ctx.Tokens()),
		observe.Int("messages.before", len(a.ctx.Messages())),
	)
	if err := a.ctx.Compact(hctx); err != nil {
		span.EndError(err)
		a.failCall(hctx, hook.PhaseHandoffBefore, i, "", err, finish)
		return true
	}
	a.metrics.ObserveHandoff()
	ev.TokensAfter = a.ctx.Tokens()
	ev.MessageCountAfter = len(a.ctx.Messages())
	span.End(
		observe.Int64("tokens.after", ev.TokensAfter),
		observe.Int("messages.after", ev.MessageCountAfter),
	)
	a.observe(hctx, a.hooks.AfterHandoff(hctx, ev))

	if a.ctx.ShouldCompact() {
		still := fmt.Errorf("压缩后 token %d 仍超阈值 %d", a.ctx.Tokens(), a.ctx.Threshold())
		a.step(ctx, "[决策] 压缩后 token %d 仍超阈值 %d，终止迭代",
			a.ctx.Tokens(), a.ctx.Threshold())
		a.notifyError(ctx, hook.ErrorEvent{Phase: hook.PhaseHandoffAfter, Iteration: i, Err: still})
		finish(session.StatusError, i-1)
		return true
	}
	a.step(ctx, "[决策] 压缩完成，当前 token: %d", a.ctx.Tokens())
	return false
}

// runTool 执行一个工具调用。返回 true 表示 hook 基础设施失败，调用方应结束 Run。
// 工具自身的 error 会回填成文本，不结束循环。
func (a *Agent) runTool(ctx context.Context, i int, call openai.ChatCompletionMessageToolCall) bool {
	ev := hook.ToolEvent{
		Iteration: i,
		CallID:    call.ID,
		Name:      call.Function.Name,
		Args:      call.Function.Arguments,
	}
	ev, _, err := a.hooks.BeforeTool(ctx, ev)
	if err != nil {
		a.errlog(ctx, "hook 失败（%s）: %v", hook.PhaseToolBefore, err)
		a.notifyError(ctx, hook.ErrorEvent{Phase: hook.PhaseToolBefore, Iteration: i, Tool: ev.Name, Err: err})
		return true
	}

	toolCtx, span := a.tracer.Start(ctx, "tool",
		observe.String("tool.name", ev.Name),
		observe.String("tool.call_id", ev.CallID),
		observe.Bool("tool.skipped", ev.Skipped),
	)
	a.step(toolCtx, "[TOOL] %s(%s)", ev.Name, truncate(ev.Args, 200))
	toolStart := time.Now()
	var callErr error
	if !ev.Skipped {
		ev.Result, callErr = a.dispatch(toolCtx, ev.Name, ev.Args)
		ev.Err = callErr
	}
	elapsed := time.Since(toolStart)
	if ctx.Err() != nil {
		span.EndError(ctx.Err())
		return false
	}
	failed := callErr != nil
	a.metrics.ObserveTool(elapsed, failed)
	if failed {
		a.notifyError(toolCtx, hook.ErrorEvent{
			Phase: hook.PhaseToolAfter, Iteration: i, Tool: ev.Name, Err: callErr,
		})
		span.EndError(callErr, observe.String("tool.name", ev.Name))
	} else {
		span.End(observe.String("tool.name", ev.Name), observe.Bool("tool.skipped", ev.Skipped))
	}
	a.step(toolCtx, "[TOOL] 耗时 %v | 结果(%d 字符): %s",
		elapsed, len([]rune(ev.Result)), truncate(ev.Result, 300))
	a.observe(toolCtx, a.hooks.AfterTool(toolCtx, ev))
	a.append(openai.ToolMessage(ev.Result, call.ID))
	return false
}

func (a *Agent) dispatch(ctx context.Context, name, args string) (string, error) {
	if a.registry == nil {
		err := fmt.Errorf("未知工具: %s，可用工具: []", name)
		return fmt.Sprintf("工具执行失败: %v", err), err
	}
	result, err := a.registry.DispatchContext(ctx, name, args)
	if err != nil {
		return fmt.Sprintf("工具执行失败: %v", err), err
	}
	return result, nil
}

// failHook 把 hook 自身的错误当成运行失败。工具执行失败不走这里。
func (a *Agent) failHook(ctx context.Context, phase hook.Phase, iteration int, err error, finish func(string, int)) {
	a.errlog(ctx, "hook 失败（%s）: %v", phase, err)
	a.notifyError(ctx, hook.ErrorEvent{Phase: phase, Iteration: iteration, Err: err})
	recorded := iteration - 1
	if recorded < 0 {
		recorded = 0
	}
	finish(session.StatusError, recorded)
}

func (a *Agent) failCall(ctx context.Context, phase hook.Phase, iteration int, tool string, err error, finish func(string, int)) {
	if isCancel(err) {
		a.noteCancel(ctx, err)
		recorded := iteration - 1
		if recorded < 0 {
			recorded = 0
		}
		finish(session.StatusCancelled, recorded)
		return
	}
	switch phase {
	case hook.PhaseHandoffBefore:
		a.errlog(ctx, "上下文压缩失败: %v", err)
	default:
		a.errlog(ctx, "调用大模型失败: %v", err)
	}
	a.notifyError(ctx, hook.ErrorEvent{Phase: phase, Iteration: iteration, Tool: tool, Err: err})
	recorded := iteration - 1
	if recorded < 0 {
		recorded = 0
	}
	finish(session.StatusError, recorded)
}

func (a *Agent) halt(ctx context.Context, phase hook.Phase, iterations int, dec hook.Decision, finish func(string, int)) {
	reason := dec.Reason
	if reason == "" {
		reason = "未说明原因"
	}
	if iterations < 0 {
		iterations = 0
	}
	a.step(ctx, "[决策] hook 短路 %s（%s）: %s", phase, dec.Status(), reason)
	finish(dec.Status(), iterations)
}

func (a *Agent) noteCancel(ctx context.Context, err error) {
	a.step(ctx, "[决策] 运行已取消: %v", err)
	a.notifyError(ctx, hook.ErrorEvent{Phase: hook.PhaseError, Err: err})
}

func (a *Agent) notifyError(ctx context.Context, e hook.ErrorEvent) {
	a.observe(ctx, a.hooks.OnErrorRun(ctx, e))
}

func (a *Agent) observe(ctx context.Context, err error) {
	if err != nil {
		a.errlog(ctx, "观察 hook 失败（结果不变）: %v", err)
	}
}

func (a *Agent) append(msg openai.ChatCompletionMessageParamUnion) {
	a.ctx.Append(msg)
	if a.session != nil {
		a.session.AddMessage(msg)
	}
}

func (a *Agent) toolParams() []openai.ChatCompletionToolParam {
	if a.registry == nil {
		return nil
	}
	return a.registry.ToChatCompletionTools()
}

func (a *Agent) sessionID() string {
	if a.session == nil {
		return ""
	}
	return a.session.ID
}

func (a *Agent) sessionEvent(traceID string) hook.SessionEvent {
	e := hook.SessionEvent{Model: a.model, TraceID: traceID}
	if a.session != nil {
		e.SessionID = a.session.ID
		e.Status = a.session.Status
		e.Iterations = a.session.Iterations
		e.TotalTokens = a.session.TotalTokens
	}
	return e
}

// finalize 记录最终指标到 session。
func (a *Agent) finalize(status string, iterations int, tokens int64, duration time.Duration) {
	if a.session == nil {
		return
	}
	a.session.SetMetrics(iterations, tokens, duration)
	a.session.SetStatus(status)
}

func (a *Agent) step(ctx context.Context, format string, args ...any) {
	if a.logger == nil {
		return
	}
	a.logger.StepContext(ctx, format, args...)
}

func (a *Agent) info(ctx context.Context, format string, args ...any) {
	if a.logger == nil {
		return
	}
	a.logger.InfoContext(ctx, format, args...)
}

func (a *Agent) errlog(ctx context.Context, format string, args ...any) {
	if a.logger == nil {
		return
	}
	a.logger.ErrorContext(ctx, format, args...)
}

func (a *Agent) debugJSON(ctx context.Context, tag string, v any) {
	if a.logger == nil {
		return
	}
	a.logger.JSONContext(ctx, tag, v)
}

func copyMessages(in []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, len(in))
	copy(out, in)
	return out
}

func isCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
