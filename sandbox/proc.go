package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RunCommand 执行一段 bash。restricted 模式先做命令检查，危险命令不会起进程。
// timeout 到点后结束整个进程组。隔离初始化失败时返回 ErrIsolation，不降级重试。
func (s *Sandbox) RunCommand(ctx context.Context, command string, timeout time.Duration) (string, string, error) {
	if s == nil {
		return "", "", fmtError("沙箱未配置")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if !s.enabled {
		return startCommand(ctx, "bash", []string{"bash", "-c", command}, ".", nil, false)
	}
	switch s.bashMode {
	case BashNone:
		return "", "", errors.Join(ErrRefused, errBashNone)
	case BashRestricted:
		if err := s.checkCommand(command); err != nil {
			return "", "", err
		}
	}

	env := s.cleanEnv()
	switch s.isolation {
	case IsolationLandlock:
		return s.runLandlock(ctx, command, env)
	default:
		return startCommand(ctx, "bash", []string{"bash", "-c", command}, s.workspace, env, true)
	}
}

var errBashNone = errors.New("bash 模式为 none，只开放结构化工具")

func (s *Sandbox) cleanEnv() []string {
	allow := map[string]struct{}{
		"PATH": {}, "LANG": {}, "LC_ALL": {}, "LC_CTYPE": {},
		"TERM": {}, "TZ": {}, "USER": {}, "LOGNAME": {},
	}
	var env []string
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, keep := allow[k]; !keep || v == "" && k == "PATH" {
			continue
		}
		env = append(env, kv)
	}
	if !hasKey(env, "PATH") {
		env = append(env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}
	env = append(env,
		"HOME="+s.workspace,
		"PWD="+s.workspace,
		"TMPDIR="+s.workspace,
		"TMP="+s.workspace,
		"TEMP="+s.workspace,
	)
	return env
}

func hasKey(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) && len(kv) > len(prefix) {
			return true
		}
	}
	return false
}

func startCommand(ctx context.Context, name string, argv []string, dir string, env []string, group bool) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	args := []string{}
	if len(argv) > 1 {
		args = argv[1:]
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if group {
		cmd.SysProcAttr = sysProcAttr()
		cmd.Cancel = func() error { return killGroup(cmd) }
		cmd.WaitDelay = 2 * time.Second
	}
	err := cmd.Run()
	out, errOut := stdout.String(), stderr.String()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, errOut, ctx.Err()
	}
	if strings.Contains(errOut, "sandbox helper:") {
		return out, errOut, errors.Join(ErrIsolation, errors.New(strings.TrimSpace(errOut)))
	}
	return out, errOut, err
}

func fmtError(msg string) error {
	return errors.New(msg)
}
