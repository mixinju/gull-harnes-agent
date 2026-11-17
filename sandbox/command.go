package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

type redirect struct {
	op     string
	target string
}

type simpleCmd struct {
	argv      []string
	redirects []redirect
}

// checkCommand 在 restricted 模式下于进程启动前检查整段脚本。
// 解析不了的语法直接拒绝，避免放行一段没看懂的命令。
func (s *Sandbox) checkCommand(command string) error {
	return s.checkAt(command, 0)
}

func (s *Sandbox) checkAt(command string, depth int) error {
	if depth > 8 {
		return fmt.Errorf("%w: 嵌套过深", ErrRefused)
	}
	if strings.Contains(command, "<<") || strings.Contains(command, "<(") || strings.Contains(command, ">(") || strings.Contains(command, "`") || strings.Contains(command, "()") {
		return fmt.Errorf("%w: 命令包含无法静态确认的语法", ErrRefused)
	}
	rewritten, err := s.extractSubs(command, depth)
	if err != nil {
		return err
	}
	cmds, err := parseCommands(rewritten)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRefused, err)
	}
	for _, cmd := range cmds {
		if err := s.checkSimple(cmd, depth); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sandbox) extractSubs(command string, depth int) (string, error) {
	var b strings.Builder
	i := 0
	inSingle := false
	inDouble := false
	for i < len(command) {
		c := command[i]
		if inSingle {
			b.WriteByte(c)
			if c == '\'' {
				inSingle = false
			}
			i++
			continue
		}
		if c == '\\' && !inDouble && i+1 < len(command) {
			b.WriteByte(c)
			b.WriteByte(command[i+1])
			i += 2
			continue
		}
		if c == '\'' && !inDouble {
			inSingle = true
			b.WriteByte(c)
			i++
			continue
		}
		if c == '"' {
			inDouble = !inDouble
			b.WriteByte(c)
			i++
			continue
		}
		if c == '$' && i+1 < len(command) && command[i+1] == '(' {
			if i+2 < len(command) && command[i+2] == '(' {
				end, err := skipArithmetic(command, i)
				if err != nil {
					return "", fmt.Errorf("%w: %v", ErrRefused, err)
				}
				b.WriteString(command[i:end])
				i = end
				continue
			}
			inner, end, err := sliceCommandSub(command, i)
			if err != nil {
				return "", fmt.Errorf("%w: %v", ErrRefused, err)
			}
			if err := s.checkAt(inner, depth+1); err != nil {
				return "", err
			}
			b.WriteByte(0)
			i = end
			continue
		}
		b.WriteByte(c)
		i++
	}
	if inSingle || inDouble {
		return "", fmt.Errorf("%w: 引号没有闭合", ErrRefused)
	}
	return b.String(), nil
}

func skipArithmetic(s string, i int) (int, error) {
	// i 指向 $((
	if i+2 >= len(s) || s[i+1] != '(' || s[i+2] != '(' {
		return 0, fmt.Errorf("算术展开没有闭合")
	}
	depth := 2
	for j := i + 3; j < len(s); j++ {
		switch s[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("算术展开没有闭合")
}

func sliceCommandSub(s string, i int) (string, int, error) {
	// i 指向 $(
	depth := 0
	inSingle := false
	inDouble := false
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if c == '\\' && j+1 < len(s) {
			j++
			continue
		}
		if c == '\'' && !inDouble {
			inSingle = true
			continue
		}
		if c == '"' {
			inDouble = !inDouble
			continue
		}
		if c == '(' {
			depth++
			continue
		}
		if c == ')' {
			depth--
			if depth == 0 {
				return s[i+2 : j], j + 1, nil
			}
		}
	}
	return "", 0, fmt.Errorf("命令替换没有闭合")
}

func parseCommands(s string) ([]simpleCmd, error) {
	var cmds []simpleCmd
	var cur simpleCmd
	flush := func() {
		if len(cur.argv) > 0 || len(cur.redirects) > 0 {
			cmds = append(cmds, cur)
		}
		cur = simpleCmd{}
	}
	i := 0
	expectRedirect := false
	pendingOp := ""
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		if s[i] == '&' && i+1 < len(s) && s[i+1] == '>' {
			if expectRedirect {
				return nil, fmt.Errorf("连续的重定向")
			}
			pendingOp = "&>"
			i += 2
			expectRedirect = true
			continue
		}
		switch {
		case s[i] == '\n' || s[i] == ';' || s[i] == '(' || s[i] == ')' || s[i] == '{' || s[i] == '}':
			if expectRedirect {
				return nil, fmt.Errorf("重定向缺少目标")
			}
			flush()
			i++
			continue
		case s[i] == '&' || s[i] == '|':
			if expectRedirect {
				return nil, fmt.Errorf("重定向缺少目标")
			}
			flush()
			if i+1 < len(s) && s[i+1] == s[i] {
				i += 2
			} else {
				i++
			}
			continue
		}

		if op, next, ok := redirectAt(s, i); ok {
			if expectRedirect {
				return nil, fmt.Errorf("连续的重定向")
			}
			pendingOp = op
			i = next
			expectRedirect = true
			continue
		}

		word, next, err := readWord(s, i)
		if err != nil {
			return nil, err
		}
		if next == i {
			return nil, fmt.Errorf("无法解析命令")
		}
		i = next
		if expectRedirect {
			cur.redirects = append(cur.redirects, redirect{op: pendingOp, target: word})
			expectRedirect = false
			pendingOp = ""
			continue
		}
		if word != "" {
			cur.argv = append(cur.argv, word)
		}
	}
	if expectRedirect {
		return nil, fmt.Errorf("重定向缺少目标")
	}
	flush()
	return cmds, nil
}

func redirectAt(s string, i int) (string, int, bool) {
	start := i
	if s[i] >= '0' && s[i] <= '9' {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && (s[j] == '>' || s[j] == '<') {
			i = j
		}
	}
	if i >= len(s) {
		return "", 0, false
	}
	switch s[i] {
	case '>':
		i++
		if i < len(s) && (s[i] == '>' || s[i] == '&' || s[i] == '|') {
			i++
		}
		return s[start:i], i, true
	case '<':
		i++
		if i < len(s) && (s[i] == '>' || s[i] == '&') {
			i++
		}
		return s[start:i], i, true
	case '&':
		if i+1 < len(s) && s[i+1] == '>' {
			return s[start : i+2], i + 2, true
		}
	}
	return "", 0, false
}

func readWord(s string, i int) (string, int, error) {
	var b strings.Builder
	for i < len(s) {
		c := s[i]
		switch c {
		case '\\':
			if i+1 >= len(s) {
				return "", 0, fmt.Errorf("反斜杠没有转义目标")
			}
			b.WriteByte(s[i+1])
			i += 2
			continue
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return "", 0, fmt.Errorf("引号没有闭合")
			}
			b.WriteString(s[i+1 : i+1+end])
			i += end + 2
			continue
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					b.WriteByte(s[i+1])
					i += 2
					continue
				}
				b.WriteByte(s[i])
				i++
			}
			if i >= len(s) || s[i] != '"' {
				return "", 0, fmt.Errorf("引号没有闭合")
			}
			i++
			continue
		case ' ', '\t', '\n', ';', '|', '&', '<', '>', '(', ')', '{', '}':
			return b.String(), i, nil
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), i, nil
}

func (s *Sandbox) checkSimple(cmd simpleCmd, depth int) error {
	for _, redir := range cmd.redirects {
		if err := s.checkRedirect(redir); err != nil {
			return err
		}
	}
	argv, nested, err := unwrap(cmd.argv)
	if err != nil {
		return err
	}
	if nested != "" {
		return s.checkAt(nested, depth+1)
	}
	if len(argv) == 0 {
		return nil
	}
	name := filepath.Base(argv[0])
	if isShell(name) {
		script, ok := shellDashC(argv)
		if !ok {
			return fmt.Errorf("%w: 嵌套 shell 无法静态确认", ErrRefused)
		}
		return s.checkAt(script, depth+1)
	}
	if _, bad := s.denied[name]; bad || strings.HasPrefix(name, "mkfs") {
		return fmt.Errorf("%w: 危险命令 %s", ErrRefused, name)
	}
	switch name {
	case "rm":
		return s.checkRm(argv[1:])
	case "chmod", "chown", "chgrp":
		return s.checkModeCommand(name, argv[1:])
	}
	if exemptArgCheck(name) {
		return nil
	}
	for _, arg := range argv[1:] {
		if err := s.checkArgPath(arg, false); err != nil {
			return err
		}
	}
	return nil
}

func unwrap(argv []string) ([]string, string, error) {
	for {
		if len(argv) == 0 {
			return nil, "", nil
		}
		name := filepath.Base(argv[0])
		switch name {
		case "env":
			rest, err := skipEnv(argv)
			if err != nil {
				return nil, "", err
			}
			argv = rest
		case "command":
			if hasFlag(argv[1:], "v") || hasFlag(argv[1:], "V") {
				return nil, "", nil
			}
			argv = skipShortFlags(argv)
		case "builtin", "exec", "nohup", "time":
			argv = skipShortFlags(argv)
		case "nice":
			argv = skipNice(argv)
		case "stdbuf":
			argv = skipStdbuf(argv)
		default:
			return argv, "", nil
		}
	}
}

func skipEnv(argv []string) ([]string, error) {
	i := 1
	for i < len(argv) {
		a := argv[i]
		switch {
		case a == "--":
			return argv[i+1:], nil
		case a == "-i" || a == "-0" || a == "-v":
			i++
		case a == "-u":
			if i+1 >= len(argv) {
				return nil, fmt.Errorf("%w: env -u 缺少参数", ErrRefused)
			}
			i += 2
		case strings.HasPrefix(a, "-"):
			i++
		case isAssignment(a):
			i++
		default:
			return argv[i:], nil
		}
	}
	return nil, nil
}

func skipShortFlags(argv []string) []string {
	i := 1
	for i < len(argv) {
		a := argv[i]
		if a == "--" {
			return argv[i+1:]
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			i++
			continue
		}
		break
	}
	if i >= len(argv) {
		return nil
	}
	return argv[i:]
}

func skipNice(argv []string) []string {
	i := 1
	for i < len(argv) {
		a := argv[i]
		if a == "-n" {
			i += 2
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			i++
			continue
		}
		break
	}
	if i >= len(argv) {
		return nil
	}
	return argv[i:]
}

func skipStdbuf(argv []string) []string {
	i := 1
	for i < len(argv) {
		a := argv[i]
		if a == "-i" || a == "-o" || a == "-e" {
			i += 2
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			i++
			continue
		}
		break
	}
	if i >= len(argv) {
		return nil
	}
	return argv[i:]
}

func hasFlag(argv []string, letter string) bool {
	for _, a := range argv {
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, "-") && strings.Contains(a, letter) && !strings.HasPrefix(a, "--") {
			return true
		}
	}
	return false
}

func isAssignment(s string) bool {
	if s == "" || strings.HasPrefix(s, "=") {
		return false
	}
	eq := strings.IndexByte(s, '=')
	if eq <= 0 || strings.ContainsAny(s[:eq], "/$") {
		return false
	}
	for _, r := range s[:eq] {
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isShell(name string) bool {
	switch name {
	case "bash", "sh", "dash", "zsh", "ksh", "fish", "ash":
		return true
	default:
		return false
	}
}

func shellDashC(argv []string) (string, bool) {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			return "", false
		}
		if a == "-c" {
			if i+1 >= len(argv) {
				return "", false
			}
			return argv[i+1], true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
			rest := a[strings.IndexByte(a, 'c')+1:]
			if rest != "" {
				return rest, true
			}
			if i+1 >= len(argv) {
				return "", false
			}
			return argv[i+1], true
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return "", false
	}
	return "", false
}

func (s *Sandbox) checkRedirect(redir redirect) error {
	target := redir.target
	if target == "" {
		return fmt.Errorf("%w: 重定向缺少目标", ErrRefused)
	}
	if strings.HasPrefix(target, "&") {
		return nil
	}
	if strings.HasPrefix(target, "/dev/sd") || strings.HasPrefix(target, "/dev/nvme") ||
		strings.HasPrefix(target, "/dev/mmc") || target == "/dev/mem" || target == "/dev/kmem" {
		return fmt.Errorf("%w: 危险重定向 %s", ErrRefused, target)
	}
	if target == "/dev/null" || target == "/dev/zero" || target == "/dev/tty" {
		return nil
	}
	write := strings.Contains(redir.op, ">")
	return s.checkArgPathMode(target, write)
}

func (s *Sandbox) checkRm(args []string) error {
	flags, paths := splitFlags(args)
	recursive := false
	for _, f := range flags {
		switch f {
		case "--recursive", "-r", "-R":
			recursive = true
		default:
			if strings.HasPrefix(f, "--") {
				continue
			}
			body := strings.TrimLeft(f, "-")
			if strings.ContainsAny(body, "rR") {
				recursive = true
			}
		}
	}
	for _, p := range paths {
		if isRootish(p) {
			return fmt.Errorf("%w: 危险命令 rm %s", ErrRefused, strings.Join(append([]string{"rm"}, append(flags, paths...)...), " "))
		}
		if !isStatic(p) {
			if recursive {
				return fmt.Errorf("%w: 递归删除的目标无法静态确认: %s", ErrRefused, p)
			}
			continue
		}
		if err := s.checkArgPath(p, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sandbox) checkModeCommand(name string, args []string) error {
	_, operands := splitFlags(args)
	for _, p := range operands {
		if name == "chmod" && looksLikeMode(p) {
			continue
		}
		if isRootish(p) {
			return fmt.Errorf("%w: 危险命令 %s %s", ErrRefused, name, p)
		}
		if !isStatic(p) {
			return fmt.Errorf("%w: %s 的目标无法静态确认: %s", ErrRefused, name, p)
		}
		if err := s.checkArgPath(p, true); err != nil {
			return err
		}
	}
	return nil
}

func splitFlags(args []string) (flags, rest []string) {
	end := false
	for _, a := range args {
		if !end && a == "--" {
			end = true
			continue
		}
		if !end && strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			continue
		}
		rest = append(rest, a)
	}
	return flags, rest
}

func (s *Sandbox) checkArgPath(p string, write bool) error {
	return s.checkArgPathMode(p, write)
}

func (s *Sandbox) checkArgPathMode(p string, write bool) error {
	if p == "" || !isStatic(p) {
		return nil
	}
	if isRootish(p) {
		return fmt.Errorf("%w: 路径 %s", ErrRefused, p)
	}
	if !strings.Contains(p, "/") && p != ".." && p != "." && !strings.HasPrefix(p, "~") {
		return nil
	}
	if _, err := s.Resolve(p, write); err != nil {
		if errors.Is(err, ErrRefused) {
			return err
		}
		return fmt.Errorf("%w: 路径 %s", ErrRefused, p)
	}
	return nil
}

func isStatic(p string) bool {
	return !strings.ContainsAny(p, "\x00*?[") && !strings.Contains(p, "$")
}

func isRootish(p string) bool {
	switch p {
	case "/", "/*", "/.", "/..", "//":
		return true
	}
	if strings.HasPrefix(p, "/*") {
		return true
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		if filepath.Clean(p) == "/" {
			return true
		}
	}
	return false
}

func looksLikeMode(p string) bool {
	if p == "" || strings.Contains(p, "/") {
		return false
	}
	for _, r := range p {
		if unicode.IsDigit(r) {
			continue
		}
		switch r {
		case 'u', 'g', 'o', 'a', '+', '-', '=', 'r', 'w', 'x', 'X', 's', 't':
			continue
		default:
			return false
		}
	}
	return true
}

func exemptArgCheck(name string) bool {
	switch name {
	case "echo", "printf", "true", "false", "pwd", "test", "[", ":",
		"uname", "whoami", "id", "sleep", "export", "alias", "hash",
		"type", "jobs", "fg", "bg", "umask", "ulimit", "readonly",
		"shift", "set", "unset", "local", "return", "exit", "break",
		"continue", "wait", "times", "getopts":
		return true
	default:
		return false
	}
}

func deniedCommands(allowNetwork bool, extra []string) map[string]struct{} {
	names := []string{
		"sudo", "su", "doas", "pkexec",
		"dd",
		"shutdown", "reboot", "poweroff", "halt", "telinit",
		"mount", "umount",
		"chroot",
		"iptables", "ip6tables", "nft", "ufw", "firewall-cmd",
		"useradd", "userdel", "groupadd", "groupdel", "usermod", "passwd", "chpasswd",
		"systemctl", "service",
		"insmod", "modprobe", "rmmod",
		"sysctl",
		"crontab",
		"kill", "killall", "pkill",
		"unshare", "nsenter",
		"xargs", "eval", "source",
		"wipefs", "fdisk", "parted", "mkswap",
	}
	if !allowNetwork {
		names = append(names,
			"curl", "wget", "ssh", "scp", "sftp", "ftp", "rsync",
			"nc", "ncat", "netcat", "telnet", "socat",
		)
	}
	out := make(map[string]struct{}, len(names)+len(extra))
	for _, n := range names {
		out[n] = struct{}{}
	}
	for _, n := range extra {
		n = filepath.Base(strings.TrimSpace(n))
		if n != "" {
			out[n] = struct{}{}
		}
	}
	return out
}
