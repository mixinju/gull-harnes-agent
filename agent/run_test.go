package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"os"

	"gull-herness-agent/hook"
	"gull-herness-agent/logger"
	"gull-herness-agent/observe"
	"gull-herness-agent/session"
	"gull-herness-agent/tool"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func TestRunFinalAnswerWithoutHooks(t *testing.T) {
	var calls int
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeChat(w, chatBody("最终答案", "stop", 8, 4, "", "", ""))
	})
	sess := session.New("test-model", "你好")
	tr := observe.NewTracer()
	metrics := observe.NewMetrics()
	ag := agentWith(client, sess, tr, metrics, nil, "你好", 3, 0)
	ag.Run()

	if sess.Status != session.StatusCompleted || sess.Iterations != 1 || sess.TotalTokens != 12 {
		t.Fatalf("session = %+v", sess)
	}
	if calls != 1 {
		t.Fatalf("模型调用 %d 次", calls)
	}
	if !hasContent(sess, "最终答案") {
		t.Fatalf("消息 = %+v", sess.Messages)
	}
	snap := metrics.Snapshot()
	if snap.ModelCalls != 1 || snap.ModelErrors != 0 || snap.TotalTokens != 12 || snap.ToolCalls != 0 {
		t.Fatalf("metrics = %+v", snap)
	}
	assertNested(t, tr, false)
	if sess.TraceID == "" {
		t.Fatal("会话应记下 trace id")
	}
}

func TestHookOrderShortCircuitAndObserver(t *testing.T) {
	var calls int
	var mu sync.Mutex
	var phases []string
	record := func(name string) {
		mu.Lock()
		phases = append(phases, name)
		mu.Unlock()
	}
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeChat(w, chatBody("", "tool_calls", 5, 5, "echo", `{"text":"hi"}`, "call_1"))
			return
		}
		writeChat(w, chatBody("做完了", "stop", 6, 2, "", "", ""))
	})
	tool := &recordingTool{name: "echo", result: "echoed"}
	reg := registryWith(tool)
	hooks := hook.New().
		OnSessionStart(func(context.Context, hook.SessionEvent) error { record("session.start"); return nil }).
		OnTurnStart(func(_ context.Context, e hook.TurnEvent) (hook.Decision, error) {
			record("turn.start")
			return hook.Decision{}, nil
		}).
		OnModelBefore(func(context.Context, hook.ModelEvent) (hook.Decision, error) {
			record("model.before")
			return hook.Decision{}, nil
		}).
		OnModelAfter(func(context.Context, hook.ModelEvent) error {
			record("model.after")
			return errString("观察失败")
		}).
		OnToolBefore(func(context.Context, hook.ToolEvent) (hook.Decision, error) {
			record("tool.before")
			return hook.Decision{}, nil
		}).
		OnToolAfter(func(context.Context, hook.ToolEvent) error { record("tool.after"); return nil }).
		OnTurnEnd(func(context.Context, hook.TurnEvent) error { record("turn.end"); return nil }).
		OnSessionEnd(func(_ context.Context, e hook.SessionEvent) error {
			record("session.end:" + e.Status)
			return errString("结束观察失败")
		})

	sess := session.New("test-model", "跑一下")
	ag := agentWith(client, sess, observe.NewTracer(), observe.NewMetrics(), hooks, "跑一下", 4, 0)
	ag.registry = reg
	ag.Run()

	if sess.Status != session.StatusCompleted {
		t.Fatalf("观察 hook 的错误不应改变结果，status=%s", sess.Status)
	}
	if !hasContent(sess, "做完了") || !hasContent(sess, "echoed") {
		t.Fatalf("消息 = %+v", sess.Messages)
	}
	want := "session.start,turn.start,model.before,model.after,tool.before,tool.after,turn.end,turn.start,model.before,model.after,turn.end,session.end:completed"
	if got := strings.Join(phases, ","); got != want {
		t.Fatalf("hook 顺序\n得到 %s\n想要 %s", got, want)
	}
}

func TestTurnHaltSkipsModelAndLaterHooks(t *testing.T) {
	var calls int
	var phases []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeChat(w, chatBody("不应出现", "stop", 1, 1, "", "", ""))
	})
	hooks := hook.New().
		OnSessionStart(func(context.Context, hook.SessionEvent) error { phases = append(phases, "session.start"); return nil }).
		OnTurnStart(func(context.Context, hook.TurnEvent) (hook.Decision, error) {
			phases = append(phases, "first")
			return hook.Decision{Halt: true, Reason: "到此为止"}, nil
		}).
		OnTurnStart(func(context.Context, hook.TurnEvent) (hook.Decision, error) {
			phases = append(phases, "second")
			return hook.Decision{}, nil
		}).
		OnModelBefore(func(context.Context, hook.ModelEvent) (hook.Decision, error) {
			phases = append(phases, "model")
			return hook.Decision{}, nil
		}).
		OnSessionEnd(func(context.Context, hook.SessionEvent) error { phases = append(phases, "session.end"); return nil })
	sess := session.New("test-model", "停")
	ag := agentWith(client, sess, observe.NewTracer(), observe.NewMetrics(), hooks, "停", 3, 0)
	ag.Run()
	if calls != 0 || sess.Status != session.StatusCancelled || sess.Iterations != 0 {
		t.Fatalf("calls=%d status=%s iterations=%d", calls, sess.Status, sess.Iterations)
	}
	if got := strings.Join(phases, ","); got != "session.start,first,session.end" {
		t.Fatalf("短路后的 hook = %s", got)
	}
}

func TestToolRewriteAndSkip(t *testing.T) {
	var bodies []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			writeChat(w, chatBody("", "tool_calls", 4, 4, "echo", `{"text":"原参数"}`, "call_9"))
			return
		}
		writeChat(w, chatBody("收到拦住", "stop", 3, 3, "", "", ""))
	})
	tool := &recordingTool{name: "echo", result: "不应执行"}
	hooks := hook.New().OnToolBefore(func(context.Context, hook.ToolEvent) (hook.Decision, error) {
		args := `{"text":"改写"}`
		return hook.Decision{Args: &args, ReplaceResult: true, Result: "拦住了"}, nil
	})
	sess := session.New("test-model", "用工具")
	metrics := observe.NewMetrics()
	ag := agentWith(client, sess, observe.NewTracer(), metrics, hooks, "用工具", 4, 0)
	ag.registry = registryWith(tool)
	ag.Run()

	if len(tool.calls) != 0 {
		t.Fatalf("工具被执行了 %d 次: %v", len(tool.calls), tool.calls)
	}
	if !hasContent(sess, "拦住了") || !hasContent(sess, "收到拦住") {
		t.Fatalf("消息 = %+v", sess.Messages)
	}
	if sess.Status != session.StatusCompleted {
		t.Fatalf("status = %s", sess.Status)
	}
	var assistantArgs string
	for _, m := range sess.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			assistantArgs = m.ToolCalls[0].Args
		}
	}
	if assistantArgs != `{"text":"原参数"}` {
		t.Fatalf("历史里的模型参数被改掉了: %s", assistantArgs)
	}
	snap := metrics.Snapshot()
	if snap.ToolCalls != 1 || snap.ToolErrors != 0 {
		t.Fatalf("短路替换不应算工具错误: %+v", snap)
	}
}

func TestToolErrorContinuesAndIsCounted(t *testing.T) {
	var calls int
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeChat(w, chatBody("", "tool_calls", 2, 2, "echo", `{"text":"x"}`, "call_e"))
			return
		}
		writeChat(w, chatBody("根据失败继续", "stop", 2, 2, "", "", ""))
	})
	tool := &recordingTool{name: "echo", err: errString("坏了")}
	var sawToolErr bool
	hooks := hook.New().OnError(func(_ context.Context, e hook.ErrorEvent) error {
		if e.Tool == "echo" {
			sawToolErr = true
			return errString("观察错误失败")
		}
		return nil
	})
	sess := session.New("test-model", "试错")
	metrics := observe.NewMetrics()
	ag := agentWith(client, sess, observe.NewTracer(), metrics, hooks, "试错", 4, 0)
	ag.registry = registryWith(tool)
	ag.Run()
	if sess.Status != session.StatusCompleted || !sawToolErr {
		t.Fatalf("status=%s saw=%v", sess.Status, sawToolErr)
	}
	if !hasContent(sess, "工具执行失败:") || !hasContent(sess, "根据失败继续") {
		t.Fatalf("消息 = %+v", sess.Messages)
	}
	if metrics.Snapshot().ToolErrors != 1 {
		t.Fatalf("metrics = %+v", metrics.Snapshot())
	}
}

func TestModelRewriteDoesNotTouchContext(t *testing.T) {
	var body string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		writeChat(w, chatBody("好", "stop", 1, 1, "", "", ""))
	})
	hooks := hook.New().OnModelBefore(func(_ context.Context, e hook.ModelEvent) (hook.Decision, error) {
		model := "override-model"
		msgs := append(append([]openai.ChatCompletionMessageParamUnion{}, e.Messages...), openai.UserMessage("HOOK_ONLY"))
		return hook.Decision{Model: &model, RewriteMessages: true, Messages: msgs}, nil
	})
	sess := session.New("test-model", "原文")
	ag := agentWith(client, sess, observe.NewTracer(), observe.NewMetrics(), hooks, "原文", 2, 0)
	ag.Run()
	if !strings.Contains(body, "override-model") || !strings.Contains(body, "HOOK_ONLY") {
		t.Fatalf("出站请求没有改写: %s", body)
	}
	if hasContent(sess, "HOOK_ONLY") {
		t.Fatal("出站改写写进了会话历史")
	}
	if sess.Model != "test-model" {
		t.Fatalf("会话模型被改成 %s", sess.Model)
	}
}

func TestHandoffCompactAndHalt(t *testing.T) {
	t.Run("compact", func(t *testing.T) {
		var calls int
		var phases []string
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				writeChat(w, chatBody("", "tool_calls", 4, 6, "echo", `{"text":"a"}`, "call_h"))
				return
			}
			writeChat(w, chatBody("压缩后回复", "stop", 40, 60, "", "", ""))
		})
		hooks := hook.New().
			OnHandoffBefore(func(_ context.Context, e hook.HandoffEvent) (hook.Decision, error) {
				phases = append(phases, e.Kind)
				if e.MessageCount < 3 {
					t.Fatalf("移交前消息太少: %+v", e)
				}
				return hook.Decision{}, nil
			}).
			OnHandoffAfter(func(_ context.Context, e hook.HandoffEvent) error {
				phases = append(phases, "after")
				if e.MessageCountAfter >= e.MessageCount {
					t.Fatalf("压缩没有减少消息: %+v", e)
				}
				return nil
			})
		tr := observe.NewTracer()
		metrics := observe.NewMetrics()
		sess := session.New("test-model", "长对话")
		ag := agentWith(client, sess, tr, metrics, hooks, "长对话", 4, 50)
		ag.registry = registryWith(&recordingTool{name: "echo", result: "a"})
		ag.Run()
		if sess.Status != session.StatusCompleted {
			t.Fatalf("status = %s", sess.Status)
		}
		if strings.Join(phases, ",") != "compact,after" {
			t.Fatalf("handoff = %v", phases)
		}
		if metrics.Snapshot().Handoffs != 1 {
			t.Fatalf("metrics = %+v", metrics.Snapshot())
		}
		assertNested(t, tr, true)
	})

	t.Run("halt", func(t *testing.T) {
		var calls int
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				writeChat(w, chatBody("", "tool_calls", 4, 6, "echo", `{"text":"a"}`, "call_h"))
				return
			}
			writeChat(w, chatBody("不应再执行工具之后的回复被用到", "stop", 80, 80, "", "", ""))
		})
		before := 0
		hooks := hook.New().OnHandoffBefore(func(_ context.Context, e hook.HandoffEvent) (hook.Decision, error) {
			before = e.MessageCount
			return hook.Decision{Halt: true, HaltStatus: hook.HaltCancelled, Reason: "不要摘要"}, nil
		})
		sess := session.New("test-model", "长对话")
		ag := agentWith(client, sess, observe.NewTracer(), observe.NewMetrics(), hooks, "长对话", 4, 50)
		ag.registry = registryWith(&recordingTool{name: "echo", result: "a"})
		ag.Run()
		if sess.Status != session.StatusCancelled {
			t.Fatalf("status = %s", sess.Status)
		}
		if len(sess.Messages) != before {
			t.Fatalf("拒绝移交后消息数变了: before=%d now=%d %+v", before, len(sess.Messages), sess.Messages)
		}
	})
}

func TestCancelBeforeAndDuringModel(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		var calls int
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			writeChat(w, chatBody("x", "stop", 1, 1, "", "", ""))
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sess := session.New("test-model", "取消")
		ag := agentWith(client, sess, observe.NewTracer(), observe.NewMetrics(), nil, "取消", 3, 0)
		ag.RunContext(ctx)
		if calls != 0 || sess.Status != session.StatusCancelled {
			t.Fatalf("calls=%d status=%s", calls, sess.Status)
		}
	})

	t.Run("during", func(t *testing.T) {
		rt := &cancelTripper{started: make(chan struct{})}
		client := openai.NewClient(
			option.WithAPIKey("test"),
			option.WithBaseURL("http://example.com/v1/"),
			option.WithMaxRetries(0),
			option.WithHTTPClient(&http.Client{Transport: rt}),
		)
		ctx, cancel := context.WithCancel(context.Background())
		sess := session.New("test-model", "取消")
		metrics := observe.NewMetrics()
		var got error
		hooks := hook.New().OnError(func(_ context.Context, e hook.ErrorEvent) error {
			got = e.Err
			return nil
		})
		ag := agentWith(client, sess, observe.NewTracer(), metrics, hooks, "取消", 3, 0)
		done := make(chan struct{})
		go func() {
			ag.RunContext(ctx)
			close(done)
		}()
		select {
		case <-rt.started:
			cancel()
		case <-time.After(2 * time.Second):
			t.Fatal("请求没有发出")
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("取消后 Run 没有返回")
		}
		if sess.Status != session.StatusCancelled {
			t.Fatalf("status=%s err=%v", sess.Status, got)
		}
		if metrics.Snapshot().ModelErrors != 0 {
			t.Fatalf("取消不应计入模型错误: %+v", metrics.Snapshot())
		}
	})
}

func TestExistingStopConditions(t *testing.T) {
	t.Run("api error", func(t *testing.T) {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		})
		sess := session.New("test-model", "失败")
		metrics := observe.NewMetrics()
		ag := agentWith(client, sess, observe.NewTracer(), metrics, nil, "失败", 3, 0)
		ag.Run()
		if sess.Status != session.StatusError || sess.Iterations != 0 {
			t.Fatalf("session = %+v", sess)
		}
		if metrics.Snapshot().ModelErrors != 1 {
			t.Fatalf("metrics = %+v", metrics.Snapshot())
		}
	})

	t.Run("length", func(t *testing.T) {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChat(w, chatBody("只写了一半", "length", 3, 9, "", "", ""))
		})
		sess := session.New("test-model", "长")
		ag := agentWith(client, sess, observe.NewTracer(), observe.NewMetrics(), nil, "长", 3, 0)
		ag.Run()
		if sess.Status != session.StatusCompleted || sess.Iterations != 1 || !hasContent(sess, "只写了一半") {
			t.Fatalf("session = %+v", sess)
		}
	})

	t.Run("max iterations", func(t *testing.T) {
		var calls int
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			writeChat(w, chatBody("", "tool_calls", 1, 1, "echo", `{"text":"loop"}`, "call_m"))
		})
		sess := session.New("test-model", "循环")
		ag := agentWith(client, sess, observe.NewTracer(), observe.NewMetrics(), nil, "循环", 2, 0)
		ag.registry = registryWith(&recordingTool{name: "echo", result: "ok"})
		ag.Run()
		if sess.Status != session.StatusCompleted || sess.Iterations != 2 || calls != 2 {
			t.Fatalf("status=%s iterations=%d calls=%d", sess.Status, sess.Iterations, calls)
		}
	})

	t.Run("empty choices", func(t *testing.T) {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"chatcmpl-empty","object":"chat.completion","created":0,"model":"test-model","choices":[]}`)
		})
		sess := session.New("test-model", "空")
		ag := agentWith(client, sess, observe.NewTracer(), observe.NewMetrics(), nil, "空", 2, 0)
		ag.Run()
		if sess.Status != session.StatusError {
			t.Fatalf("status = %s", sess.Status)
		}
	})
}

func TestLogCarriesTraceID(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChat(w, chatBody("日志", "stop", 2, 2, "", "", ""))
	})
	dir := t.TempDir()
	lg, err := logger.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	sess := session.New("test-model", "看日志")
	tr := observe.NewTracer()
	ag := agentWith(client, sess, tr, observe.NewMetrics(), nil, "看日志", 2, 0)
	ag.logger = lg
	ag.Run()
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(mustOpen(t, dir+"/agent.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if sess.TraceID == "" || !strings.Contains(text, sess.TraceID) {
		t.Fatalf("日志里没有 trace id %s\n%s", sess.TraceID, text)
	}
	if !strings.Contains(text, `"span_id"`) {
		t.Fatalf("日志里没有 span id\n%s", text)
	}
}

func agentWith(client openai.Client, sess *session.Session, tr *observe.Tracer, metrics *observe.Metrics, hooks *hook.Chain, input string, maxIter int, threshold int64) *Agent {
	opts := []Option{
		WithModel("test-model"),
		WithRegistry(registryWith()),
		WithUserInput(input),
		WithMaxIterations(maxIter),
		WithSession(sess),
		WithTracer(tr),
		WithMetrics(metrics),
		WithHooks(hooks),
	}
	if threshold > 0 {
		opts = append(opts, WithContext(NewContext(WithThreshold(threshold))))
	}
	return New(client, opts...)
}

func registryWith(tools ...*recordingTool) *tool.Registry {
	r := tool.NewRegistry()
	if len(tools) == 0 {
		r.Register(&recordingTool{name: "echo", result: "ok"})
		return r
	}
	for _, item := range tools {
		r.Register(item)
	}
	return r
}

type recordingTool struct {
	name   string
	result string
	err    error
	calls  []string
}

func (t *recordingTool) Name() string        { return t.name }
func (t *recordingTool) Description() string { return t.name }
func (t *recordingTool) Schema() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        t.name,
		Description: openai.String("test tool"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
		},
	}
}
func (t *recordingTool) Execute(args map[string]any) (string, error) {
	raw, _ := json.Marshal(args)
	t.calls = append(t.calls, string(raw))
	if t.err != nil {
		return "", t.err
	}
	if t.result != "" {
		return t.result, nil
	}
	return "ok", nil
}

type errString string

func (e errString) Error() string { return string(e) }

func testClient(t *testing.T, h http.HandlerFunc) openai.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL(srv.URL+"/v1/"),
		option.WithMaxRetries(0),
	)
}

func writeChat(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func chatBody(content, finish string, prompt, completion int64, toolName, toolArgs, toolID string) string {
	msg := map[string]any{
		"role":    "assistant",
		"content": content,
		"refusal": "",
	}
	if toolName != "" {
		msg["tool_calls"] = []map[string]any{{
			"id":   toolID,
			"type": "function",
			"function": map[string]any{
				"name":      toolName,
				"arguments": toolArgs,
			},
		}}
	}
	body := map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1,
		"model":   "test-model",
		"choices": []map[string]any{{
			"index":         0,
			"finish_reason": finish,
			"logprobs":      nil,
			"message":       msg,
		}},
		"usage": map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      prompt + completion,
		},
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func hasContent(sess *session.Session, needle string) bool {
	for _, m := range sess.Messages {
		if strings.Contains(m.Content, needle) {
			return true
		}
	}
	return false
}

func assertNested(t *testing.T, tr *observe.Tracer, wantHandoff bool) {
	t.Helper()
	finished := tr.Finished()
	byID := map[string]observe.FinishedSpan{}
	var root observe.FinishedSpan
	var sawHandoff bool
	for _, s := range finished {
		byID[s.SpanID] = s
		if s.Name == "session" {
			root = s
		}
		if s.Name == "handoff" {
			sawHandoff = true
		}
	}
	if root.SpanID == "" {
		t.Fatal("没有 session span")
	}
	for _, s := range finished {
		if s.TraceID != root.TraceID {
			t.Fatalf("%s 的 trace id 与 session 不同", s.Name)
		}
		switch s.Name {
		case "session":
		case "turn":
			if s.ParentID != root.SpanID {
				t.Fatalf("turn 的父 span 是 %s", s.ParentID)
			}
		case "model", "tool", "handoff":
			parent, ok := byID[s.ParentID]
			if !ok || parent.Name != "turn" {
				t.Fatalf("%s 没有嵌在 turn 里", s.Name)
			}
		default:
			t.Fatalf("未知 span %s", s.Name)
		}
	}
	if wantHandoff != sawHandoff {
		t.Fatalf("handoff span 出现=%v，想要 %v", sawHandoff, wantHandoff)
	}
}

// cancelTripper 在请求的 context 结束前不返回，用来确认取消能传到模型调用。
type cancelTripper struct {
	started chan struct{}
	once    sync.Once
}

func (c *cancelTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.once.Do(func() { close(c.started) })
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func mustOpen(t *testing.T, path string) io.Reader {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
