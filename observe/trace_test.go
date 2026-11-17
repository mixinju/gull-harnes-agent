package observe

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestSpanNesting(t *testing.T) {
	tr := NewTracer()
	ctx, session := tr.Start(context.Background(), "session", String("model", "m"))
	ctx, turn := tr.Start(ctx, "turn", Int("iteration", 1))
	_, model := tr.Start(ctx, "model")
	model.End(Int64("tokens.total", 12))
	_, tool := tr.Start(ctx, "tool", String("tool.name", "bash"))
	tool.EndError(context.Canceled)
	turn.End()
	session.End()

	finished := tr.Finished()
	if len(finished) != 4 {
		t.Fatalf("span 数 = %d", len(finished))
	}
	byName := map[string]FinishedSpan{}
	for _, s := range finished {
		byName[s.Name] = s
		if s.TraceID != session.TraceID {
			t.Fatalf("%s trace = %s，根 trace = %s", s.Name, s.TraceID, session.TraceID)
		}
		if s.DurationMs < 0 {
			t.Fatalf("%s duration = %d", s.Name, s.DurationMs)
		}
	}
	if byName["turn"].ParentID != byName["session"].SpanID {
		t.Fatal("turn 的父 span 不是 session")
	}
	if byName["model"].ParentID != byName["turn"].SpanID || byName["tool"].ParentID != byName["turn"].SpanID {
		t.Fatal("model 和 tool 应嵌在 turn 里")
	}
	if byName["model"].Attrs["tokens.total"] != int64(12) {
		t.Fatalf("model attrs = %#v", byName["model"].Attrs)
	}
	if byName["tool"].Err == "" {
		t.Fatal("tool span 应记录错误")
	}
}

func TestOTELBridgeUsesSameIDs(t *testing.T) {
	rec := &spanRecorder{}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	tr := NewTracer()
	tr.UseOTEL(provider.Tracer("gull-test"))
	ctx, session := tr.Start(context.Background(), "session")
	_, turn := tr.Start(ctx, "turn")
	time.Sleep(time.Millisecond)
	turn.End()
	session.End()

	finished := tr.Finished()
	if len(finished) != 2 || finished[0].Name != "turn" || finished[1].Name != "session" {
		t.Fatalf("finished = %+v", finished)
	}
	if finished[0].ParentID != finished[1].SpanID || finished[0].TraceID != finished[1].TraceID {
		t.Fatalf("内置 span 未嵌套: %+v", finished)
	}

	var otelTurn, otelSession sdktrace.ReadOnlySpan
	for _, s := range rec.spans {
		switch s.Name() {
		case "turn":
			otelTurn = s
		case "session":
			otelSession = s
		}
	}
	if otelTurn == nil || otelSession == nil {
		t.Fatalf("otel spans = %d", len(rec.spans))
	}
	if otelTurn.Parent().SpanID().String() != otelSession.SpanContext().SpanID().String() {
		t.Fatal("OTEL 子 span 没有挂到 session 上")
	}
	if otelTurn.SpanContext().TraceID().String() != finished[0].TraceID {
		t.Fatalf("日志 trace id %s 与 OTEL %s 不一致", finished[0].TraceID, otelTurn.SpanContext().TraceID())
	}
	if otelTurn.SpanContext().SpanID().String() != finished[0].SpanID {
		t.Fatal("span id 与 OTEL 不一致")
	}
}

func TestStartOTLPDisabledWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	tr, shutdown, err := StartOTLP(context.Background())
	if err != nil || tr != nil || shutdown != nil {
		t.Fatalf("未配置端点时不应创建导出器: tr=%v shutdownNil=%v err=%v", tr, shutdown == nil, err)
	}
}

func TestMetricsAccumulate(t *testing.T) {
	m := NewMetrics()
	m.ObserveModel(20*time.Millisecond, 3, 4, 7, false)
	m.ObserveModel(5*time.Millisecond, 0, 0, 0, true)
	m.ObserveTool(2*time.Millisecond, true)
	m.ObserveTool(1*time.Millisecond, false)
	m.ObserveHandoff()
	m.ObserveTurn(30 * time.Millisecond)
	m.ObserveSession(40 * time.Millisecond)
	got := m.Snapshot()
	if got.ModelCalls != 2 || got.ModelErrors != 1 || got.TotalTokens != 7 || got.PromptTokens != 3 {
		t.Fatalf("model = %+v", got)
	}
	if got.ToolCalls != 2 || got.ToolErrors != 1 || got.Handoffs != 1 || got.Turns != 1 {
		t.Fatalf("tool/turn = %+v", got)
	}
	if got.ModelLatencyMs < 20 || got.SessionDurationMs < 40 {
		t.Fatalf("latency = %+v", got)
	}
}

type spanRecorder struct {
	spans []sdktrace.ReadOnlySpan
}

func (r *spanRecorder) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (r *spanRecorder) OnEnd(s sdktrace.ReadOnlySpan) {
	r.spans = append(r.spans, s)
}

func (r *spanRecorder) Shutdown(context.Context) error { return nil }

func (r *spanRecorder) ForceFlush(context.Context) error { return nil }
