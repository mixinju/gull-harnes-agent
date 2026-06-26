package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/openai/openai-go"
)

// BashTool 是一个在子进程中执行 bash 命令的工具。
type BashTool struct{}

// NewBashTool 创建一个 BashTool 实例。
func NewBashTool() *BashTool {
	return &BashTool{}
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Description() string {
	return "执行 bash 命令并返回标准输出。" +
		"适用场景：文件列表（ls/cat）、文本处理（grep/wc/sed）、" +
		"代码运行（go build/python）、系统信息查询（uname/pwd/env）等。" +
		"每次调用只执行一条命令，复杂操作请使用 && 或管道拼接。"
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

	// 30 秒超时保护，防止命令挂死
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	// 让子进程继承当前工作目录
	cmd.Dir = "."

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	stdoutStr := strings.TrimSpace(stdout.String())
	stderrStr := strings.TrimSpace(stderr.String())

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

	// 超时处理
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("命令执行超时（30s限制）: %s", command)
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
