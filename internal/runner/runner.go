// Package runner executes toolchain phases (lint/format/test) for a project.
// It is the one place commands are actually run: it resolves the command table
// from internal/toolchain, discovers files via internal/fileset, sets the
// working directory to the project root, and enforces the FailOnStdout rule that
// gofmt's always-zero exit code requires.
package runner

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/olesho/harness/internal/fileset"
	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/toolchain"
)

// RunPhase runs every command of a phase for one project, writing per-command
// failures to out. files is the project-root-relative file list appended to
// file-oriented commands; if nil, all eligible source files are discovered.
// It returns an error if any command failed (after running them all).
func RunPhase(root string, lock *lockfile.Lock, proj lockfile.Project, phase toolchain.Phase, files []string, out io.Writer) error {
	dir := projectDir(root, lock, proj)
	cmds := toolchain.Commands(proj.Language, phase)

	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	for _, c := range cmds {
		argv := append([]string{}, c.Argv...)
		if c.Files == toolchain.AppendFiles {
			fl := files
			if fl == nil {
				discovered, derr := fileset.EligibleFiles(root, lock, proj)
				if derr != nil {
					fail(derr)
					continue
				}
				fl = discovered
			}
			if len(fl) == 0 {
				continue // nothing to check for this file-oriented command
			}
			argv = append(argv, fl...)
		}

		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr := cmd.Run()

		failed := runErr != nil
		if c.FailOnStdout && strings.TrimSpace(stdout.String()) != "" {
			failed = true
		}
		if failed {
			fmt.Fprintf(out, "FAIL [%s] %s\n", proj.Name, strings.Join(argv, " "))
			if s := strings.TrimRight(stdout.String(), "\n"); s != "" {
				fmt.Fprintln(out, s)
			}
			if s := strings.TrimRight(stderr.String(), "\n"); s != "" {
				fmt.Fprintln(out, s)
			}
			fail(fmt.Errorf("%s: command failed: %s", proj.Name, strings.Join(argv, " ")))
		}
	}
	return firstErr
}

// Lint runs the full project-lint bundle for a project.
func Lint(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.Lint {
		fmt.Fprintf(out, "NOTICE [%s] linting disabled\n", proj.Name)
		return nil
	}
	return RunPhase(root, lock, proj, toolchain.PhaseProjectLint, nil, out)
}

// LintFiles runs the fast per-file lint on a specific set of project-root-relative
// files (used by the post-edit hook).
func LintFiles(root string, lock *lockfile.Lock, proj lockfile.Project, files []string, out io.Writer) error {
	if !proj.Features.Lint || len(files) == 0 {
		return nil
	}
	return RunPhase(root, lock, proj, toolchain.PhaseFileLint, files, out)
}

// Test runs the verifier for a project.
func Test(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.Test {
		fmt.Fprintf(out, "NOTICE [%s] testing disabled\n", proj.Name)
		return nil
	}
	return RunPhase(root, lock, proj, toolchain.PhaseTest, nil, out)
}

// Format rewrites formatting in place for a project.
func Format(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	return RunPhase(root, lock, proj, toolchain.PhaseFormat, nil, out)
}

func projectDir(root string, lock *lockfile.Lock, proj lockfile.Project) string {
	base := proj.Path(lock.Layout)
	if base == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(base))
}
