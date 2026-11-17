package tool

import (
	"encoding/json"
	"errors"
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

func TestFileToolsAllowWorkspaceAndRejectOutside(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("read = %s", got)
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
	if _, err := write.Execute(map[string]any{"path": filepath.Join(ro, "note.txt"), "content": "nope"}); !errors.Is(err, sandbox.ErrRefused) {
		t.Fatalf("read-only write err=%v", err)
	}

	if _, err := read.Execute(map[string]any{"path": "/etc/passwd"}); !errors.Is(err, sandbox.ErrRefused) {
		t.Fatalf("outside read err=%v", err)
	}
	outside := filepath.Join(t.TempDir(), "nope.txt")
	if _, err := write.Execute(map[string]any{"path": outside, "content": "x"}); !errors.Is(err, sandbox.ErrRefused) {
		t.Fatalf("outside write err=%v", err)
	}
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatal("outside file was created")
	}

	link := filepath.Join(ws, "escape")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	if _, err := read.Execute(map[string]any{"path": link}); !errors.Is(err, sandbox.ErrRefused) {
		t.Fatalf("symlink err=%v", err)
	}
}

func TestBashToolRefusesDangerousAndAllowsWorkspace(t *testing.T) {
	ws := t.TempDir()
	_, bash, _, write := newTools(t, sandbox.Options{Workspace: ws, BashMode: sandbox.BashRestricted})
	if _, err := write.Execute(map[string]any{"path": "hello.txt", "content": "hello"}); err != nil {
		t.Fatal(err)
	}
	got, err := bash.Execute(map[string]any{"command": "cat hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("bash read = %s", got)
	}

	marker := filepath.Join(ws, "marker")
	_, err = bash.Execute(map[string]any{"command": "printf x > marker && rm -rf /"})
	if !errors.Is(err, sandbox.ErrRefused) {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatal("dangerous command started")
	}
}

func TestDispatchLoopSurvivesSandboxDenial(t *testing.T) {
	ws := t.TempDir()
	_, bash, read, write := newTools(t, sandbox.Options{Workspace: ws, BashMode: sandbox.BashRestricted})
	reg := NewRegistry()
	reg.Register(NewWeatherTool())
	reg.Register(bash)
	reg.Register(read)
	reg.Register(write)

	// 与 Agent Loop 一样：按顺序 Dispatch，失败变成错误文本，下一轮工具继续。
	writeArgs, _ := json.Marshal(map[string]any{"path": "note.txt", "content": "kept"})
	if result, err := reg.Dispatch("file_write", string(writeArgs)); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(result, "note.txt") {
		t.Fatalf("write result=%s", result)
	}

	if _, err := reg.Dispatch("file_read", `{"path":"/etc/passwd"}`); !errors.Is(err, sandbox.ErrRefused) {
		t.Fatalf("outside read err=%v", err)
	}
	if _, err := reg.Dispatch("bash", `{"command":"rm -rf /"}`); !errors.Is(err, sandbox.ErrRefused) {
		t.Fatalf("dangerous bash err=%v", err)
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
