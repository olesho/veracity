// Package hook implements the harness hook entry point invoked by the thin
// per-agent shims (`harness hook <event> --agent <host>`) and the native git
// delegates. The heavy logic lives here, shared across Claude and Codex, which
// expose near-identical contracts (PostToolUse/Stop, exit 2 + stderr to block
// with feedback). PostToolUse is a fast single-file optimization; the Stop scan
// is the local correctness net that covers edits the per-tool hook can miss.
package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/olesho/harness/internal/docgen"
	"github.com/olesho/harness/internal/fileset"
	"github.com/olesho/harness/internal/gitq"
	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/runner"
	"github.com/olesho/harness/internal/version"
)

// Event names accepted by Run.
const (
	EventPostEdit     = "post-edit"
	EventStop         = "stop"
	EventSessionStart = "session-start"
	EventPreCommit    = "pre-commit"
	EventPrePush      = "pre-push"
)

// recognized single-file editing tools whose edits the post-edit hook can lint
// immediately. Anything else is left to the Stop scan.
var singleFileTools = map[string]bool{
	"Edit": true, "Write": true, "MultiEdit": true, "apply_patch": true,
}

type hookInput struct {
	SessionID      string          `json:"session_id"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	StopHookActive bool            `json:"stop_hook_active"`
}

type toolInput struct {
	FilePath string `json:"file_path"`
}

// Run dispatches a hook event. root is the repo root (the shim cd's there first).
// It returns a process exit code following the shared contract: 0 = continue,
// 2 = block with the message on stderr.
func Run(event, host, root string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch event {
	case EventPostEdit:
		return runPostEdit(root, stdin, stderr)
	case EventStop:
		return runStop(root, stdin, stderr)
	case EventSessionStart:
		return runSessionStart(root, stdin)
	case EventPreCommit:
		return runGitGate(root, stderr, false)
	case EventPrePush:
		return runGitGate(root, stderr, true)
	default:
		fmt.Fprintf(stderr, "harness hook: unknown event %q\n", event)
		return 2
	}
}

func loadLock(root string) (*lockfile.Lock, bool) {
	l, err := lockfile.Load(root)
	if err != nil {
		return nil, false
	}
	return l, true
}

// pinWarning writes a non-blocking warning if the running binary does not match
// the project's pinned version.
func pinWarning(root string, w io.Writer) {
	pin, err := os.ReadFile(filepath.Join(root, version.PinFileName))
	if err != nil {
		return
	}
	if msg := version.Mismatch(string(pin)); msg != "" {
		fmt.Fprintln(w, "warning: "+strings.SplitN(msg, "\n", 2)[0])
	}
}

func runPostEdit(root string, stdin io.Reader, stderr io.Writer) int {
	pinWarning(root, stderr)
	lock, ok := loadLock(root)
	if !ok {
		return 0 // not a managed project; nothing to do
	}
	data, _ := io.ReadAll(stdin)
	var in hookInput
	_ = json.Unmarshal(data, &in)
	if !singleFileTools[in.ToolName] {
		return 0 // optimization only applies to recognized single-file edits
	}
	var ti toolInput
	_ = json.Unmarshal(in.ToolInput, &ti)
	if ti.FilePath == "" {
		return 0 // multi-file / no path — leave it to the Stop scan
	}

	rel := toRepoRel(root, ti.FilePath)
	if rel == "" || fileset.Ignored(rel) {
		return 0
	}
	proj, ok := fileset.ProjectOf(lock, rel)
	if !ok || !hasFastChecks(proj.Features) {
		return 0
	}
	files := fileset.FilterByLanguage(lock, proj, []string{rel})
	if len(files) == 0 {
		return 0 // not a source file of the project's language
	}
	var buf bytes.Buffer
	if err := runner.FileChecks(root, lock, proj, files, &buf); err != nil {
		fmt.Fprintf(stderr, "harness: checks failed for the file you just edited:\n\n%s\nRun `harness fmt` to auto-fix formatting/imports, then address anything left, before continuing.\n", buf.String())
		return 2 // block with feedback (the model self-corrects this turn)
	}
	return 0
}

// hasFastChecks reports whether a project has any check the agent edit-loop
// enforces per file (lint or the fast formatters gofumpt/gci).
func hasFastChecks(f lockfile.Features) bool {
	return f.Lint || f.Gofumpt || f.Gci
}

func runStop(root string, stdin io.Reader, stderr io.Writer) int {
	data, _ := io.ReadAll(stdin)
	var in hookInput
	_ = json.Unmarshal(data, &in)
	if in.StopHookActive {
		return 0 // re-entrancy guard: already blocked once this turn
	}
	lock, ok := loadLock(root)
	if !ok {
		return 0
	}
	repo, err := gitq.Open(root)
	if err != nil {
		return 0 // not a git repo; nothing to scan
	}
	changed, err := repo.WorkingTreeChanges()
	if err != nil {
		return 0
	}
	groups := fileset.GroupByProject(lock, changed)

	var buf bytes.Buffer
	failed := false
	for name, files := range groups {
		proj, ok := lock.Find(name)
		if !ok || !hasFastChecks(proj.Features) {
			continue
		}
		srcFiles := fileset.FilterByLanguage(lock, proj, files)
		if len(srcFiles) == 0 {
			continue
		}
		if err := runner.FileChecks(root, lock, proj, srcFiles, &buf); err != nil {
			failed = true
		}
	}
	// Regenerate structural docs (MD + HTML/SVG) for changed projects —
	// deterministic, no LLM, reuses hash-cached summaries. Non-fatal.
	_, _ = docgen.Render(root, lock, true, false, io.Discard)

	if failed {
		fmt.Fprintf(stderr, "harness: check issues in files changed this session:\n\n%s\nRun `harness fmt` to auto-fix formatting/imports, then fix anything left, before finishing.\n", buf.String())
		return 2
	}
	// Non-blocking nudge: if diagrams are enabled and prose summaries are stale
	// for changed projects, tell the agent to refresh them via the skill.
	if note := staleSummaryNote(root, lock, groups); note != "" {
		fmt.Fprintln(stderr, note)
	}
	return 0
}

// staleSummaryNote returns a one-line, non-blocking message when a diagrams
// enabled project changed this turn has pending (stale/missing) prose summaries.
func staleSummaryNote(root string, lock *lockfile.Lock, groups map[string][]string) string {
	pending := 0
	names := []string{}
	for name := range groups {
		proj, ok := lock.Find(name)
		if !ok || !proj.Features.Diagrams {
			continue
		}
		rep, err := docgen.Status(root, lock, proj)
		if err != nil {
			continue
		}
		if p := rep.PendingCount(); p > 0 {
			pending += p
			names = append(names, name)
		}
	}
	if pending == 0 {
		return ""
	}
	return fmt.Sprintf("harness: %d diagram summary/description item(s) are stale in %v — run the harness-docs skill to refresh prose (optional).", pending, names)
}

func runSessionStart(root string, stdin io.Reader) int {
	data, _ := io.ReadAll(stdin)
	var in hookInput
	_ = json.Unmarshal(data, &in)
	if in.SessionID == "" {
		return 0
	}
	repo, err := gitq.Open(root)
	if err != nil {
		return 0
	}
	dir := filepath.Join(root, ".harness")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "session-"+sanitize(in.SessionID)), []byte(repo.HeadRef()+"\n"), 0o644)
	return 0
}

// runGitGate implements the pre-commit (test=false) and pre-push (test=true)
// native git delegates: pin-check + verify + lint (or tests). A failure returns
// a nonzero code so git aborts the commit/push.
func runGitGate(root string, stderr io.Writer, test bool) int {
	pin, err := os.ReadFile(filepath.Join(root, version.PinFileName))
	if err == nil {
		if msg := version.Mismatch(string(pin)); msg != "" {
			fmt.Fprintln(stderr, msg)
			return 1
		}
	}
	lock, ok := loadLock(root)
	if !ok {
		return 0
	}
	failed := false
	for _, proj := range lock.Projects {
		if test {
			if err := runner.Test(root, lock, proj, stderr); err != nil {
				failed = true
			}
			// Pre-push also runs the enabled quality verifiers (gofumpt/gci/
			// mod-tidy/coverage); pre-commit stays fast with lint only.
			if err := runner.ExtraChecks(root, lock, proj, stderr); err != nil {
				failed = true
			}
		} else {
			if err := runner.Lint(root, lock, proj, stderr); err != nil {
				failed = true
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

func toRepoRel(root, p string) string {
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return ""
		}
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
}
