package setup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/olesho/harness/internal/gitq"
	"github.com/olesho/harness/internal/lockfile"
)

// gitHookDelegate is the content of a native .git/hooks/<stage> delegate. It is
// a one-line shim to the harness binary; harness owns all logic (file discovery,
// parallelism, scoping), so Lefthook is unnecessary.
func gitHookDelegate(event string) string {
	return "#!/usr/bin/env sh\n# Managed by harness. Delegates to the harness binary.\nexec harness hook " + event + "\n"
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
			fmt.Fprintln(out, "git hooks: skipped (not a git repository; run `git init` then `harness bootstrap`)")
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
