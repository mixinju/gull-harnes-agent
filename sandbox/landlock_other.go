//go:build !linux || (!amd64 && !arm64)

package sandbox

import (
	"context"
	"fmt"
)

func defaultIsolation() string { return IsolationUserspace }

func probeLandlock() error {
	return fmt.Errorf("当前平台没有 Landlock")
}

func landlockCanDenyNetwork() bool { return false }

func (s *Sandbox) runLandlock(ctx context.Context, command string, env []string) (string, string, error) {
	return "", "", fmt.Errorf("%w: 当前平台不能启用 Landlock", ErrIsolation)
}
