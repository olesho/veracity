package runner

import (
	"bytes"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
)

func semgrepLock() (*lockfile.Lock, lockfile.Project) {
	proj := lockfile.Project{
		Name:     "web",
		Language: lockfile.LangTS,
		Features: lockfile.Features{Semgrep: true},
	}
	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion,
		Layout:        lockfile.LayoutSingle,
		Capabilities:  lockfile.Capabilities{Agents: []string{}},
		Projects:      []lockfile.Project{proj},
	}
	return lock, proj
}

// A disabled semgrep feature is a quiet no-op.
func TestSemgrepDisabled(t *testing.T) {
	lock, proj := semgrepLock()
	proj.Features.Semgrep = false
	var out bytes.Buffer
	if err := Semgrep(t.TempDir(), lock, proj, &out); err != nil {
		t.Fatalf("Semgrep returned error for disabled feature: %v", err)
	}
	if !strings.Contains(out.String(), "semgrep disabled") {
		t.Errorf("expected 'semgrep disabled' notice, got: %q", out.String())
	}
}

// An enabled semgrep verifier soft-skips (WARN, no error) when Docker is not on
// PATH — the graceful-degradation contract for the Docker-based scan.
func TestSemgrepSoftSkipNoDocker(t *testing.T) {
	lock, proj := semgrepLock()
	t.Setenv("CI", "")            // exercise the docker path even when the suite runs on CI
	t.Setenv("PATH", t.TempDir()) // an empty dir: docker is not resolvable
	var out bytes.Buffer
	if err := Semgrep(t.TempDir(), lock, proj, &out); err != nil {
		t.Fatalf("Semgrep should soft-skip (nil) without docker, got: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "WARN") || !strings.Contains(s, "skipping") {
		t.Errorf("expected a WARN skip, got: %q", s)
	}
}

// On a CI runner (CI=true) an enabled semgrep verifier is skipped without
// invoking Docker, so the gate stays fast; it still runs locally.
func TestSemgrepSkipInCI(t *testing.T) {
	lock, proj := semgrepLock()
	t.Setenv("CI", "true")
	var out bytes.Buffer
	if err := Semgrep(t.TempDir(), lock, proj, &out); err != nil {
		t.Fatalf("Semgrep should skip (nil) on CI, got: %v", err)
	}
	if !strings.Contains(out.String(), "skipped on CI") {
		t.Errorf("expected a 'skipped on CI' notice, got: %q", out.String())
	}
}
