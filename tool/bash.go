package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gull-herness-agent/sandbox"

	"github.com/openai/openai-go"
)

// BashTool 是一个在子进程中执行 bash 命令的工具。
// 沙箱在 Execute 里生效。tool.before hook 如果拒绝了这次调用，不会走到这里。
type BashTool struct {
	sb    *sandbox.Sandbox
	sbErr error
}

// NewBashTool 创建一个带默认沙箱的 BashTool。
func NewBashTool() *BashTool {
	sb, err := sandbox.Default()
	return &BashTool{sb: sb, sbErr: err}
}

// SetSandbox 替换沙箱。传 sandbox.Disabled() 可以回到全开放行为。
func (t *BashTool) SetSandbox(sb *sandbox.Sandbox) *BashTool {
	t.sb = sb
	t.sbErr = nil
	return t
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Description() string {
	desc := "执行 bash 命令并返回标准输出。" +
		"适用场景：文件列表（ls/cat）、文本处理（grep/wc/sed）、" +
		"代码运行（go build/python）、系统信息查询（uname/pwd/env）等。" +
		"每次调用只执行一条命令，复杂操作请使用 && 或管道拼接。"
	if t != nil && t.sb != nil && t.sb.Enabled() && t.sb.BashMode() == sandbox.BashRestricted {
		desc += "命令在本地沙箱中执行：危险命令会被拒绝，文件访问限制在工作区和允许路径内。"
	}
	return desc
}

func (t *BashTool) Schema() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        t.Name(),
		Description: openai.String(t.Description()),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "要执行的 bash 命令，可通过管道（|）和重定向（>/<）组合多个操作。",
				},
			},
			"required": []string{"command"},
		},
	}
}

func (t *BashTool) Execute(args map[string]any) (string, error) {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return "", fmt.Errorf("缺少必填参数: command")
	}

	sb, err := t.sandbox()
	if err != nil {
		return "", err
	}

	// 30 秒超时保护，防止命令挂死。
	// 策略拒绝和隔离失败在进程启动前或启动时返回，不会降级成无沙箱执行。
	const timeout = 30 * time.Second
	stdout, stderr, err := sb.RunCommand(context.Background(), command, timeout)
	if errors.Is(err, context.DeadlineExceeded) {
		return "", fmt.Errorf("命令执行超时（30s限制）: %s", command)
	}
	if errors.Is(err, sandbox.ErrRefused) || errors.Is(err, sandbox.ErrIsolation) {
		return "", err
	}
	stdoutStr := strings.TrimSpace(stdout)
	stderrStr := strings.TrimSpace(stderr)

	// 输出截断，避免超长输出撑爆上下文窗口
	const maxOutput = 4000
	if len(stdoutStr) > maxOutput {
		stdoutStr = stdoutStr[:maxOutput] +
			fmt.Sprintf("\n...（输出过长，已截断至 %d 字符）", maxOutput)
	}
	if len(stderrStr) > 800 {
		stderrStr = stderrStr[:800] + "...（已截断）"
	}

	// 组合输出
	var result strings.Builder
	if stdoutStr != "" {
		result.WriteString(stdoutStr)
	}
	if stderrStr != "" {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("[stderr]\n")
		result.WriteString(stderrStr)
	}

	// 命令执行失败（非零退出码）
	if err != nil {
		if result.Len() == 0 {
			return "", fmt.Errorf("命令执行失败: %v", err)
		}
		// 即使失败，也返回已有输出（stderr 可能包含错误诊断信息）
		result.WriteString(fmt.Sprintf("\n[命令执行失败: %v]", err))
	}

	if result.Len() == 0 {
		return "(命令执行成功，无输出)", nil
	}

	return result.String(), nil
}

func (t *BashTool) sandbox() (*sandbox.Sandbox, error) {
	if t == nil {
		return nil, fmt.Errorf("%w: bash 工具未初始化", sandbox.ErrIsolation)
	}
	if t.sbErr != nil {
		return nil, t.sbErr
	}
	if t.sb == nil {
		return nil, fmt.Errorf("%w: bash 沙箱未配置", sandbox.ErrIsolation)
	}
	return t.sb, nil
}
