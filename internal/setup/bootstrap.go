package setup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/olesho/veracity/internal/analyzers"
	"github.com/olesho/veracity/internal/docgen"
	"github.com/olesho/veracity/internal/gitq"
	"github.com/olesho/veracity/internal/lockfile"
)

// gitHookDelegate is the content of a native .git/hooks/<stage> delegate. It is
// a one-line shim to the veracity binary; veracity owns all logic (file discovery,
// parallelism, scoping), so Lefthook is unnecessary.
func gitHookDelegate(event string) string {
	return "#!/usr/bin/env sh\n# Managed by veracity. Delegates to the veracity binary.\nexec veracity hook " + event + "\n"
}

// Bootstrap installs git-hook delegates (when enabled) and per-project
// dependencies. It is idempotent and convergent: each step reports pass/fail and
// re-running is safe.
func Bootstrap(root string, out io.Writer) error {
	lock, err := lockfile.Load(root)
	if err != nil {
		return err
	}

	if lock.Capabilities.GitHooks {
		if gitq.IsRepo(root) {
			if err := installGitHooks(root); err != nil {
				fmt.Fprintf(out, "git hooks: FAILED: %v\n", err)
			} else {
				fmt.Fprintln(out, "git hooks: installed (pre-commit, pre-push)")
			}
		} else {
			fmt.Fprintln(out, "git hooks: skipped (not a git repository; run `git init` then `veracity bootstrap`)")
		}
	}

	for _, p := range lock.Projects {
		dir := projectDir(root, lock, p)
		if err := installDeps(dir, p.Language, out); err != nil {
			fmt.Fprintf(out, "deps [%s]: FAILED: %v\n", p.Name, err)
		} else {
			fmt.Fprintf(out, "deps [%s]: ok\n", p.Name)
		}
	}

	// Report the veracity-managed analyzers the enabled features need. Bootstrap
	// deliberately does not compile them — that keeps it fast and offline; the
	// explicit `veracity install-tools` provisions the pinned cache (CI runs it,
	// and the git-hook/CI gates hard-fail with that same hint if a required tool
	// is missing, so an unprovisioned repo can never pass silently).
	if req := analyzers.Required(lock); len(req) > 0 {
		names := make([]string, 0, len(req))
		for _, a := range req {
			names = append(names, a.Name)
		}
		fmt.Fprintf(out, "analyzers: run `veracity install-tools` to provision %s\n", strings.Join(names, ", "))
	}

	// Render structural docs now (deterministic, no LLM) so they exist right
	// after setup — the auto-render Stop hook only takes effect in the next agent
	// session, so we cannot rely on it for the first render.
	if n, err := docgen.Render(root, lock, false, false, out); err != nil {
		fmt.Fprintf(out, "docs: FAILED: %v\n", err)
	} else if n > 0 {
		fmt.Fprintln(out, "docs: rendered (run the veracity-docs skill to add prose summaries)")
	}
	return nil
}

func installGitHooks(root string) error {
	hooksDir := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}
	for _, stage := range []string{"pre-commit", "pre-push"} {
		path := filepath.Join(hooksDir, stage)
		if err := os.WriteFile(path, []byte(gitHookDelegate(stage)), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func installDeps(dir, lang string, out io.Writer) error {
	var name string
	var args []string
	switch lang {
	case lockfile.LangGo:
		name, args = "go", []string{"mod", "download"}
	case lockfile.LangPython:
		name, args = "uv", []string{"sync"}
	case lockfile.LangTS:
		name, args = "pnpm", []string{"install"}
	default:
		return nil
	}
	if _, err := exec.LookPath(name); err != nil {
		fmt.Fprintf(out, "  (%s not on PATH; skipping dependency install)\n", name)
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
