// Package sandbox 在工具真正执行时做本地隔离。
//
// 它不代替 tool.before hook。hook（见独立的生命周期改动）跑在
// Registry.Dispatch 之前，可以改写或直接拒绝这一次调用；被拒绝的调用
// 不会进入本包。调用一旦开始执行，沙箱仍然限制路径、命令和子进程。
// hook 是事前短路，沙箱是执行时的隔离，两层都保留。本包不实现 hook。
//
// 模型来自《Github狂揽万星！腾讯开源沙箱的生产级Agent实践》里能落到这个
// 进程内 Harness 的部分，主要是芒果 TV 的 Fence：限制文件、网络和环境变量，
// 隔离初始化失败就拒绝执行。Bash 分成 none / restricted / full，对应文中的
// No Bash、Restricted Bash、Full Bash，但不做文中的单次人工审批。
// 子进程在 Linux 上用 Landlock 收紧文件系统和 TCP，并清掉凭据环境变量。
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	BashRestricted = "restricted"
	BashNone       = "none"
	BashFull       = "full"

	IsolationLandlock  = "landlock"
	IsolationUserspace = "userspace"
)

// Options 是调用方可以覆盖的沙箱配置。零值不要直接传给 New，用 DefaultOptions。
type Options struct {
	// Enabled 为 false 时工具保持原来的全开放行为。
	Enabled bool
	// Workspace 是可读写的工作区，相对路径会按进程当前目录解析。
	Workspace string
	// AllowedPaths 是工作区之外额外可读写的目录。
	AllowedPaths []string
	// ReadOnlyPaths 是额外只读目录。file_write 不能写，bash 的 Landlock 规则也是只读。
	ReadOnlyPaths []string
	// BashMode 取 restricted、none、full。空字符串等同 restricted。
	BashMode string
	// AllowNetwork 为 true 时放行网络类命令，并且不让 Landlock 拦截 TCP。
	AllowNetwork bool
	// ProcessIsolation 取 landlock 或 userspace。空字符串按平台默认：
	// Linux amd64/arm64 用 landlock，其它平台用 userspace。
	ProcessIsolation string
	// ExtraDeniedCommands 追加到 restricted 模式的命令名黑名单，只比较 basename。
	ExtraDeniedCommands []string
}

// DefaultOptions 是默认安全配置：开启、工作区为当前目录、restricted bash、禁止网络。
func DefaultOptions() Options {
	return Options{
		Enabled:      true,
		Workspace:    ".",
		BashMode:     BashRestricted,
		AllowNetwork: false,
	}
}

// Sandbox 是一份已经解析好的执行边界。文件工具和 bash 共用同一份。
type Sandbox struct {
	enabled      bool
	workspace    string
	allowed      []string
	readOnly     []string
	bashMode     string
	allowNetwork bool
	isolation    string
	denied       map[string]struct{}
}

// New 校验配置并解析路径。工作区或允许路径不可用，或者要求的内核隔离起不来，
// 都直接返回错误，调用方不应降级成无沙箱执行。
func New(opt Options) (*Sandbox, error) {
	if !opt.Enabled {
		return &Sandbox{enabled: false, bashMode: BashFull, isolation: IsolationUserspace}, nil
	}

	mode := opt.BashMode
	if mode == "" {
		mode = BashRestricted
	}
	switch mode {
	case BashRestricted, BashNone, BashFull:
	default:
		return nil, fmt.Errorf("未知的 bash_mode: %s", mode)
	}

	workspace := opt.Workspace
	if workspace == "" {
		workspace = "."
	}
	workspace, err := resolveExistingDir(workspace)
	if err != nil {
		return nil, fmt.Errorf("沙箱工作区不可用: %w", err)
	}
	allowed, err := resolveExistingDirs(opt.AllowedPaths)
	if err != nil {
		return nil, fmt.Errorf("沙箱允许路径不可用: %w", err)
	}
	readOnly, err := resolveExistingDirs(opt.ReadOnlyPaths)
	if err != nil {
		return nil, fmt.Errorf("沙箱只读路径不可用: %w", err)
	}

	isolation := opt.ProcessIsolation
	if isolation == "" {
		isolation = defaultIsolation()
	}
	switch isolation {
	case IsolationLandlock:
		if err := probeLandlock(); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrIsolation, err.Error())
		}
		if !opt.AllowNetwork && !landlockCanDenyNetwork() {
			return nil, fmt.Errorf("%w: 当前 Landlock 不能限制 TCP", ErrIsolation)
		}
	case IsolationUserspace:
	default:
		return nil, fmt.Errorf("未知的 process_isolation: %s", isolation)
	}

	return &Sandbox{
		enabled:      true,
		workspace:    workspace,
		allowed:      allowed,
		readOnly:     readOnly,
		bashMode:     mode,
		allowNetwork: opt.AllowNetwork,
		isolation:    isolation,
		denied:       deniedCommands(opt.AllowNetwork, opt.ExtraDeniedCommands),
	}, nil
}

// Default 用 DefaultOptions 创建沙箱。
func Default() (*Sandbox, error) {
	return New(DefaultOptions())
}

// Disabled 是显式关闭的沙箱，行为与加固前的工具一致。
func Disabled() *Sandbox {
	sb, err := New(Options{Enabled: false})
	if err != nil {
		// Enabled false 不会失败；这里只是让构造函数保持一个返回值。
		return &Sandbox{enabled: false, bashMode: BashFull, isolation: IsolationUserspace}
	}
	return sb
}

// Enabled 报告沙箱是否生效。
func (s *Sandbox) Enabled() bool {
	if s == nil {
		return false
	}
	return s.enabled
}

// Workspace 返回解析后的工作区绝对路径。未启用时为空。
func (s *Sandbox) Workspace() string { return s.workspace }

// BashMode 返回 none、restricted 或 full。
func (s *Sandbox) BashMode() string {
	if s == nil || s.bashMode == "" {
		return BashFull
	}
	return s.bashMode
}

// Isolation 返回 landlock 或 userspace。
func (s *Sandbox) Isolation() string {
	if s == nil || s.isolation == "" {
		return IsolationUserspace
	}
	return s.isolation
}

// Summary 是启动日志用的一行摘要。
func (s *Sandbox) Summary() string {
	if s == nil || !s.enabled {
		return "enabled=false"
	}
	return fmt.Sprintf("enabled=true workspace=%s bash=%s isolation=%s network=%t",
		s.workspace, s.bashMode, s.isolation, s.allowNetwork)
}

func resolveExistingDir(path string) (string, error) {
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

func resolveExistingDirs(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("路径为空")
		}
		resolved, err := resolveExistingDir(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, resolved)
	}
	return out, nil
}
