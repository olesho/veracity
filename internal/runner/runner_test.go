package runner

import (
	"bytes"
	"os"
	"path/filepath"
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
