package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Level 是日志级别别名，对齐 slog.Level。
type Level = slog.Level

// 预定义日志级别，与 slog 保持一致。
// 仅暴露实际使用到的 LevelDebug（文件 Handler 级别过滤用）。
const LevelDebug = slog.LevelDebug

// StepLevel 是自定义级别，介于 Info 和 Warn 之间。
// 用于 Agent Loop 中的关键步骤和决策（迭代开始、工具调用、终止判断），
// 比 Info 更醒目，但不到 Warn 的告急程度。
const StepLevel = slog.Level(2) // Info=0, Warn=4

// Logger 基于 log/slog 实现，采用"双通道"设计：
//
//   - 文件通道：slog JSONHandler，全量日志（含 Debug 和原始 JSON 载荷），写入 logDir/agent.log
//     结构化 JSON 格式，便于后续用 jq 等工具检索分析
//   - 终端通道：直接 fmt.Fprintln，仅 Step/Info/Error 级别，友好文本展示给用户
//     不用 slog.TextHandler 是为了避免 time=/level=/msg= 前缀噪音，保持输出干净
//
// 对外暴露 Step/Info/Error/Debug/JSON 方法，调用方无需感知 slog 细节。
type Logger struct {
	slog *slog.Logger // 文件端：结构化 JSON
	file *os.File
}

// New 创建一个 Logger 实例。
// logDir 是日志文件所在目录，不存在时自动创建。
func New(logDir string) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录 %s 失败: %w", logDir, err)
	}

	logPath := filepath.Join(logDir, "agent.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件 %s 失败: %w", logPath, err)
	}

	// 文件端：JSON 格式，记录 Debug 及以上所有日志
	// ReplaceAttr 把自定义 StepLevel 渲染为 "STEP"，而非数字
	fileHandler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey && a.Value.Any() != nil {
				if level, ok := a.Value.Any().(slog.Level); ok && level == StepLevel {
					a.Value = slog.StringValue("STEP")
				}
			}
			return a
		},
	})

	return &Logger{
		slog: slog.New(fileHandler),
		file: f,
	}, nil
}

// Step 记录关键步骤或决策，同时输出到终端和文件。
// 用于：迭代开始、模型响应摘要、工具调用、终止决策等。
func (l *Logger) Step(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(os.Stdout, msg)
	l.slog.Log(context.Background(), StepLevel, msg)
}

// Info 记录启动阶段的信息，同时输出到终端和文件。
func (l *Logger) Info(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(os.Stdout, msg)
	l.slog.Info(msg)
}

// Error 记录错误信息，同时输出到终端 stderr 和文件。
func (l *Logger) Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(os.Stderr, msg)
	l.slog.Error(msg)
}

// Debug 记录调试细节，仅写入文件（终端不显示，避免噪音）。
func (l *Logger) Debug(format string, args ...any) {
	l.slog.Debug(fmt.Sprintf(format, args...))
}

// JSON 记录原始 JSON 载荷（如 LLM 请求体/响应体），仅写入文件。
// tag 作为属性名，v 作为属性值（会被 slog 序列化为 JSON）。
func (l *Logger) JSON(tag string, v any) {
	l.slog.With(tag, v).Debug("raw payload")
}

// Close 关闭日志文件。
func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// 兼容 io.Closer 接口（保留给可能的外部引用）
var _ io.Closer = (*Logger)(nil)

