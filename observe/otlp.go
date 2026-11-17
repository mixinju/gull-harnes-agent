package observe

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// StartOTLP 在设置了 OTEL_EXPORTER_OTLP_ENDPOINT 时创建 OTLP/HTTP 导出器。
// 环境变量为空则返回 nil，不访问网络。导出器本身由 OpenTelemetry SDK 按标准环境变量配置。
func StartOTLP(ctx context.Context) (oteltrace.Tracer, func(context.Context) error, error) {
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == "" {
		return nil, nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 OTLP 导出器失败: %w", err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	shutdown := func(ctx context.Context) error {
		return provider.Shutdown(ctx)
	}
	return provider.Tracer("gull-herness-agent"), shutdown, nil
}
