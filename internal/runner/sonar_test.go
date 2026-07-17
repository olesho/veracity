package runner

import (
	"bytes"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
)

func singleGoLock() (*lockfile.Lock, lockfile.Project) {
	proj := lockfile.Project{
		Name:       "myproj",
		Language:   lockfile.LangGo,
		ModulePath: "example.com/myproj",
		Features:   lockfile.Features{Sonar: true},
	}
	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion,
		Layout:        lockfile.LayoutSingle,
		Capabilities:  lockfile.Capabilities{Agents: []string{}},
		Projects:      []lockfile.Project{proj},
	}
	return lock, proj
}

func TestContainerHost(t *testing.T) {
	cases := map[string]string{
		"http://localhost:9000": "http://host.docker.internal:9000",
		"http://127.0.0.1:9000": "http://host.docker.internal:9000",
		"http://sonar.example":  "http://sonar.example",
	}
	for in, want := range cases {
		if got := containerHost(in); got != want {
			t.Errorf("containerHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// A disabled sonar feature is a quiet no-op.
func TestSonarDisabled(t *testing.T) {
	lock, proj := singleGoLock()
	proj.Features.Sonar = false
	var out bytes.Buffer
	if err := Sonar(t.TempDir(), lock, proj, &out); err != nil {
		t.Fatalf("Sonar returned error for disabled feature: %v", err)
	}
	if !strings.Contains(out.String(), "sonar disabled") {
		t.Errorf("expected 'sonar disabled' notice, got: %q", out.String())
	}
}

// An enabled sonar verifier soft-skips (WARN, no error) when the server can't be
// reached — the graceful-degradation contract for a heavy optional service.
func TestSonarSoftSkipUnreachable(t *testing.T) {
	lock, proj := singleGoLock()
	t.Setenv("SONAR_TOKEN", "dummy-token")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:1") // nothing listens here
	var out bytes.Buffer
	if err := Sonar(t.TempDir(), lock, proj, &out); err != nil {
		t.Fatalf("Sonar should soft-skip (nil) when unreachable, got: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "WARN") || !strings.Contains(s, "skipping") {
		t.Errorf("expected a WARN skip, got: %q", s)
	}
}
