package sandbox

import "errors"

// ErrRefused 表示命令或路径在启动前被策略拒绝，进程没有执行。
var ErrRefused = errors.New("沙箱拒绝执行")

// ErrIsolation 表示内核或进程隔离没有建立。调用方必须放弃这次执行，不能降级。
var ErrIsolation = errors.New("沙箱隔离失败（fail closed）")
