// Package sandbox 把工具执行放进本机的 bubblewrap 沙箱。
//
// 隔离由 bubblewrap 的挂载、用户、进程和网络命名空间完成，本包不自己写
// 路径白名单判定、命令黑名单或 Landlock 规则。
//
// tool.before hook 仍然跑在 Registry.Dispatch 之前，可以改写或拒绝这一次
// 调用。调用真的执行时才进入沙箱。两层都留，本包不实现 hook。
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Options 是调用方可以覆盖的沙箱配置。零值不要直接传给 New，用 DefaultOptions。
type Options struct {
	// Enabled 为 false 时工具保持原来的全开放行为。
	Enabled bool
	// Workspace 是放进沙箱并可读写的工作区。
	Workspace string
	// AllowedPaths 是额外挂进沙箱、可读写的目录。
	AllowedPaths []string
	// ReadOnlyPaths 是额外只读挂进沙箱的目录。
	ReadOnlyPaths []string
	// AllowNetwork 为 true 时不使用 bubblewrap 的网络命名空间隔离。
	AllowNetwork bool
}

// DefaultOptions 默认开启沙箱，工作区是当前目录，网络隔离。
func DefaultOptions() Options {
	return Options{
		Enabled:      true,
		Workspace:    ".",
		AllowNetwork: false,
	}
}

// Sandbox 是一份已经解析好的 bubblewrap 运行参数。
type Sandbox struct {
	enabled      bool
	workspace    string
	allowed      []string
	readOnly     []string
	allowNetwork bool
	bwrap        string
}

// New 校验配置。沙箱开启但找不到 bwrap，或工作区不可用，都直接失败，不降级成无沙箱执行。
func New(opt Options) (*Sandbox, error) {
	if !opt.Enabled {
		return &Sandbox{enabled: false}, nil
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("%w: 找不到 bubblewrap (bwrap): %v", ErrIsolation, err)
	}
	workspace := opt.Workspace
	if workspace == "" {
		workspace = "."
	}
	workspace, err = existingDir(workspace)
	if err != nil {
		return nil, fmt.Errorf("沙箱工作区不可用: %w", err)
	}
	allowed, err := existingDirs(opt.AllowedPaths)
	if err != nil {
		return nil, fmt.Errorf("沙箱允许路径不可用: %w", err)
	}
	readOnly, err := existingDirs(opt.ReadOnlyPaths)
	if err != nil {
		return nil, fmt.Errorf("沙箱只读路径不可用: %w", err)
	}
	if _, err := os.Stat("/usr"); err != nil {
		return nil, fmt.Errorf("%w: 宿主机没有 /usr，bubblewrap 无法启动命令", ErrIsolation)
	}
	return &Sandbox{
		enabled:      true,
		workspace:    workspace,
		allowed:      allowed,
		readOnly:     readOnly,
		allowNetwork: opt.AllowNetwork,
		bwrap:        bwrap,
	}, nil
}

// Default 用 DefaultOptions 创建沙箱。
func Default() (*Sandbox, error) {
	return New(DefaultOptions())
}

// Disabled 是显式关闭的沙箱。
func Disabled() *Sandbox {
	return &Sandbox{enabled: false}
}

// Enabled 报告沙箱是否生效。
func (s *Sandbox) Enabled() bool {
	return s != nil && s.enabled
}

// Workspace 返回解析后的工作区绝对路径。
func (s *Sandbox) Workspace() string {
	if s == nil {
		return ""
	}
	return s.workspace
}

// Summary 是启动日志用的一行摘要。
func (s *Sandbox) Summary() string {
	if s == nil || !s.enabled {
		return "enabled=false"
	}
	return fmt.Sprintf("enabled=true runtime=bubblewrap workspace=%s network=%t", s.workspace, s.allowNetwork)
}

func existingDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s 不是目录", resolved)
	}
	return resolved, nil
}

func existingDirs(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		resolved, err := existingDir(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, resolved)
	}
	return out, nil
}
