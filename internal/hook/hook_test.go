package hook

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
)

func managedGoRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// git repo (Stop scan needs one; harmless for post-edit).
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@e"}, {"config", "user.name", "t"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutSingle,
		Capabilities: lockfile.Capabilities{Agents: []string{"claude"}},
		Projects: []lockfile.Project{{
			Name: "app", Language: lockfile.LangGo, ModulePath: "example.com/app",
			Features: lockfile.Features{Lint: true, Test: true},
		}},
	}
	b, _ := lockfile.Marshal(lock)
	mustWrite(t, root, "harness.lock.json", string(b))
	mustWrite(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	return root
}

func mustWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPostEditBlocksOnBadFormat(t *testing.T) {
	cases := []struct {
		name  string
		host  string
		stdin string
	}{
		{
			"claude Write", "claude",
			`{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"bad.go"}}`,
		},
		{
			"codex apply_patch", "codex",
			`{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"apply_patch","tool_input":{"file_path":"bad.go"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := managedGoRepo(t)
			mustWrite(t, root, "bad.go", "package app\nfunc  X( ){\nreturn\n}\n")
			var stderr bytes.Buffer
			code := Run(EventPostEdit, tc.host, root, strings.NewReader(tc.stdin), &bytes.Buffer{}, &stderr)
			if code != 2 {
				t.Fatalf("expected exit 2, got %d; stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "lint failed") {
				t.Fatalf("expected feedback on stderr, got %q", stderr.String())
			}
		})
	}
}

func TestPostEditPassesWellFormatted(t *testing.T) {
	root := managedGoRepo(t)
	mustWrite(t, root, "good.go", "package app\n\nfunc X() {}\n")
	stdin := `{"tool_name":"Write","tool_input":{"file_path":"good.go"}}`
	var stderr bytes.Buffer
	if code := Run(EventPostEdit, "claude", root, strings.NewReader(stdin), &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("expected 0, got %d; stderr=%s", code, stderr.String())
	}
}

func TestPostEditIgnoresNonSingleFileTools(t *testing.T) {
	root := managedGoRepo(t)
	mustWrite(t, root, "bad.go", "package app\nfunc  X( ){}\n")
	// A Bash tool call carries no single file_path; post-edit must no-op.
	stdin := `{"tool_name":"Bash","tool_input":{"command":"echo hi"}}`
	var stderr bytes.Buffer
	if code := Run(EventPostEdit, "claude", root, strings.NewReader(stdin), &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("Bash edit should no-op, got %d", code)
	}
}

func TestStopReentrancyGuard(t *testing.T) {
	root := managedGoRepo(t)
	mustWrite(t, root, "bad.go", "package app\nfunc  X( ){}\n")
	// stop_hook_active=true must short-circuit even with lint issues present.
	stdin := `{"stop_hook_active":true}`
	if code := Run(EventStop, "claude", root, strings.NewReader(stdin), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("re-entrant Stop should exit 0, got %d", code)
	}
}

func TestSessionStartRecordsBaseline(t *testing.T) {
	root := managedGoRepo(t)
	stdin := `{"session_id":"abc123"}`
	if code := Run(EventSessionStart, "claude", root, strings.NewReader(stdin), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("session-start should exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, ".harness", "session-abc123")); err != nil {
		t.Fatalf("expected baseline file: %v", err)
	}
}
