package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

func startCommand(ctx context.Context, name string, argv []string, dir string, env []string, group bool, stdin io.Reader) (string, string, error) {
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
	if stdin != nil {
		cmd.Stdin = stdin
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
	return out, errOut, err
}
