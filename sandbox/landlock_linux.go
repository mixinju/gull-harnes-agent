//go:build linux && (amd64 || arm64)

package sandbox

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446

	landlockCreateRulesetVersion = 1
	landlockRulePathBeneath      = 1
	oPath                        = 0o10000000

	accessFSExecute    = 1 << 0
	accessFSWriteFile  = 1 << 1
	accessFSReadFile   = 1 << 2
	accessFSReadDir    = 1 << 3
	accessFSRemoveDir  = 1 << 4
	accessFSRemoveFile = 1 << 5
	accessFSMakeChar   = 1 << 6
	accessFSMakeDir    = 1 << 7
	accessFSMakeReg    = 1 << 8
	accessFSMakeSock   = 1 << 9
	accessFSMakeFIFO   = 1 << 10
	accessFSMakeBlock  = 1 << 11
	accessFSMakeSym    = 1 << 12
	accessFSRefer      = 1 << 13
	accessFSTruncate   = 1 << 14
	accessFSIoctlDev   = 1 << 15

	accessNetBindTCP    = 1 << 0
	accessNetConnectTCP = 1 << 1
)

func init() {
	if os.Getenv("GULL_SANDBOX_HELPER") != "1" {
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "--gull-sandbox-helper" {
		return
	}
	if err := runHelper(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox helper: %s\n", err)
		os.Exit(1)
	}
}

func defaultIsolation() string { return IsolationLandlock }

func probeLandlock() error {
	abi, err := landlockABI()
	if err != nil {
		return err
	}
	if abi < 1 {
		return errors.New("landlock ABI 不可用")
	}
	return nil
}

func landlockCanDenyNetwork() bool {
	abi, err := landlockABI()
	return err == nil && abi >= 4
}

func (s *Sandbox) runLandlock(ctx context.Context, command string, env []string) (string, string, error) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		return "", "", fmt.Errorf("%w: 找不到 bash: %v", ErrIsolation, err)
	}
	spec := helperSpec{
		Writable:         append([]string{s.workspace}, s.allowed...),
		ReadOnly:         append([]string{}, s.readOnly...),
		OptionalReadOnly: systemReadOnly(bash),
		DenyNetwork:      !s.allowNetwork,
		Argv:             []string{bash, "-c", command},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrIsolation, err)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("%w: 找不到当前可执行文件: %v", ErrIsolation, err)
	}
	env = append(append([]string{}, env...),
		"GULL_SANDBOX_HELPER=1",
		"GULL_SANDBOX_SPEC="+string(raw),
	)
	return startCommand(ctx, exe, []string{exe, "--gull-sandbox-helper"}, s.workspace, env, true)
}

type helperSpec struct {
	Writable         []string `json:"writable"`
	ReadOnly         []string `json:"read_only"`
	OptionalReadOnly []string `json:"optional_read_only"`
	DenyNetwork      bool     `json:"deny_network"`
	Argv             []string `json:"argv"`
}

func runHelper() error {
	var spec helperSpec
	if err := json.Unmarshal([]byte(os.Getenv("GULL_SANDBOX_SPEC")), &spec); err != nil {
		return err
	}
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return errors.New("缺少要执行的命令")
	}
	if err := applyRlimits(); err != nil {
		return err
	}
	if err := applyLandlock(spec); err != nil {
		return err
	}
	return syscall.Exec(spec.Argv[0], spec.Argv, childEnv(os.Environ()))
}

func childEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, _, _ := cutEnv(kv)
		if k == "GULL_SANDBOX_HELPER" || k == "GULL_SANDBOX_SPEC" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func cutEnv(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}

func applyRlimits() error {
	limits := []struct {
		res int
		cur uint64
		max uint64
	}{
		{syscall.RLIMIT_CPU, 60, 60},
		{syscall.RLIMIT_CORE, 0, 0},
		{syscall.RLIMIT_NOFILE, 1024, 1024},
	}
	for _, l := range limits {
		r := syscall.Rlimit{Cur: l.cur, Max: l.max}
		if err := syscall.Setrlimit(l.res, &r); err != nil {
			return fmt.Errorf("setrlimit: %w", err)
		}
	}
	return nil
}

func applyLandlock(spec helperSpec) error {
	abi, err := landlockABI()
	if err != nil || abi < 1 {
		return fmt.Errorf("landlock 不可用: %v", err)
	}
	handled := fsHandled(abi)
	var net uint64
	attrSize := uintptr(8)
	if abi >= 4 {
		attrSize = 16
		if spec.DenyNetwork {
			net = accessNetBindTCP | accessNetConnectTCP
		}
	} else if spec.DenyNetwork {
		return errors.New("landlock 不能限制 TCP")
	}
	ruleset, err := createRuleset(handled, net, attrSize)
	if err != nil {
		return err
	}
	defer syscall.Close(ruleset)

	rw := fsReadWrite(abi) & handled
	ro := fsReadOnly(abi) & handled
	if len(spec.Writable) == 0 {
		return errors.New("没有可写路径")
	}
	for _, p := range spec.Writable {
		if err := addPathRule(ruleset, p, rw); err != nil {
			return fmt.Errorf("可写路径 %s: %w", p, err)
		}
	}
	for _, p := range spec.ReadOnly {
		if err := addPathRule(ruleset, p, ro); err != nil {
			return fmt.Errorf("只读路径 %s: %w", p, err)
		}
	}
	for _, p := range spec.OptionalReadOnly {
		// 解释器和系统库不存在或打不开时跳过。后面的 exec 若因此失败，
		// 仍然不会去掉 Landlock 再跑一遍。
		_ = addPathRule(ruleset, p, ro)
	}
	if err := setNoNewPrivs(); err != nil {
		return err
	}
	return restrictSelf(ruleset)
}

func setNoNewPrivs() error {
	// 必须在调用 restrict_self 的同一个线程上设置。AllThreadsSyscall 在带 cgo 的二进制里会直接失败。
	runtime.LockOSThread()
	const prSetNoNewPrivs = 38
	_, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0)
	if errno != 0 {
		return fmt.Errorf("PR_SET_NO_NEW_PRIVS: %w", errno)
	}
	return nil
}

func landlockABI() (int, error) {
	r, _, errno := syscall.Syscall(sysLandlockCreateRuleset, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}

func createRuleset(fs, net uint64, size uintptr) (int, error) {
	buf := make([]byte, size)
	binary.LittleEndian.PutUint64(buf[:8], fs)
	if size >= 16 {
		binary.LittleEndian.PutUint64(buf[8:16], net)
	}
	r, _, errno := syscall.Syscall(
		sysLandlockCreateRuleset,
		uintptr(unsafe.Pointer(&buf[0])),
		size,
		0,
	)
	runtime.KeepAlive(buf)
	if errno != 0 {
		return 0, errno
	}
	syscall.CloseOnExec(int(r))
	return int(r), nil
}

func addPathRule(ruleset int, path string, access uint64) error {
	fd, err := syscall.Open(path, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var buf [12]byte
	binary.LittleEndian.PutUint64(buf[0:8], access)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(fd))
	_, _, errno := syscall.Syscall6(
		sysLandlockAddRule,
		uintptr(ruleset),
		landlockRulePathBeneath,
		uintptr(unsafe.Pointer(&buf[0])),
		0,
		0,
		0,
	)
	runtime.KeepAlive(&buf)
	if errno != 0 {
		return errno
	}
	return nil
}

func restrictSelf(ruleset int) error {
	_, _, errno := syscall.Syscall(sysLandlockRestrictSelf, uintptr(ruleset), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func fsHandled(abi int) uint64 {
	m := uint64(accessFSExecute | accessFSWriteFile | accessFSReadFile | accessFSReadDir |
		accessFSRemoveDir | accessFSRemoveFile | accessFSMakeChar | accessFSMakeDir |
		accessFSMakeReg | accessFSMakeSock | accessFSMakeFIFO | accessFSMakeBlock | accessFSMakeSym)
	if abi >= 2 {
		m |= accessFSRefer
	}
	if abi >= 3 {
		m |= accessFSTruncate
	}
	if abi >= 5 {
		m |= accessFSIoctlDev
	}
	return m
}

func fsReadWrite(abi int) uint64 {
	m := uint64(accessFSExecute | accessFSWriteFile | accessFSReadFile | accessFSReadDir |
		accessFSRemoveDir | accessFSRemoveFile | accessFSMakeDir | accessFSMakeReg | accessFSMakeSym)
	if abi >= 2 {
		m |= accessFSRefer
	}
	if abi >= 3 {
		m |= accessFSTruncate
	}
	if abi >= 5 {
		m |= accessFSIoctlDev
	}
	return m
}

func fsReadOnly(abi int) uint64 {
	m := uint64(accessFSExecute | accessFSReadFile | accessFSReadDir)
	if abi >= 5 {
		m |= accessFSIoctlDev
	}
	return m
}

func systemReadOnly(bash string) []string {
	candidates := []string{
		"/usr", "/lib", "/lib64", "/bin", "/sbin",
		"/etc/ld.so.cache",
		"/etc/ssl", "/etc/ssl/certs", "/etc/ca-certificates",
		"/usr/share/ca-certificates",
		"/dev/null", "/dev/zero", "/dev/urandom", "/dev/tty",
		filepath.Dir(bash),
	}
	seen := make(map[string]struct{}, len(candidates))
	var out []string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		info, err := os.Stat(c)
		if err != nil {
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeDevice == 0 {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}
