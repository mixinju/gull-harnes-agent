// Package hook 提供可组合的生命周期 hook。
//
// 阶段与现有 Agent Loop 一一对应，一次正常迭代的出现顺序是：
//
//	session.start
//	  turn.start
//	    model.before →（模型调用）→ model.after
//	    handoff.before →（上下文移交给摘要器）→ handoff.after   // 仅 token 超阈值
//	    tool.before →（执行工具）→ tool.after                   // 每个工具调用一次
//	  turn.end
//	session.end
//
// error 在失败路径上额外触发，不替代原来的终止。
//
// 两类 hook：
//
//   - 控制类（turn.start、model.before、tool.before、handoff.before）
//     可以短路或改写，但只能走循环里已经有的那条路。
//   - 观察类（其余阶段）返回的 error 只记日志，不改变结果，也不停止后续观察者。
//
// 顺序：同一阶段按注册顺序执行。控制类 hook 返回 error，或作出短路决定时，
// 同阶段后面的 hook 不再执行。改写会先写回事件，下一个 hook 看到的是改写后的输入。
package hook

import (
	"context"

	"github.com/openai/openai-go"
)

// Phase 是生命周期阶段名，也是日志和 error 事件里的标识。
type Phase string

const (
	PhaseSessionStart  Phase = "session.start"
	PhaseSessionEnd    Phase = "session.end"
	PhaseTurnStart     Phase = "turn.start"
	PhaseTurnEnd       Phase = "turn.end"
	PhaseModelBefore   Phase = "model.before"
	PhaseModelAfter    Phase = "model.after"
	PhaseToolBefore    Phase = "tool.before"
	PhaseToolAfter     Phase = "tool.after"
	PhaseHandoffBefore Phase = "handoff.before"
	PhaseHandoffAfter  Phase = "handoff.after"
	PhaseError         Phase = "error"
)

// 短路时允许写入会话的状态。与 session 包的取值一致。
const (
	// HaltCancelled 表示调用方主动停止，不是模型或工具故障。
	HaltCancelled = "cancelled"
	// HaltError 表示把这次运行按失败结束。
	HaltError = "error"
)

// HandoffKind 区分移交种类。本框架只有一种移交：
// 上下文超过阈值时，把历史消息交给 Summarizer。
const HandoffCompact = "compact"

// Decision 是控制类 hook 的决定。零值表示沿当前控制流继续。
type Decision struct {
	// Halt 结束整个 Run。
	// 只允许出现在 turn.start、model.before、handoff.before。
	// tool.before 不能 Halt：工具失败会变成文本回填给模型，循环继续。
	Halt bool
	// HaltStatus 只能是 HaltCancelled 或 HaltError。空值按 HaltCancelled。
	HaltStatus string
	// Reason 写入日志和 span，不参与分支判断。
	Reason string

	// Model 仅 model.before：非 nil 时替换本次请求的模型名。
	// 不修改 Agent 上配置的模型，下一轮恢复原值，除非再次改写。
	Model *string
	// RewriteMessages 仅 model.before：为 true 时，本次发往模型的消息改用 Messages。
	// 这是出站副本，不写回 Context，下一轮仍从 Context 取历史。
	RewriteMessages bool
	Messages        []openai.ChatCompletionMessageParamUnion

	// Args 仅 tool.before：非 nil 时替换即将执行的参数 JSON。
	// 已经记入历史的 assistant tool_call 仍是模型原始参数。
	Args *string
	// ReplaceResult 仅 tool.before：不执行工具，把 Result 当作工具结果回填。
	// 循环不结束。这与「工具执行失败时回填错误文本并继续」是同一条路。
	ReplaceResult bool
	Result        string
}

// Status 返回 Halt 应写入会话的状态。
func (d Decision) Status() string {
	if d.HaltStatus == "" {
		return HaltCancelled
	}
	return d.HaltStatus
}

// validate 拒绝与该阶段控制流不一致的决定。违规当成 hook 错误，Run 按失败结束。
func (d Decision) validate(phase Phase) error {
	switch phase {
	case PhaseTurnStart, PhaseHandoffBefore:
		if d.ReplaceResult || d.Args != nil || d.Model != nil || d.RewriteMessages {
			return phaseError(phase, "只能停止运行，不能改写模型请求或工具")
		}
	case PhaseModelBefore:
		if d.ReplaceResult || d.Args != nil {
			return phaseError(phase, "不能改写工具参数或结果")
		}
	case PhaseToolBefore:
		if d.Halt {
			return phaseError(phase, "不能结束 Run：工具失败会回填文本并继续循环")
		}
		if d.Model != nil || d.RewriteMessages {
			return phaseError(phase, "不能改写模型请求")
		}
	default:
		if d.Halt || d.ReplaceResult || d.Args != nil || d.Model != nil || d.RewriteMessages {
			return phaseError(phase, "是观察阶段，不能短路或改写")
		}
	}
	if d.Halt && d.HaltStatus != "" && d.HaltStatus != HaltCancelled && d.HaltStatus != HaltError {
		return phaseError(phase, "HaltStatus 只能是 cancelled 或 error")
	}
	return nil
}

func phaseError(phase Phase, msg string) error {
	return &InvalidDecision{Phase: phase, Msg: msg}
}

// InvalidDecision 表示 hook 的决定和该阶段的控制流不一致。
type InvalidDecision struct {
	Phase Phase
	Msg   string
}

func (e *InvalidDecision) Error() string {
	return string(e.Phase) + " " + e.Msg
}

// SessionEvent 是会话开始和结束时的只读快照。
type SessionEvent struct {
	SessionID   string
	Model       string
	TraceID     string
	Status      string
	Iterations  int
	TotalTokens int64
}

// TurnEvent 是一轮迭代的快照。Outcome 只在 turn.end 有值。
type TurnEvent struct {
	Iteration     int
	MaxIterations int
	Tokens        int64
	// Outcome 在 turn.end 为 continue、completed 或 length。
	Outcome string
}

// ModelEvent 是一次模型调用。Messages 在 before 阶段是出站副本。
type ModelEvent struct {
	Iteration        int
	Model            string
	Messages         []openai.ChatCompletionMessageParamUnion
	ToolCount        int
	FinishReason     string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Content          string
	ToolCallCount    int
}

// ToolEvent 是一次工具调用。
type ToolEvent struct {
	Iteration int
	CallID    string
	Name      string
	// Args 是即将执行（before）或实际执行（after）的参数 JSON。
	Args string
	// Result 是回填给模型的文本。before 阶段只有 ReplaceResult 之后才有值。
	Result string
	// Skipped 为 true 表示工具没有执行，Result 来自 hook。
	Skipped bool
	// Err 是工具实现返回的错误。回填文本仍然是「工具执行失败: ...」。
	Err error
}

// HandoffEvent 描述把超窗历史移交给摘要器的那一次。
type HandoffEvent struct {
	Kind              string
	Iteration         int
	Tokens            int64
	Threshold         int64
	MessageCount      int
	TokensAfter       int64
	MessageCountAfter int
}

// ErrorEvent 是失败路径上的观察事件。处理它不能改变即将写入的会话状态。
type ErrorEvent struct {
	Phase     Phase
	Iteration int
	Tool      string
	Err       error
}

// Hook 是一次实现全部阶段的方式。嵌入 Nop 后只覆盖关心的方法。
// 想按函数注册时，用 Chain 上的 On* 方法，不必实现这个接口。
type Hook interface {
	OnSessionStart(ctx context.Context, e SessionEvent) error
	OnSessionEnd(ctx context.Context, e SessionEvent) error
	OnTurnStart(ctx context.Context, e TurnEvent) (Decision, error)
	OnTurnEnd(ctx context.Context, e TurnEvent) error
	OnModelBefore(ctx context.Context, e ModelEvent) (Decision, error)
	OnModelAfter(ctx context.Context, e ModelEvent) error
	OnToolBefore(ctx context.Context, e ToolEvent) (Decision, error)
	OnToolAfter(ctx context.Context, e ToolEvent) error
	OnHandoffBefore(ctx context.Context, e HandoffEvent) (Decision, error)
	OnHandoffAfter(ctx context.Context, e HandoffEvent) error
	OnError(ctx context.Context, e ErrorEvent) error
}

// Nop 为 Hook 的全部方法提供空实现。
type Nop struct{}

func (Nop) OnSessionStart(context.Context, SessionEvent) error { return nil }
func (Nop) OnSessionEnd(context.Context, SessionEvent) error   { return nil }
func (Nop) OnTurnStart(context.Context, TurnEvent) (Decision, error) {
	return Decision{}, nil
}
func (Nop) OnTurnEnd(context.Context, TurnEvent) error { return nil }
func (Nop) OnModelBefore(context.Context, ModelEvent) (Decision, error) {
	return Decision{}, nil
}
func (Nop) OnModelAfter(context.Context, ModelEvent) error { return nil }
func (Nop) OnToolBefore(context.Context, ToolEvent) (Decision, error) {
	return Decision{}, nil
}
func (Nop) OnToolAfter(context.Context, ToolEvent) error { return nil }
func (Nop) OnHandoffBefore(context.Context, HandoffEvent) (Decision, error) {
	return Decision{}, nil
}
func (Nop) OnHandoffAfter(context.Context, HandoffEvent) error { return nil }
func (Nop) OnError(context.Context, ErrorEvent) error          { return nil }
