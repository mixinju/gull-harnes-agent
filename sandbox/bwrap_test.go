package sandbox

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gull-herness-agent/config"
)

func newTestSandbox(t *testing.T, opt Options) *Sandbox {
	t.Helper()
	if opt.Workspace == "" {
		opt.Workspace = t.TempDir()
	}
	opt.Enabled = true
	sb, err := New(opt)
	if err != nil {
		t.Fatalf("sandbox: %v", err)
	}
	return sb
}

func TestBubblewrapHidesHostAndKeepsMounts(t *testing.T) {
	ws := t.TempDir()
	extra := t.TempDir()
	ro := t.TempDir()
	if err := os.WriteFile(filepath.Join(ro, "note.txt"), []byte("read-only"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb := newTestSandbox(t, Options{
		Workspace:     ws,
		AllowedPaths:  []string{extra},
		ReadOnlyPaths: []string{ro},
	})

	if err := sb.WriteFile(filepath.Join(ws, "nested", "a.txt"), "hello"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(ws, "nested", "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("workspace write = %q err=%v", got, err)
	}
	got, err = sb.ReadFile("nested/a.txt")
	if err != nil || string(got) != "hello" {
		t.Fatalf("relative read = %q err=%v", got, err)
	}

	if err := sb.WriteFile(filepath.Join(extra, "b.txt"), "extra"); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(extra, "b.txt"))
	if err != nil || string(got) != "extra" {
		t.Fatalf("allowed write = %q err=%v", got, err)
	}

	got, err = sb.ReadFile(filepath.Join(ro, "note.txt"))
	if err != nil || string(got) != "read-only" {
		t.Fatalf("read-only read = %q err=%v", got, err)
	}
	if err := sb.WriteFile(filepath.Join(ro, "note.txt"), "nope"); err == nil {
		t.Fatal("read-only write succeeded")
	}
	got, err = os.ReadFile(filepath.Join(ro, "note.txt"))
	if err != nil || string(got) != "read-only" {
		t.Fatalf("read-only host changed to %q err=%v", got, err)
	}

	if _, err := sb.ReadFile("/etc/passwd"); err == nil || !strings.Contains(err.Error(), "文件不存在") {
		t.Fatalf("passwd err=%v", err)
	}
	link := filepath.Join(ws, "escape")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	if data, err := sb.ReadFile(link); err == nil || strings.Contains(string(data), "root:") {
		t.Fatalf("symlink data=%q err=%v", data, err)
	}

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = sb.RunCommand(context.Background(), "rm -rf --no-preserve-root / "+shellQuote(secret)+" || true", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(secret)
	if err != nil || string(got) != "keep" {
		t.Fatalf("host secret = %q err=%v", got, err)
	}
	if _, _, err := sb.RunCommand(context.Background(), "printf gone > "+shellQuote(secret), 5*time.Second); err != nil {
		t.Logf("write outside returned %v", err)
	}
	got, err = os.ReadFile(secret)
	if err != nil || string(got) != "keep" {
		t.Fatalf("host secret after printf = %q err=%v", got, err)
	}
}

func TestBubblewrapNetworkAndCredentials(t *testing.T) {
	sb := newTestSandbox(t, Options{})
	t.Setenv("GULL_OPENAI_API_KEY", "secret-key")
	out, _, err := sb.RunCommand(context.Background(), `printf '%s' "$GULL_OPENAI_API_KEY"`, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("credential leaked: %q", out)
	}

	if accepted := dialLocal(t, sb); accepted {
		t.Fatal("default sandbox accepted a TCP connection")
	}

	open := newTestSandbox(t, Options{Workspace: sb.Workspace(), AllowNetwork: true})
	if accepted := dialLocal(t, open); !accepted {
		t.Fatal("allow_network sandbox did not reach the listener")
	}
}

func TestBubblewrapTimeoutKillsCommand(t *testing.T) {
	ws := t.TempDir()
	sb := newTestSandbox(t, Options{Workspace: ws})
	_, _, err := sb.RunCommand(context.Background(), "sleep 2 && touch slept", 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout")
	}
	time.Sleep(2200 * time.Millisecond)
	if _, statErr := os.Stat(filepath.Join(ws, "slept")); !os.IsNotExist(statErr) {
		t.Fatal("timed out command still created the marker")
	}
}

func TestOptionsFromConfig(t *testing.T) {
	opt, err := OptionsFromConfig(nil)
	if err != nil || !opt.Enabled || opt.AllowNetwork {
		t.Fatalf("nil config opt=%#v err=%v", opt, err)
	}
	enabled := false
	network := true
	opt, err = OptionsFromConfig(&config.SandboxConfig{Enabled: &enabled, AllowNetwork: &network})
	if err != nil || opt.Enabled || !opt.AllowNetwork {
		t.Fatalf("explicit opt=%#v err=%v", opt, err)
	}
}

func TestDisabledSandboxUsesHost(t *testing.T) {
	sb := Disabled()
	target := filepath.Join(t.TempDir(), "host.txt")
	if _, _, err := sb.RunCommand(context.Background(), "printf host > "+shellQuote(target), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "host" {
		t.Fatalf("disabled write = %q err=%v", got, err)
	}
}

func dialLocal(t *testing.T, sb *Sandbox) bool {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	accepted := make(chan struct{})
	go func() {
		_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(2 * time.Second))
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
			close(accepted)
		}
	}()
	_, _, _ = sb.RunCommand(context.Background(),
		"echo hi >/dev/tcp/127.0.0.1/"+itoa(port), 3*time.Second)
	select {
	case <-accepted:
		return true
	case <-time.After(500 * time.Millisecond):
		return false
	}
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
