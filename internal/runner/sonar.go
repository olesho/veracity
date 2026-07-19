package runner

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/olesho/harness/internal/lockfile"
)

// defaultSonarHost is the self-hosted SonarQube endpoint assumed when
// SONAR_HOST_URL is unset. The whole stack is local; nothing is called off-box.
const defaultSonarHost = "http://localhost:9000"

// infraEnvRel is the central infra .env the local convention keeps the token in.
// It is an optional fallback only — env vars take precedence and a fresh machine
// without this file still works (the scan simply soft-skips when no token turns up).
const infraEnvRel = "Work/infra/sonarqube/.env"

// Sonar runs a SonarQube scan for a project when the feature is enabled.
//
// SonarQube is a heavy, manually-installed external service, so unlike the Go
// verifiers this degrades gracefully: if Docker is missing, the server is
// unreachable, or no token is available, it prints a WARN and returns nil rather
// than failing the gate. A *reachable* server that rejects the scan is a real
// failure. The scan itself runs the official sonar-scanner-cli container against
// the local server; the token only authenticates the scanner to that local API.
func Sonar(root string, lock *lockfile.Lock, proj lockfile.Project, out io.Writer) error {
	if !proj.Features.Sonar {
		fmt.Fprintf(out, "NOTICE [%s] sonar disabled\n", proj.Name)
		return nil
	}

	// SonarQube is a self-hosted server reachable only from the local machine; a
	// remote CI runner cannot reach it (and holds no token). Skip explicitly on
	// CI so the scan is local-only by design — via the git hooks and local
	// `harness ci` — rather than merely soft-skipped by an unreachable-server
	// probe. CI systems set CI=true by convention.
	if isCI() {
		fmt.Fprintf(out, "NOTICE [%s] sonar skipped on CI (runs locally)\n", proj.Name)
		return nil
	}

	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintf(out, "WARN [%s] sonar: docker not on PATH; skipping\n", proj.Name)
		return nil
	}

	host := sonarHost()
	token := sonarToken()
	if token == "" {
		fmt.Fprintf(out, "WARN [%s] sonar: no SONAR_TOKEN (env or %s); skipping\n", proj.Name, infraEnvRel)
		return nil
	}
	if !sonarReachable(host) {
		fmt.Fprintf(out, "WARN [%s] sonar: server %s unreachable; skipping\n", proj.Name, host)
		return nil
	}

	dir := projectDir(root, lock, proj)
	args := []string{
		"run", "--rm",
		"-e", "SONAR_HOST_URL=" + containerHost(host),
		"-e", "SONAR_TOKEN=" + token,
		"-v", dir + ":/usr/src",
	}
	// Linux daemons don't resolve host.docker.internal by default.
	if runtime.GOOS == "linux" {
		args = append(args, "--add-host=host.docker.internal:host-gateway")
	}
	args = append(args, "sonarsource/sonar-scanner-cli")

	cmd := exec.Command("docker", args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(out, "FAIL [%s] sonar scan\n", proj.Name)
		return fmt.Errorf("%s: sonar scan failed: %w", proj.Name, err)
	}
	fmt.Fprintf(out, "NOTICE [%s] sonar scan complete (%s)\n", proj.Name, host)
	return nil
}

// SonarStatus is a non-fatal snapshot of SonarQube availability, used by
// `harness doctor` to tell the user whether an enabled sonar verifier will run.
type SonarStatus struct {
	DockerOK  bool
	Host      string
	HasToken  bool
	Reachable bool
}

// ProbeSonar reports SonarQube availability without running a scan.
func ProbeSonar() SonarStatus {
	_, dockerErr := exec.LookPath("docker")
	host := sonarHost()
	st := SonarStatus{
		DockerOK: dockerErr == nil,
		Host:     host,
		HasToken: sonarToken() != "",
	}
	st.Reachable = sonarReachable(host)
	return st
}

// sonarHost returns the SonarQube base URL from SONAR_HOST_URL, else the default,
// else the value recorded in the central infra .env.
func sonarHost() string {
	if h := strings.TrimSpace(os.Getenv("SONAR_HOST_URL")); h != "" {
		return h
	}
	if h := envFileValue("SONAR_HOST_URL"); h != "" {
		return h
	}
	return defaultSonarHost
}

// sonarToken returns the scanner token from SONAR_TOKEN, else the infra .env.
func sonarToken() string {
	if t := strings.TrimSpace(os.Getenv("SONAR_TOKEN")); t != "" {
		return t
	}
	return envFileValue("SONAR_TOKEN")
}

// containerHost rewrites a host-loopback URL to the address the scanner container
// uses to reach the host. localhost/127.0.0.1 inside the container is the
// container itself, so it must become host.docker.internal.
func containerHost(host string) string {
	h := strings.ReplaceAll(host, "127.0.0.1", "host.docker.internal")
	h = strings.ReplaceAll(h, "localhost", "host.docker.internal")
	return h
}

// sonarReachable reports whether the SonarQube server answers its status endpoint.
func sonarReachable(host string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(host, "/") + "/api/system/status")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode < 500
}

// envFileValue reads a single KEY=value from the central infra .env, if present.
// It is a best-effort fallback: a missing file or key yields "".
func envFileValue(key string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	f, err := os.Open(filepath.Join(home, filepath.FromSlash(infraEnvRel)))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	prefix := key + "="
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		v := strings.TrimPrefix(line, prefix)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		return v
	}
	return ""
}
