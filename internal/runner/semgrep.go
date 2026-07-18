package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/olesho/harness/internal/lockfile"
)

// semgrepImage is the official Semgrep CLI container. Running it via Docker keeps
// Semgrep out of every project's dependency tree (no pip/node install) the same
// way the Sonar verifier does for SonarQube.
const semgrepImage = "semgrep/semgrep"

// Semgrep runs a Semgrep SAST scan for a project when the feature is enabled.
//
// Like the Sonar verifier (and unlike the Go analyzers), it degrades gracefully:
// if Docker is not on PATH it prints a WARN and returns nil rather than failing
// the gate, so a machine without Docker is not blocked. When Docker is present, a
// scan that surfaces findings (semgrep --error exits nonzero) is a real failure.
func Semgrep(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.Semgrep {
		fmt.Fprintf(out, "NOTICE [%s] semgrep disabled\n", proj.Name)
		return nil
	}

	// Semgrep is heavy on CI (pulls a container, downloads the auto ruleset, and
	// scans the whole tree with no caching). Skip it on CI runners so the gate
	// stays fast; it still runs locally through the git hooks and manual
	// `harness ci`. CI systems set CI=true by convention.
	if isCI() {
		fmt.Fprintf(out, "NOTICE [%s] semgrep skipped on CI (runs locally)\n", proj.Name)
		return nil
	}

	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintf(out, "WARN [%s] semgrep: docker not on PATH; skipping\n", proj.Name)
		return nil
	}

	dir := projectDir(root, lock, proj)
	args := []string{
		"run", "--rm",
		"-v", dir + ":/src",
		semgrepImage,
		"semgrep", "scan", "--config", "auto", "--error",
		// Skip vendored/generated trees. These hold third-party or minified code
		// (and coverage/ is produced by the coverage verifier earlier in the same
		// run), so findings there are noise, not the project's own code.
		"--exclude", "node_modules", "--exclude", "dist",
		"--exclude", "coverage", "--exclude", "vendor",
		"/src",
	}
	cmd := exec.Command("docker", args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(out, "FAIL [%s] semgrep scan\n", proj.Name)
		return fmt.Errorf("%s: semgrep scan failed: %w", proj.Name, err)
	}
	fmt.Fprintf(out, "NOTICE [%s] semgrep scan complete\n", proj.Name)
	return nil
}

// isCI reports whether we appear to be running on a CI runner, via the
// conventional CI environment variable (set to a truthy value by GitHub
// Actions and most other CI systems). Local shells and git hooks leave it
// unset, so the semgrep scan still runs there.
func isCI() bool {
	v := os.Getenv("CI")
	return v != "" && v != "false" && v != "0"
}

// SemgrepStatus is a non-fatal snapshot of Semgrep availability, used by
// `harness doctor` to tell the user whether an enabled semgrep verifier will run.
type SemgrepStatus struct {
	DockerOK bool
	Image    string
}

// ProbeSemgrep reports Semgrep availability without running a scan.
func ProbeSemgrep() SemgrepStatus {
	_, dockerErr := exec.LookPath("docker")
	return SemgrepStatus{DockerOK: dockerErr == nil, Image: semgrepImage}
}
