package hook

import (
	"context"
	"errors"
	"testing"
)

func TestChainOrderAndHalt(t *testing.T) {
	var seq []string
	c := New().
		OnTurnStart(func(context.Context, TurnEvent) (Decision, error) {
			seq = append(seq, "a")
			return Decision{}, nil
		}).
		OnTurnStart(func(context.Context, TurnEvent) (Decision, error) {
			seq = append(seq, "b")
			return Decision{Halt: true, Reason: "停"}, nil
		}).
		OnTurnStart(func(context.Context, TurnEvent) (Decision, error) {
			seq = append(seq, "c")
			return Decision{}, nil
		})

	d, err := c.BeforeTurn(context.Background(), TurnEvent{Iteration: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Halt || d.Status() != HaltCancelled {
		t.Fatalf("decision = %+v", d)
	}
	if got, want := join(seq), "a,b"; got != want {
		t.Fatalf("顺序 = %s，想要 %s", got, want)
	}
}

func TestObserversKeepGoing(t *testing.T) {
	var seq []string
	c := New().
		OnModelAfter(func(context.Context, ModelEvent) error {
			seq = append(seq, "1")
			return errors.New("观察失败")
		}).
		OnModelAfter(func(context.Context, ModelEvent) error {
			seq = append(seq, "2")
			return nil
		})
	err := c.AfterModel(context.Background(), ModelEvent{})
	if err == nil || join(seq) != "1,2" {
		t.Fatalf("err=%v seq=%v", err, seq)
	}
}

func TestToolBeforeCannotHalt(t *testing.T) {
	c := New().OnToolBefore(func(context.Context, ToolEvent) (Decision, error) {
		return Decision{Halt: true}, nil
	})
	_, _, err := c.BeforeTool(context.Background(), ToolEvent{Name: "bash"})
	if err == nil {
		t.Fatal("tool.before 的 Halt 应被拒绝")
	}
}

func TestToolRewriteThenSkip(t *testing.T) {
	c := New().
		OnToolBefore(func(_ context.Context, e ToolEvent) (Decision, error) {
			if e.Args != `{"n":1}` {
				t.Fatalf("第一个 hook 看到的参数 = %s", e.Args)
			}
			args := `{"n":2}`
			return Decision{Args: &args}, nil
		}).
		OnToolBefore(func(_ context.Context, e ToolEvent) (Decision, error) {
			if e.Args != `{"n":2}` {
				t.Fatalf("第二个 hook 应看到改写后的参数，实际 %s", e.Args)
			}
			return Decision{ReplaceResult: true, Result: "拦住"}, nil
		}).
		OnToolBefore(func(context.Context, ToolEvent) (Decision, error) {
			t.Fatal("替换结果后不应再调用后续 tool.before")
			return Decision{}, nil
		})
	ev, _, err := c.BeforeTool(context.Background(), ToolEvent{Name: "echo", Args: `{"n":1}`})
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Skipped || ev.Result != "拦住" || ev.Args != `{"n":2}` {
		t.Fatalf("event = %+v", ev)
	}
}

func TestMergeAndNilChain(t *testing.T) {
	var seq []string
	a := New().OnError(func(context.Context, ErrorEvent) error {
		seq = append(seq, "a")
		return nil
	})
	b := New().OnError(func(context.Context, ErrorEvent) error {
		seq = append(seq, "b")
		return nil
	})
	if err := Merge(nil, a, b).OnErrorRun(context.Background(), ErrorEvent{}); err != nil {
		t.Fatal(err)
	}
	if join(seq) != "a,b" {
		t.Fatalf("merge 顺序 = %v", seq)
	}
	var c *Chain
	if _, err := c.BeforeTurn(context.Background(), TurnEvent{}); err != nil {
		t.Fatal(err)
	}
}

type sampleHook struct {
	Nop
	seq *[]string
}

func (h sampleHook) OnSessionStart(context.Context, SessionEvent) error {
	*h.seq = append(*h.seq, "start")
	return nil
}

func TestUseStructHook(t *testing.T) {
	var seq []string
	c := New().Use(sampleHook{seq: &seq})
	if err := c.SessionStart(context.Background(), SessionEvent{}); err != nil {
		t.Fatal(err)
	}
	if join(seq) != "start" {
		t.Fatalf("seq = %v", seq)
	}
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
