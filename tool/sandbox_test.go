package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gull-herness-agent/sandbox"
)

func newTools(t *testing.T, opt sandbox.Options) (*sandbox.Sandbox, *BashTool, *FileReadTool, *FileWriteTool) {
	t.Helper()
	if opt.Workspace == "" {
		opt.Workspace = t.TempDir()
	}
	opt.Enabled = true
	sb, err := sandbox.New(opt)
	if err != nil {
		t.Fatalf("sandbox: %v", err)
	}
	return sb, NewBashTool().SetSandbox(sb), NewFileReadTool().SetSandbox(sb), NewFileWriteTool().SetSandbox(sb)
}

func TestFileToolsStayInsideBubblewrap(t *testing.T) {
	ws := t.TempDir()
	extra := t.TempDir()
	ro := t.TempDir()
	if err := os.WriteFile(filepath.Join(ro, "note.txt"), []byte("read-only"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, read, write := newTools(t, sandbox.Options{
		Workspace:     ws,
		AllowedPaths:  []string{extra},
		ReadOnlyPaths: []string{ro},
	})

	if _, err := write.Execute(map[string]any{"path": "nested/a.txt", "content": "hello"}); err != nil {
		t.Fatal(err)
	}
	got, err := read.Execute(map[string]any{"path": "nested/a.txt"})
	if err != nil || !strings.Contains(got, "hello") {
		t.Fatalf("read = %s err=%v", got, err)
	}
	host, err := os.ReadFile(filepath.Join(ws, "nested", "a.txt"))
	if err != nil || string(host) != "hello" {
		t.Fatalf("host workspace = %q err=%v", host, err)
	}

	if _, err := write.Execute(map[string]any{"path": filepath.Join(extra, "b.txt"), "content": "extra"}); err != nil {
		t.Fatal(err)
	}
	got, err = read.Execute(map[string]any{"path": filepath.Join(extra, "b.txt")})
	if err != nil || !strings.Contains(got, "extra") {
		t.Fatalf("allowed path read=%s err=%v", got, err)
	}

	got, err = read.Execute(map[string]any{"path": filepath.Join(ro, "note.txt")})
	if err != nil || !strings.Contains(got, "read-only") {
		t.Fatalf("read-only read=%s err=%v", got, err)
	}
	if _, err := write.Execute(map[string]any{"path": filepath.Join(ro, "note.txt"), "content": "nope"}); err == nil {
		t.Fatal("read-only write succeeded")
	}
	host, err = os.ReadFile(filepath.Join(ro, "note.txt"))
	if err != nil || string(host) != "read-only" {
		t.Fatalf("read-only host changed to %q err=%v", host, err)
	}

	if _, err := read.Execute(map[string]any{"path": "/etc/passwd"}); err == nil {
		t.Fatal("read /etc/passwd succeeded")
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = write.Execute(map[string]any{"path": secret, "content": "gone"})
	host, err = os.ReadFile(secret)
	if err != nil || string(host) != "keep" {
		t.Fatalf("outside write changed host to %q err=%v", host, err)
	}

	link := filepath.Join(ws, "escape")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	got, err = read.Execute(map[string]any{"path": link})
	if err == nil || strings.Contains(got, "root:") {
		t.Fatalf("symlink result=%s err=%v", got, err)
	}
}

func TestBashToolRunsInsideBubblewrap(t *testing.T) {
	ws := t.TempDir()
	_, bash, _, write := newTools(t, sandbox.Options{Workspace: ws})
	if _, err := write.Execute(map[string]any{"path": "hello.txt", "content": "hello"}); err != nil {
		t.Fatal(err)
	}
	got, err := bash.Execute(map[string]any{"command": "cat hello.txt"})
	if err != nil || !strings.Contains(got, "hello") {
		t.Fatalf("bash read = %s err=%v", got, err)
	}

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = bash.Execute(map[string]any{"command": "rm -rf --no-preserve-root / " + shellQuote(secret)})
	if err != nil {
		t.Logf("rm returned %v", err)
	}
	host, readErr := os.ReadFile(secret)
	if readErr != nil || string(host) != "keep" {
		t.Fatalf("host secret = %q err=%v", host, readErr)
	}
}

func TestDispatchLoopSurvivesSandboxBoundary(t *testing.T) {
	ws := t.TempDir()
	_, bash, read, write := newTools(t, sandbox.Options{Workspace: ws})
	reg := NewRegistry()
	reg.Register(NewWeatherTool())
	reg.Register(bash)
	reg.Register(read)
	reg.Register(write)

	writeArgs, _ := json.Marshal(map[string]any{"path": "note.txt", "content": "kept"})
	if result, err := reg.Dispatch("file_write", string(writeArgs)); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(result, "note.txt") {
		t.Fatalf("write result=%s", result)
	}

	if _, err := reg.Dispatch("file_read", `{"path":"/etc/passwd"}`); err == nil {
		t.Fatal("outside read succeeded")
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"command": "rm -rf --no-preserve-root / " + shellQuote(secret)})
	_, _ = reg.Dispatch("bash", string(args))
	host, err := os.ReadFile(secret)
	if err != nil || string(host) != "keep" {
		t.Fatalf("dispatch rm changed host to %q err=%v", host, err)
	}

	writeArgs, _ = json.Marshal(map[string]any{"path": "note.txt", "content": "kept"})
	if _, err := reg.Dispatch("file_write", string(writeArgs)); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]any{"path": "note.txt"})
	result, err := reg.Dispatch("file_read", string(readArgs))
	if err != nil || !strings.Contains(result, "kept") {
		t.Fatalf("loop did not continue: result=%s err=%v", result, err)
	}
	weather, err := reg.Dispatch("getWeather", `{"city":"北京"}`)
	if err != nil || !strings.Contains(weather, "北京") {
		t.Fatalf("weather=%s err=%v", weather, err)
	}
}

func TestDisabledSandboxWritesHost(t *testing.T) {
	bash := NewBashTool().SetSandbox(sandbox.Disabled())
	target := filepath.Join(t.TempDir(), "host.txt")
	if _, err := bash.Execute(map[string]any{"command": "printf host > " + shellQuote(target)}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "host" {
		t.Fatalf("disabled write = %q err=%v", got, err)
	}
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
