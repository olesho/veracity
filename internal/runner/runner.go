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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/olesho/harness/internal/analyzers"
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
		if c.Gate != "" && !proj.Features.Enabled(c.Gate) {
			continue // command gated off for this project
		}
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
		display := strings.Join(argv, " ") // tool name (+ files), before path resolution

		// Harness-managed analyzers resolve to a verified absolute cache path; an
		// enabled-but-unavailable tool hard-fails here with an install hint.
		if bin, rerr := resolveArgv0(proj.Language, argv[0]); rerr != nil {
			fmt.Fprintf(out, "FAIL [%s] %s\n%v\n", proj.Name, display, rerr)
			fail(fmt.Errorf("%s: %w", proj.Name, rerr))
			continue
		} else {
			argv[0] = bin
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
			fmt.Fprintf(out, "FAIL [%s] %s\n", proj.Name, display)
			if s := strings.TrimRight(stdout.String(), "\n"); s != "" {
				fmt.Fprintln(out, s)
			}
			if s := strings.TrimRight(stderr.String(), "\n"); s != "" {
				fmt.Fprintln(out, s)
			}
			fail(fmt.Errorf("%s: command failed: %s", proj.Name, display))
		}
	}
	return firstErr
}

// resolveArgv0 rewrites a command's leading token to a verified absolute path
// when it names a harness-managed analyzer; other tools (go, gofmt, …) pass
// through to PATH resolution as before.
func resolveArgv0(lang, name string) (string, error) {
	for _, a := range toolchain.Analyzers(lang) {
		if a.Name == name {
			return analyzers.Resolve(lang, name)
		}
	}
	return name, nil
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

// FileChecks runs the fast, per-file checks the agent edit-loop enforces on the
// given changed files: the file lint (gofmt) plus any enabled fast formatters
// (gofumpt, gci). The slower verifiers (tests, mod-tidy, coverage) are left to
// the git pre-push gate and `harness ci`. It returns the first failure after
// running them all.
func FileChecks(root string, lock *lockfile.Lock, proj lockfile.Project, files []string, out io.Writer) error {
	if len(files) == 0 {
		return nil
	}
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	record(LintFiles(root, lock, proj, files, out)) // gofmt -l, gated on Lint
	if proj.Features.Gofumpt {
		record(RunPhase(root, lock, proj, toolchain.PhaseGofumpt, files, out))
	}
	if proj.Features.Gci {
		record(RunPhase(root, lock, proj, toolchain.PhaseGci, files, out))
	}
	return firstErr
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

// Gofumpt runs the stricter-format check (gofumpt -l) when enabled.
func Gofumpt(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.Gofumpt {
		fmt.Fprintf(out, "NOTICE [%s] gofumpt disabled\n", proj.Name)
		return nil
	}
	return RunPhase(root, lock, proj, toolchain.PhaseGofumpt, nil, out)
}

// Gci runs the import-ordering check (gci list) when enabled.
func Gci(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.Gci {
		fmt.Fprintf(out, "NOTICE [%s] gci disabled\n", proj.Name)
		return nil
	}
	return RunPhase(root, lock, proj, toolchain.PhaseGci, nil, out)
}

// ModTidy runs the module-hygiene check (go mod tidy -diff) when enabled.
func ModTidy(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.ModTidy {
		fmt.Fprintf(out, "NOTICE [%s] mod-tidy disabled\n", proj.Name)
		return nil
	}
	return RunPhase(root, lock, proj, toolchain.PhaseModTidy, nil, out)
}

// ModTidy runs the module-hygiene check (go mod tidy -diff) when enabled — kept
// above Audit so the verifier wrappers read top-to-bottom in ExtraChecks order.

// Audit runs the TypeScript dependency-vulnerability gate (pnpm audit) when
// enabled.
func Audit(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.Audit {
		fmt.Fprintf(out, "NOTICE [%s] audit disabled\n", proj.Name)
		return nil
	}
	return RunPhase(root, lock, proj, toolchain.PhaseAudit, nil, out)
}

// Coverage runs the coverage gate, failing when total coverage is below
// proj.CoverageMin. A CoverageMin of 0 measures and reports but never fails. It
// dispatches on language: Go parses a coverage profile itself; TypeScript defers
// the threshold check to vitest.
func Coverage(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.Coverage {
		fmt.Fprintf(out, "NOTICE [%s] coverage disabled\n", proj.Name)
		return nil
	}
	if proj.Language == lockfile.LangTS {
		return coverageTS(root, lock, proj, out)
	}
	return coverageGo(root, lock, proj, out)
}

// tsCoverageArgv builds the `vitest run --coverage` invocation, appending
// vitest's own line-threshold flag when CoverageMin > 0 so vitest enforces the
// gate and exits nonzero itself. Returns nil when the phase has no command.
func tsCoverageArgv(proj lockfile.Project) []string {
	cmds := toolchain.Commands(proj.Language, toolchain.PhaseCoverage)
	if len(cmds) == 0 {
		return nil
	}
	argv := append([]string{}, cmds[0].Argv...)
	if proj.CoverageMin > 0 {
		argv = append(argv, fmt.Sprintf("--coverage.thresholds.lines=%d", proj.CoverageMin))
	}
	return argv
}

// coverageTS runs the vitest coverage gate for a TypeScript project.
func coverageTS(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	argv := tsCoverageArgv(proj)
	if len(argv) == 0 {
		return nil
	}
	display := strings.Join(argv, " ")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = projectDir(root, lock, proj)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(out, "FAIL [%s] %s\n", proj.Name, display)
		if s := strings.TrimRight(buf.String(), "\n"); s != "" {
			fmt.Fprintln(out, s)
		}
		return fmt.Errorf("%s: coverage below minimum %d%% (or test run failed)", proj.Name, proj.CoverageMin)
	}
	fmt.Fprintf(out, "NOTICE [%s] coverage: passed (min %d%%)\n", proj.Name, proj.CoverageMin)
	return nil
}

// coverageGo generates a Go coverage profile into an exclusive temp file and
// computes total statement coverage from the profile itself (not the rounded
// `go tool cover -func` display), failing when it is below proj.CoverageMin.
func coverageGo(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	cmds := toolchain.Commands(proj.Language, toolchain.PhaseCoverage)
	if len(cmds) == 0 {
		return nil
	}
	f, err := os.CreateTemp("", "harness-cover-*.out")
	if err != nil {
		return err
	}
	profPath := f.Name()
	_ = f.Close() // go test writes it; we only need the path
	defer func() { _ = os.Remove(profPath) }()

	argv := make([]string, 0, len(cmds[0].Argv))
	for _, a := range cmds[0].Argv {
		argv = append(argv, strings.ReplaceAll(a, toolchain.CoverProfilePlaceholder, profPath))
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = projectDir(root, lock, proj)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(out, "FAIL [%s] go test (coverage)\n", proj.Name)
		if s := strings.TrimRight(buf.String(), "\n"); s != "" {
			fmt.Fprintln(out, s)
		}
		return fmt.Errorf("%s: coverage test run failed", proj.Name)
	}
	pct, hasData, err := parseCoverProfile(profPath)
	if err != nil {
		return fmt.Errorf("%s: reading coverage profile: %w", proj.Name, err)
	}
	if hasData {
		fmt.Fprintf(out, "NOTICE [%s] coverage: %.1f%% (min %d%%)\n", proj.Name, pct, proj.CoverageMin)
	} else {
		fmt.Fprintf(out, "NOTICE [%s] coverage: no coverage data (min %d%%)\n", proj.Name, proj.CoverageMin)
	}
	if pct < float64(proj.CoverageMin) {
		fmt.Fprintf(out, "FAIL [%s] coverage %.1f%% is below the %d%% minimum\n", proj.Name, pct, proj.CoverageMin)
		return fmt.Errorf("%s: coverage %.1f%% below minimum %d%%", proj.Name, pct, proj.CoverageMin)
	}
	return nil
}

// ExtraChecks runs every enabled quality/security verifier for a project
// (quietly skipping disabled ones) and returns the first error.
func ExtraChecks(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if proj.Features.Gofumpt {
		record(Gofumpt(root, lock, proj, out))
	}
	if proj.Features.Gci {
		record(Gci(root, lock, proj, out))
	}
	if proj.Features.ModTidy {
		record(ModTidy(root, lock, proj, out))
	}
	if proj.Features.Coverage {
		record(Coverage(root, lock, proj, out))
	}
	if proj.Features.Audit {
		record(Audit(root, lock, proj, out))
	}
	if proj.Features.Semgrep {
		record(Semgrep(root, lock, proj, out))
	}
	if proj.Features.Sonar {
		record(Sonar(root, lock, proj, out))
	}
	return firstErr
}

// parseCoverProfile computes the percentage of covered statements from a Go
// coverage profile. hasData is false when the profile has no statement rows (no
// packages/tests). It errors only on an unreadable or malformed file.
func parseCoverProfile(path string) (pct float64, hasData bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	var total, covered int
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// Format: name.go:line.col,line.col numStmts count
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return 0, false, fmt.Errorf("malformed coverage line: %q", line)
		}
		n, err1 := strconv.Atoi(fields[1])
		cnt, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			return 0, false, fmt.Errorf("malformed coverage line: %q", line)
		}
		total += n
		if cnt > 0 {
			covered += n
		}
	}
	if total == 0 {
		return 0, false, nil
	}
	return 100 * float64(covered) / float64(total), true, nil
}

func projectDir(root string, lock *lockfile.Lock, proj lockfile.Project) string {
	base := proj.Path(lock.Layout)
	if base == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(base))
}
