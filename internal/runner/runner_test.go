package runner

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/toolchain"
)

func goLock() *lockfile.Lock {
	return &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutSingle,
		Projects: []lockfile.Project{{
			Name: "app", Language: lockfile.LangGo, ModulePath: "example.com/app",
			Features: lockfile.Features{Lint: true, Test: true},
		}},
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGoFileLintDetectsBadFormat(t *testing.T) {
	root := t.TempDir()
	lock := goLock()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	// Deliberately mis-formatted (extra spaces, bad indentation).
	writeFile(t, root, "bad.go", "package app\nfunc  Bad( ){\nreturn\n}\n")

	var out bytes.Buffer
	err := RunPhase(root, lock, lock.Projects[0], toolchain.PhaseFileLint, []string{"bad.go"}, &out)
	if err == nil {
		t.Fatalf("expected gofmt -l to flag bad.go; output:\n%s", out.String())
	}
}

func TestGoFileLintPassesWellFormatted(t *testing.T) {
	root := t.TempDir()
	lock := goLock()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeFile(t, root, "good.go", "package app\n\nfunc Good() {}\n")

	var out bytes.Buffer
	if err := RunPhase(root, lock, lock.Projects[0], toolchain.PhaseFileLint, []string{"good.go"}, &out); err != nil {
		t.Fatalf("well-formatted file should pass: %v\n%s", err, out.String())
	}
}

func TestGoTestRuns(t *testing.T) {
	root := t.TempDir()
	lock := goLock()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeFile(t, root, "m.go", "package app\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, root, "m_test.go", "package app\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")

	var out bytes.Buffer
	if err := Test(root, lock, lock.Projects[0], &out); err != nil {
		t.Fatalf("go test should pass: %v\n%s", err, out.String())
	}
}

func TestDisabledFeaturesAreNoOps(t *testing.T) {
	root := t.TempDir()
	lock := goLock()
	lock.Projects[0].Features.Lint = false
	lock.Projects[0].Features.Test = false
	var out bytes.Buffer
	if err := Lint(root, lock, lock.Projects[0], &out); err != nil {
		t.Fatalf("disabled lint should no-op: %v", err)
	}
	if err := Test(root, lock, lock.Projects[0], &out); err != nil {
		t.Fatalf("disabled test should no-op: %v", err)
	}
}

func TestVerifierWrappersNoticeWhenDisabled(t *testing.T) {
	root := t.TempDir()
	lock := goLock() // verifiers all off
	for _, fn := range []func(string, *lockfile.Lock, lockfile.Project, io.Writer) error{
		Gofumpt, Gci, ModTidy, Coverage, Audit,
	} {
		var out bytes.Buffer
		if err := fn(root, lock, lock.Projects[0], &out); err != nil {
			t.Errorf("disabled verifier should no-op: %v", err)
		}
		if !strings.Contains(out.String(), "disabled") {
			t.Errorf("expected a disabled NOTICE, got %q", out.String())
		}
	}
}

func TestFormatGateSkipsDisabledFormatters(t *testing.T) {
	// gci/gofumpt are gated off, so PhaseFormat runs only gofmt and never tries
	// to resolve the (uninstalled) analyzers. A well-formatted file passes.
	t.Setenv("HARNESS_ANALYZERS_DIR", t.TempDir()) // empty cache: resolution would fail
	t.Setenv("HARNESS_ANALYZERS_DEV", "")
	root := t.TempDir()
	lock := goLock() // gofumpt/gci off
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeFile(t, root, "good.go", "package app\n\nfunc Good() {}\n")
	var out bytes.Buffer
	if err := Format(root, lock, lock.Projects[0], &out); err != nil {
		t.Fatalf("format with gated-off formatters should pass: %v\n%s", err, out.String())
	}
}

func TestEnabledVerifierHardFailsWhenToolMissing(t *testing.T) {
	// gofumpt enabled but not in the (empty) cache and no dev PATH fallback →
	// hard fail with an install hint, never a silent skip.
	t.Setenv("HARNESS_ANALYZERS_DIR", t.TempDir())
	t.Setenv("HARNESS_ANALYZERS_DEV", "")
	root := t.TempDir()
	lock := goLock()
	lock.Projects[0].Features.Gofumpt = true
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeFile(t, root, "good.go", "package app\n\nfunc Good() {}\n")
	var out bytes.Buffer
	err := Gofumpt(root, lock, lock.Projects[0], &out)
	if err == nil {
		t.Fatalf("enabled gofumpt with no tool must fail; output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "install-tools") {
		t.Errorf("failure should hint at install-tools, got %q", out.String())
	}
}

func TestFileChecksWiresFormatters(t *testing.T) {
	t.Setenv("HARNESS_ANALYZERS_DIR", t.TempDir()) // empty cache → resolution would fail
	t.Setenv("HARNESS_ANALYZERS_DEV", "")
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeFile(t, root, "good.go", "package app\n\nfunc Good() {}\n")

	// Formatters off → only gofmt runs; a clean file passes with no tool needed.
	lock := goLock()
	var out bytes.Buffer
	if err := FileChecks(root, lock, lock.Projects[0], []string{"good.go"}, &out); err != nil {
		t.Fatalf("FileChecks with formatters off should pass: %v\n%s", err, out.String())
	}

	// gofumpt on → FileChecks tries to resolve it and hard-fails (not installed),
	// proving gofumpt is part of the fast agent-loop path.
	lock.Projects[0].Features.Gofumpt = true
	out.Reset()
	if err := FileChecks(root, lock, lock.Projects[0], []string{"good.go"}, &out); err == nil {
		t.Fatalf("FileChecks should run gofumpt when enabled; output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "install-tools") {
		t.Errorf("expected an install-tools hint, got %q", out.String())
	}
}

func TestParseCoverProfile(t *testing.T) {
	// Non-integer coverage must be preserved as a decimal (not rounded).
	pct, hasData, err := parseCoverProfile(writeProfile(t,
		"mode: atomic\nx.go:1.1,2.1 2 1\nx.go:3.1,4.1 1 0\n")) // 2 of 3 covered
	if err != nil || !hasData {
		t.Fatalf("parse: pct=%v hasData=%v err=%v", pct, hasData, err)
	}
	if pct < 66.6 || pct > 66.7 {
		t.Errorf("expected ~66.67%%, got %v (rounding bug?)", pct)
	}

	// mode-only profile → no data.
	if _, hasData, err := parseCoverProfile(writeProfile(t, "mode: atomic\n")); err != nil || hasData {
		t.Fatalf("empty profile: hasData=%v err=%v", hasData, err)
	}

	// malformed → error.
	if _, _, err := parseCoverProfile(writeProfile(t, "mode: atomic\ngarbage line here\n")); err == nil {
		t.Fatal("malformed profile line should error")
	}
}

func writeProfile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cover.out")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCoverageGate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	// Two functions, one statement each; the test covers only one → 50%.
	writeFile(t, root, "m.go", "package app\n\nfunc Covered() int { return 1 }\n\nfunc Uncovered() int { return 2 }\n")
	writeFile(t, root, "m_test.go", "package app\n\nimport \"testing\"\n\nfunc TestCovered(t *testing.T) {\n\tif Covered() != 1 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")

	lock := goLock()
	lock.Projects[0].Features.Coverage = true

	// Below threshold → fail.
	lock.Projects[0].CoverageMin = 80
	var out bytes.Buffer
	if err := Coverage(root, lock, lock.Projects[0], &out); err == nil {
		t.Fatalf("50%% coverage should fail an 80%% gate; output:\n%s", out.String())
	}

	// At/below actual → pass, and the measured total is printed.
	lock.Projects[0].CoverageMin = 40
	out.Reset()
	if err := Coverage(root, lock, lock.Projects[0], &out); err != nil {
		t.Fatalf("50%% coverage should pass a 40%% gate: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "50.0%") {
		t.Errorf("expected the measured 50.0%% to be printed, got %q", out.String())
	}

	// Report-only (0) always passes.
	lock.Projects[0].CoverageMin = 0
	out.Reset()
	if err := Coverage(root, lock, lock.Projects[0], &out); err != nil {
		t.Fatalf("report-only coverage should pass: %v\n%s", err, out.String())
	}
}

// tsCoverageArgv appends vitest's threshold flag only when a minimum is set, so
// CoverageMin=0 stays report-only while a positive minimum lets vitest gate.
func TestTSCoverageArgv(t *testing.T) {
	proj := lockfile.Project{Name: "web", Language: lockfile.LangTS,
		Features: lockfile.Features{Coverage: true}}

	proj.CoverageMin = 0
	argv := tsCoverageArgv(proj)
	if strings.Join(argv, " ") != "pnpm exec vitest run --coverage" {
		t.Errorf("report-only argv = %q", argv)
	}
	for _, a := range argv {
		if strings.Contains(a, "thresholds") {
			t.Errorf("min=0 must not add a threshold flag, got %q", argv)
		}
	}

	proj.CoverageMin = 80
	argv = tsCoverageArgv(proj)
	last := argv[len(argv)-1]
	if last != "--coverage.thresholds.lines=80" {
		t.Errorf("min=80 threshold flag = %q, want --coverage.thresholds.lines=80", last)
	}
}
