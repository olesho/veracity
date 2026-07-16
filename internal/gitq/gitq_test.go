package gitq

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestUnbornRepo(t *testing.T) {
	dir := gitInit(t)
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.HasCommits() {
		t.Fatal("fresh repo should have no commits")
	}
	write(t, dir, "a.go", "package a\n")
	// ChangedSince on an unborn repo returns untracked files.
	changed, err := r.ChangedSince("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(changed, "a.go") {
		t.Fatalf("expected a.go in changes, got %v", changed)
	}
}

func TestTrickyFilenames(t *testing.T) {
	dir := gitInit(t)
	tricky := "weird name.go"
	write(t, dir, tricky, "package a\n")
	write(t, dir, "normal.go", "package a\n")
	r, _ := Open(dir)

	untracked, err := r.UntrackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(untracked, tricky) || !contains(untracked, "normal.go") {
		t.Fatalf("untracked missing tricky/normal: %v", untracked)
	}

	gitCmd(t, dir, "add", "-A")
	staged, err := r.StagedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(staged, tricky) {
		t.Fatalf("staged missing tricky filename: %v", staged)
	}
}

func TestWorkingTreeChangesWithRename(t *testing.T) {
	dir := gitInit(t)
	write(t, dir, "old.go", "package a\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "init")
	gitCmd(t, dir, "mv", "old.go", "new.go")

	r, _ := Open(dir)
	changes, err := r.WorkingTreeChanges()
	if err != nil {
		t.Fatal(err)
	}
	// The new path must appear; the old path (rename source) must be skipped.
	if !contains(changes, "new.go") {
		t.Fatalf("expected new.go in changes, got %v", changes)
	}
	if contains(changes, "old.go") {
		t.Fatalf("rename source old.go should be skipped, got %v", changes)
	}
}

func TestNotARepo(t *testing.T) {
	dir := t.TempDir()
	if IsRepo(dir) {
		t.Fatal("temp dir should not be a repo")
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("expected error opening non-repo")
	}
}

func contains(s []string, want string) bool {
	for _, x := range s {
		if x == want {
			return true
		}
	}
	return false
}
