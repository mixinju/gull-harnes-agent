package observe

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func toOTELAttrs(attrs []Attr) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, toOTELAttr(a))
	}
	return out
}

func toOTELAttr(a Attr) attribute.KeyValue {
	switch v := a.Value.(type) {
	case string:
		return attribute.String(a.Key, v)
	case int:
		return attribute.Int(a.Key, v)
	case int64:
		return attribute.Int64(a.Key, v)
	case bool:
		return attribute.Bool(a.Key, v)
	case float64:
		return attribute.Float64(a.Key, v)
	default:
		if v == nil {
			return attribute.String(a.Key, "")
		}
		return attribute.String(a.Key, fmt.Sprint(v))
	}
}

func endOTEL(span oteltrace.Span, err error, attrs []Attr) {
	if len(attrs) > 0 {
		span.SetAttributes(toOTELAttrs(attrs)...)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
