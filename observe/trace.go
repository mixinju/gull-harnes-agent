// Package observe 是进程内的 trace 和指标。
//
// 不引入采集器也能用：span 留在内存里，Agent 结束时把它们写进现有的 JSON 日志。
// 需要接到现有 OpenTelemetry 链路时，调用 Tracer.UseOTEL，或在设置了
// OTEL_EXPORTER_OTLP_ENDPOINT 时由 StartOTLP 创建导出器。未配置时不会建连。
package observe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"
)

type idKey struct{}

type ids struct {
	traceID string
	spanID  string
}

// WithIDs 把当前 span 的标识放进 context。日志从这里取 trace_id。
func WithIDs(ctx context.Context, traceID, spanID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, idKey{}, ids{traceID: traceID, spanID: spanID})
}

// IDs 返回 context 上的 trace id 和 span id。没有时都是空串。
func IDs(ctx context.Context) (traceID, spanID string) {
	if ctx == nil {
		return "", ""
	}
	v, _ := ctx.Value(idKey{}).(ids)
	return v.traceID, v.spanID
}

// Attr 是 span 上的一个属性。
type Attr struct {
	Key   string
	Value any
}

// String、Int、Int64、Bool 是属性构造函数。
func String(key, value string) Attr      { return Attr{Key: key, Value: value} }
func Int(key string, value int) Attr     { return Attr{Key: key, Value: value} }
func Int64(key string, value int64) Attr { return Attr{Key: key, Value: value} }
func Bool(key string, value bool) Attr   { return Attr{Key: key, Value: value} }

// FinishedSpan 是已经结束的 span，可序列化进日志。
type FinishedSpan struct {
	Name       string         `json:"name"`
	TraceID    string         `json:"trace_id"`
	SpanID     string         `json:"span_id"`
	ParentID   string         `json:"parent_id,omitempty"`
	Start      time.Time      `json:"start"`
	End        time.Time      `json:"end"`
	DurationMs int64          `json:"duration_ms"`
	Attrs      map[string]any `json:"attrs,omitempty"`
	Err        string         `json:"error,omitempty"`
}

// Exporter 在 span 结束时收到一份副本。nil 表示只留在进程内。
type Exporter interface {
	ExportSpan(span FinishedSpan)
}

// Tracer 记录父子 span。零值不可用，用 NewTracer 创建。
type Tracer struct {
	mu       sync.Mutex
	finished []FinishedSpan
	exporter Exporter
	otel     oteltrace.Tracer
}

// NewTracer 创建一个空的 tracer。
func NewTracer() *Tracer {
	return &Tracer{}
}

// SetExporter 设置结束时的导出器。传 nil 表示取消。
func (t *Tracer) SetExporter(e Exporter) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.exporter = e
	t.mu.Unlock()
}

// UseOTEL 把后续 span 镜像到一条 OpenTelemetry trace。
// 传入 nil 则关闭。未调用时没有任何 OTEL 依赖被激活。
// 启用后，日志里的 trace id / span id 与 OTEL span context 相同。
func (t *Tracer) UseOTEL(tr oteltrace.Tracer) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.otel = tr
	t.mu.Unlock()
}

// Start 开始一个 span，并把它设为 ctx 的当前 span。
// name 使用 session、turn、model、tool、handoff，与循环的控制流同名。
func (t *Tracer) Start(ctx context.Context, name string, attrs ...Attr) (context.Context, *Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil {
		t = NewTracer()
	}
	parentTrace, parentSpan := IDs(ctx)
	traceID := parentTrace
	if traceID == "" {
		traceID = newID(16)
	}
	spanID := newID(8)

	t.mu.Lock()
	otelTracer := t.otel
	t.mu.Unlock()

	var otelSpan oteltrace.Span
	if otelTracer != nil {
		ctx, otelSpan = otelTracer.Start(ctx, name, oteltrace.WithAttributes(toOTELAttrs(attrs)...))
		sc := otelSpan.SpanContext()
		if sc.HasTraceID() {
			traceID = sc.TraceID().String()
		}
		if sc.HasSpanID() {
			spanID = sc.SpanID().String()
		}
	}

	ctx = WithIDs(ctx, traceID, spanID)
	s := &Span{
		Name:     name,
		TraceID:  traceID,
		SpanID:   spanID,
		ParentID: parentSpan,
		Start:    time.Now(),
		attrs:    attrMap(attrs),
		otel:     otelSpan,
		owner:    t,
	}
	return ctx, s
}

// Finished 按结束顺序返回 span 副本。
func (t *Tracer) Finished() []FinishedSpan {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]FinishedSpan, len(t.finished))
	copy(out, t.finished)
	return out
}

// Span 是一个尚未结束或刚结束的 span。
type Span struct {
	Name     string
	TraceID  string
	SpanID   string
	ParentID string
	Start    time.Time

	mu    sync.Mutex
	attrs map[string]any
	otel  oteltrace.Span
	owner *Tracer
	done  bool
}

// End 正常结束 span。
func (s *Span) End(attrs ...Attr) {
	s.end(nil, attrs)
}

// EndError 以错误结束 span。err 为 nil 时与 End 相同。
func (s *Span) EndError(err error, attrs ...Attr) {
	s.end(err, attrs)
}

func (s *Span) end(err error, attrs []Attr) {
	if s == nil || s.owner == nil {
		return
	}
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	if s.attrs == nil {
		s.attrs = map[string]any{}
	}
	for _, a := range attrs {
		s.attrs[a.Key] = a.Value
	}
	finished := FinishedSpan{
		Name:     s.Name,
		TraceID:  s.TraceID,
		SpanID:   s.SpanID,
		ParentID: s.ParentID,
		Start:    s.Start,
		End:      time.Now(),
		Attrs:    s.attrs,
	}
	finished.DurationMs = finished.End.Sub(finished.Start).Milliseconds()
	if err != nil {
		finished.Err = err.Error()
	}
	otelSpan := s.otel
	owner := s.owner
	s.mu.Unlock()

	if otelSpan != nil {
		endOTEL(otelSpan, err, attrs)
	}

	owner.mu.Lock()
	owner.finished = append(owner.finished, finished)
	exp := owner.exporter
	owner.mu.Unlock()
	if exp != nil {
		exp.ExportSpan(finished)
	}
}

func attrMap(attrs []Attr) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		m[a.Key] = a.Value
	}
	return m
}

func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 极端情况下退回时间戳，保证仍然能把日志串起来。
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000")))
	}
	return hex.EncodeToString(b)
}
