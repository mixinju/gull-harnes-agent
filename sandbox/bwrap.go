package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunCommand 在沙箱里执行一段 bash。沙箱关闭时直接在宿主机执行，保持原来的行为。
func (s *Sandbox) RunCommand(ctx context.Context, command string, timeout time.Duration) (string, string, error) {
	if s == nil {
		return "", "", fmt.Errorf("%w: 沙箱未配置", ErrIsolation)
	}
	ctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	if !s.enabled {
		return startCommand(ctx, "bash", []string{"bash", "-c", command}, ".", nil, false, nil)
	}
	return s.exec(ctx, nil, "/usr/bin/bash", "-c", command)
}

// ReadFile 读取沙箱里能看见的文件。沙箱关闭时直接读宿主机。
func (s *Sandbox) ReadFile(path string) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: 沙箱未配置", ErrIsolation)
	}
	if !s.enabled {
		return os.ReadFile(path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stdout, stderr, err := s.exec(ctx, nil, "/usr/bin/cat", "--", path)
	if err != nil {
		return nil, fileError("读取", path, stderr, err)
	}
	return []byte(stdout), nil
}

// WriteFile 把内容写进沙箱里可写的路径，并创建缺失的父目录。
// 父目录只会出现在沙箱的挂载视图里。
func (s *Sandbox) WriteFile(path, content string) error {
	if s == nil {
		return fmt.Errorf("%w: 沙箱未配置", ErrIsolation)
	}
	if !s.enabled {
		return writeHost(path, content)
	}
	const script = `mkdir -p -- "$(dirname -- "$1")" && cat > "$1"`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, stderr, err := s.exec(ctx, strings.NewReader(content),
		"/usr/bin/bash", "-c", script, "bash", path)
	if err != nil {
		return fileError("写入", path, stderr, err)
	}
	return nil
}

func (s *Sandbox) exec(ctx context.Context, stdin io.Reader, command ...string) (string, string, error) {
	if s.bwrap == "" {
		return "", "", fmt.Errorf("%w: 未配置 bubblewrap", ErrIsolation)
	}
	argv := append(s.bwrapArgs(), command...)
	stdout, stderr, err := startCommand(ctx, s.bwrap, append([]string{s.bwrap}, argv...), "", supervisorEnv(), true, stdin)
	if err != nil && isSandboxStartFailure(err, stderr) {
		return stdout, stderr, fmt.Errorf("%w: %s", ErrIsolation, firstLine(stderr, err))
	}
	return stdout, stderr, err
}

func (s *Sandbox) bwrapArgs() []string {
	args := []string{
		"--unshare-user",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
	}
	if !s.allowNetwork {
		args = append(args, "--unshare-net")
	}
	args = append(args,
		"--ro-bind", "/usr", "/usr",
	)
	if exists("/lib") {
		args = append(args, "--ro-bind", "/lib", "/lib")
	}
	if exists("/lib64") {
		args = append(args, "--ro-bind", "/lib64", "/lib64")
	}
	args = append(args,
		"--symlink", "usr/bin", "/bin",
		"--symlink", "usr/sbin", "/sbin",
		"--dir", "/etc",
	)
	if exists("/etc/ld.so.cache") {
		args = append(args, "--ro-bind", "/etc/ld.so.cache", "/etc/ld.so.cache")
	}
	if exists("/etc/ssl") {
		args = append(args, "--ro-bind", "/etc/ssl", "/etc/ssl")
	}
	args = append(args,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--bind", s.workspace, s.workspace,
	)
	for _, p := range s.readOnly {
		args = append(args, "--ro-bind", p, p)
	}
	for _, p := range s.allowed {
		args = append(args, "--bind", p, p)
	}
	args = append(args,
		"--remount-ro", "/",
		"--chdir", s.workspace,
		"--die-with-parent",
		"--clearenv",
		"--setenv", "PATH", "/usr/bin:/bin",
		"--setenv", "HOME", s.workspace,
		"--setenv", "PWD", s.workspace,
		"--setenv", "LANG", "C",
		"--",
	)
	return args
}

func supervisorEnv() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return []string{"PATH=" + path}
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isSandboxStartFailure(err error, stderr string) bool {
	text := err.Error() + "\n" + stderr
	return strings.Contains(text, "executable file not found") ||
		strings.Contains(text, "bwrap:") && strings.Contains(text, "No such file") && strings.Contains(text, "execvp")
}

func firstLine(stderr string, err error) string {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return stderr
	}
	return err.Error()
}

func fileError(action, path, stderr string, err error) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}
	if strings.Contains(msg, "No such file") || strings.Contains(msg, "Not a directory") {
		return fmt.Errorf("文件不存在: %s", path)
	}
	return fmt.Errorf("%s文件失败: %s", action, msg)
}

func writeHost(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入文件 %s 失败: %w", path, err)
	}
	return nil
}
