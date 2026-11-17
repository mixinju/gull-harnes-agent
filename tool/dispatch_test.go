package tool

import (
	"context"
	"testing"
	"time"

	"github.com/openai/openai-go"
)

type execTool struct{}

func (execTool) Name() string        { return "plain" }
func (execTool) Description() string { return "plain" }
func (execTool) Schema() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{Name: "plain"}
}
func (execTool) Execute(args map[string]any) (string, error) {
	return args["v"].(string), nil
}

func TestDispatchContextFallsBackToExecute(t *testing.T) {
	r := NewRegistry()
	r.Register(execTool{})
	got, err := r.DispatchContext(context.Background(), "plain", `{"v":"ok"}`)
	if err != nil || got != "ok" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	got, err = r.Dispatch("plain", `{"v":"ok"}`)
	if err != nil || got != "ok" {
		t.Fatalf("Dispatch got=%q err=%v", got, err)
	}
}

func TestBashStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := NewBashTool().ExecuteContext(ctx, map[string]any{"command": "sleep 30"})
	if err == nil {
		t.Fatal("已取消的 context 不应把命令跑完")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("取消后仍等待了 %s", time.Since(start))
	}
	if ctx.Err() == nil {
		t.Fatal("context 应处于取消状态")
	}
}
