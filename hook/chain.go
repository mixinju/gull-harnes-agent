package hook

import (
	"context"
	"errors"
)

// Chain 按注册顺序执行 hook。nil 接收者上的触发方法是空操作，
// 因此 Agent 未配置 hook 时行为与原来一致。
//
// 组合方式：多次 On* 追加，Use 把一个 Hook 的各方法按同一次注册插入，
// Merge 按参数顺序把多条链拼成一条。
type Chain struct {
	sessionStart  []func(context.Context, SessionEvent) error
	sessionEnd    []func(context.Context, SessionEvent) error
	turnStart     []func(context.Context, TurnEvent) (Decision, error)
	turnEnd       []func(context.Context, TurnEvent) error
	modelBefore   []func(context.Context, ModelEvent) (Decision, error)
	modelAfter    []func(context.Context, ModelEvent) error
	toolBefore    []func(context.Context, ToolEvent) (Decision, error)
	toolAfter     []func(context.Context, ToolEvent) error
	handoffBefore []func(context.Context, HandoffEvent) (Decision, error)
	handoffAfter  []func(context.Context, HandoffEvent) error
	onError       []func(context.Context, ErrorEvent) error
}

// New 返回一条空链。
func New() *Chain {
	return &Chain{}
}

// Merge 按参数顺序拼接多条链。nil 链被跳过。
func Merge(parts ...*Chain) *Chain {
	out := New()
	for _, p := range parts {
		if p == nil {
			continue
		}
		out.sessionStart = append(out.sessionStart, p.sessionStart...)
		out.sessionEnd = append(out.sessionEnd, p.sessionEnd...)
		out.turnStart = append(out.turnStart, p.turnStart...)
		out.turnEnd = append(out.turnEnd, p.turnEnd...)
		out.modelBefore = append(out.modelBefore, p.modelBefore...)
		out.modelAfter = append(out.modelAfter, p.modelAfter...)
		out.toolBefore = append(out.toolBefore, p.toolBefore...)
		out.toolAfter = append(out.toolAfter, p.toolAfter...)
		out.handoffBefore = append(out.handoffBefore, p.handoffBefore...)
		out.handoffAfter = append(out.handoffAfter, p.handoffAfter...)
		out.onError = append(out.onError, p.onError...)
	}
	return out
}

// Use 注册一个 Hook。各方法按固定阶段归位，相对顺序与这次 Use 的调用顺序一致。
func (c *Chain) Use(h Hook) *Chain {
	if c == nil || h == nil {
		return c
	}
	c.OnSessionStart(h.OnSessionStart)
	c.OnSessionEnd(h.OnSessionEnd)
	c.OnTurnStart(h.OnTurnStart)
	c.OnTurnEnd(h.OnTurnEnd)
	c.OnModelBefore(h.OnModelBefore)
	c.OnModelAfter(h.OnModelAfter)
	c.OnToolBefore(h.OnToolBefore)
	c.OnToolAfter(h.OnToolAfter)
	c.OnHandoffBefore(h.OnHandoffBefore)
	c.OnHandoffAfter(h.OnHandoffAfter)
	c.OnError(h.OnError)
	return c
}

func (c *Chain) OnSessionStart(fn func(context.Context, SessionEvent) error) *Chain {
	if c != nil && fn != nil {
		c.sessionStart = append(c.sessionStart, fn)
	}
	return c
}

func (c *Chain) OnSessionEnd(fn func(context.Context, SessionEvent) error) *Chain {
	if c != nil && fn != nil {
		c.sessionEnd = append(c.sessionEnd, fn)
	}
	return c
}

func (c *Chain) OnTurnStart(fn func(context.Context, TurnEvent) (Decision, error)) *Chain {
	if c != nil && fn != nil {
		c.turnStart = append(c.turnStart, fn)
	}
	return c
}

func (c *Chain) OnTurnEnd(fn func(context.Context, TurnEvent) error) *Chain {
	if c != nil && fn != nil {
		c.turnEnd = append(c.turnEnd, fn)
	}
	return c
}

func (c *Chain) OnModelBefore(fn func(context.Context, ModelEvent) (Decision, error)) *Chain {
	if c != nil && fn != nil {
		c.modelBefore = append(c.modelBefore, fn)
	}
	return c
}

func (c *Chain) OnModelAfter(fn func(context.Context, ModelEvent) error) *Chain {
	if c != nil && fn != nil {
		c.modelAfter = append(c.modelAfter, fn)
	}
	return c
}

func (c *Chain) OnToolBefore(fn func(context.Context, ToolEvent) (Decision, error)) *Chain {
	if c != nil && fn != nil {
		c.toolBefore = append(c.toolBefore, fn)
	}
	return c
}

func (c *Chain) OnToolAfter(fn func(context.Context, ToolEvent) error) *Chain {
	if c != nil && fn != nil {
		c.toolAfter = append(c.toolAfter, fn)
	}
	return c
}

func (c *Chain) OnHandoffBefore(fn func(context.Context, HandoffEvent) (Decision, error)) *Chain {
	if c != nil && fn != nil {
		c.handoffBefore = append(c.handoffBefore, fn)
	}
	return c
}

func (c *Chain) OnHandoffAfter(fn func(context.Context, HandoffEvent) error) *Chain {
	if c != nil && fn != nil {
		c.handoffAfter = append(c.handoffAfter, fn)
	}
	return c
}

func (c *Chain) OnError(fn func(context.Context, ErrorEvent) error) *Chain {
	if c != nil && fn != nil {
		c.onError = append(c.onError, fn)
	}
	return c
}

// SessionStart 运行观察者。返回的 error 已合并，调用方只记录、不改变会话结果。
func (c *Chain) SessionStart(ctx context.Context, e SessionEvent) error {
	if c == nil {
		return nil
	}
	return runObservers(c.sessionStart, ctx, e)
}

// SessionEnd 同 SessionStart。
func (c *Chain) SessionEnd(ctx context.Context, e SessionEvent) error {
	if c == nil {
		return nil
	}
	return runObservers(c.sessionEnd, ctx, e)
}

// BeforeTurn 在本轮模型调用之前。Halt 时不再调用后续 turn.start hook。
func (c *Chain) BeforeTurn(ctx context.Context, e TurnEvent) (Decision, error) {
	if c == nil {
		return Decision{}, nil
	}
	for _, fn := range c.turnStart {
		d, err := fn(ctx, e)
		if err != nil {
			return Decision{}, err
		}
		if err := d.validate(PhaseTurnStart); err != nil {
			return Decision{}, err
		}
		if d.Halt {
			return d, nil
		}
	}
	return Decision{}, nil
}

// AfterTurn 在本轮正常结束时运行（继续循环、给出最终回复、或 finish_reason=length）。
// 失败和短路路径不调用它，那些路径走 OnError 或直接结束会话。
func (c *Chain) AfterTurn(ctx context.Context, e TurnEvent) error {
	if c == nil {
		return nil
	}
	return runObservers(c.turnEnd, ctx, e)
}

// BeforeModel 在发起请求之前。改写累积到返回的 ModelEvent 上。
func (c *Chain) BeforeModel(ctx context.Context, e ModelEvent) (ModelEvent, Decision, error) {
	if c == nil {
		return e, Decision{}, nil
	}
	for _, fn := range c.modelBefore {
		d, err := fn(ctx, e)
		if err != nil {
			return e, Decision{}, err
		}
		if err := d.validate(PhaseModelBefore); err != nil {
			return e, Decision{}, err
		}
		if d.Model != nil {
			e.Model = *d.Model
		}
		if d.RewriteMessages {
			e.Messages = d.Messages
		}
		if d.Halt {
			return e, d, nil
		}
	}
	return e, Decision{}, nil
}

// AfterModel 观察模型响应。error 不改变响应。
func (c *Chain) AfterModel(ctx context.Context, e ModelEvent) error {
	if c == nil {
		return nil
	}
	return runObservers(c.modelAfter, ctx, e)
}

// BeforeTool 在执行工具之前。ReplaceResult 会跳过工具，并跳过后续 tool.before hook。
// 参数改写写回 ToolEvent.Args，下一个 hook 和真正的执行都使用改写后的值。
func (c *Chain) BeforeTool(ctx context.Context, e ToolEvent) (ToolEvent, Decision, error) {
	if c == nil {
		return e, Decision{}, nil
	}
	for _, fn := range c.toolBefore {
		d, err := fn(ctx, e)
		if err != nil {
			return e, Decision{}, err
		}
		if err := d.validate(PhaseToolBefore); err != nil {
			return e, Decision{}, err
		}
		if d.Args != nil {
			e.Args = *d.Args
		}
		if d.ReplaceResult {
			e.Result = d.Result
			e.Skipped = true
			return e, d, nil
		}
	}
	return e, Decision{}, nil
}

// AfterTool 观察工具结果。error 不改变回填文本。
func (c *Chain) AfterTool(ctx context.Context, e ToolEvent) error {
	if c == nil {
		return nil
	}
	return runObservers(c.toolAfter, ctx, e)
}

// BeforeHandoff 在 Compact 之前。Halt 表示不移交并结束运行，
// 与「压缩后仍超阈值就终止」一样，都不会带着超窗上下文继续循环。
func (c *Chain) BeforeHandoff(ctx context.Context, e HandoffEvent) (Decision, error) {
	if c == nil {
		return Decision{}, nil
	}
	for _, fn := range c.handoffBefore {
		d, err := fn(ctx, e)
		if err != nil {
			return Decision{}, err
		}
		if err := d.validate(PhaseHandoffBefore); err != nil {
			return Decision{}, err
		}
		if d.Halt {
			return d, nil
		}
	}
	return Decision{}, nil
}

// AfterHandoff 观察移交结果。error 不改变压缩后的上下文。
func (c *Chain) AfterHandoff(ctx context.Context, e HandoffEvent) error {
	if c == nil {
		return nil
	}
	return runObservers(c.handoffAfter, ctx, e)
}

// OnErrorRun 运行错误观察者。返回值只供日志使用。
func (c *Chain) OnErrorRun(ctx context.Context, e ErrorEvent) error {
	if c == nil {
		return nil
	}
	return runObservers(c.onError, ctx, e)
}

func runObservers[E any](fns []func(context.Context, E) error, ctx context.Context, e E) error {
	var errs []error
	for _, fn := range fns {
		if err := fn(ctx, e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
