package sandbox

import "errors"

// ErrIsolation 表示 bubblewrap 没有把这次执行放进沙箱。调用方不能改成直接在宿主机执行。
var ErrIsolation = errors.New("沙箱隔离失败（fail closed）")
