// Package analyzers is the single owner of harness-managed external tool
// acquisition and resolution. It installs each pinned toolchain.Analyzer into a
// per-version cache with `go install` (source integrity via the Go module
// checksum database) and records the produced binary's sha256, so every later
// resolution executes a deterministic absolute path whose integrity is verified.
//
// The default contract is cache-only: CI, git hooks, and `harness ci` require the
// installed cache binary and hard-fail otherwise (with a `run: harness
// install-tools` hint). A version-verified PATH fallback is available only when
// HARNESS_ANALYZERS_DEV=1, for local development against a personally installed
// tool. Both cli and setup/bootstrap call this package, so there is one acquisition
// path and no cli→setup import cycle.
package analyzers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/toolchain"
)

// devMode reports whether the version-verified PATH fallback is permitted.
func devMode() bool { return os.Getenv("HARNESS_ANALYZERS_DEV") == "1" }

// cacheRoot is the directory holding per-version analyzer installs. Overridable
// via HARNESS_ANALYZERS_DIR (used by tests to isolate the cache).
func cacheRoot() (string, error) {
	if d := os.Getenv("HARNESS_ANALYZERS_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "harness", "analyzers"), nil
}

func analyzerDir(a toolchain.Analyzer) (string, error) {
	root, err := cacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, a.Name+"@"+a.Version), nil
}

func binPath(a toolchain.Analyzer) (string, error) {
	dir, err := analyzerDir(a)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, a.Name), nil
}

// Required returns the deduplicated (by name@version) set of analyzers whose
// gating feature is enabled on at least one project of the matching language.
func Required(lock *lockfile.Lock) []toolchain.Analyzer {
	seen := map[string]bool{}
	var out []toolchain.Analyzer
	for _, p := range lock.Projects {
		for _, a := range toolchain.Analyzers(p.Language) {
			if a.Feature != "" && !p.Features.Enabled(a.Feature) {
				continue
			}
			key := a.Name + "@" + a.Version
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, a)
		}
	}
	return out
}

// Install acquires every Required analyzer not already cached-and-valid, into a
// staging directory that is atomically renamed into the versioned cache. It
// hard-fails on the first error (so callers — CI and bootstrap — surface it).
func Install(lock *lockfile.Lock, out io.Writer) error {
	for _, a := range Required(lock) {
		ok, err := valid(a)
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(out, "analyzer %s@%s: cached\n", a.Name, a.Version)
			continue
		}
		if err := installOne(a, out); err != nil {
			return fmt.Errorf("installing %s@%s: %w", a.Name, a.Version, err)
		}
		fmt.Fprintf(out, "analyzer %s@%s: installed\n", a.Name, a.Version)
	}
	return nil
}

func installOne(a toolchain.Analyzer, out io.Writer) error {
	if a.Module == "" {
		return fmt.Errorf("analyzer %s has no install module", a.Name)
	}
	dir, err := analyzerDir(a)
	if err != nil {
		return err
	}
	root := filepath.Dir(dir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(root, ".staging-"+a.Name+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }() // no-op once renamed away

	cmd := exec.Command("go", "install", a.Module+"@v"+a.Version)
	cmd.Env = append(os.Environ(), "GOBIN="+staging)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return err
	}
	bin := filepath.Join(staging, a.Name)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("go install produced no %q binary", a.Name)
	}
	sum, err := fileSHA256(bin)
	if err != nil {
		return err
	}
	if err := os.WriteFile(bin+".sha256", []byte(sum+"\n"), 0o644); err != nil {
		return err
	}
	// Atomic publish. If the destination already exists, another process won the
	// race; keep theirs and discard staging (via the deferred RemoveAll).
	if err := os.Rename(staging, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// valid reports whether the cached binary exists and matches its sha256 sidecar.
func valid(a toolchain.Analyzer) (bool, error) {
	bin, err := binPath(a)
	if err != nil {
		return false, err
	}
	want, err := os.ReadFile(bin + ".sha256")
	if err != nil {
		return false, nil // missing binary or sidecar → not valid
	}
	got, err := fileSHA256(bin)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(want)) == got, nil
}

// Resolve returns the absolute path to run for analyzer name in language lang.
// Cache hit with a matching checksum wins; a present-but-mismatched cache is a
// corruption error; an absent cache is a hard "not installed" error unless dev
// mode allows a version-verified PATH binary.
func Resolve(lang, name string) (string, error) {
	a, ok := find(lang, name)
	if !ok {
		return "", fmt.Errorf("unknown analyzer %q for %s", name, lang)
	}
	bin, err := binPath(a)
	if err != nil {
		return "", err
	}
	switch _, statErr := os.Stat(bin); {
	case statErr == nil:
		sum, rerr := os.ReadFile(bin + ".sha256")
		if rerr != nil {
			return "", fmt.Errorf("cached analyzer %s@%s missing checksum; run `harness install-tools`", a.Name, a.Version)
		}
		got, herr := fileSHA256(bin)
		if herr != nil {
			return "", herr
		}
		if strings.TrimSpace(string(sum)) != got {
			return "", fmt.Errorf("cached analyzer %s@%s corrupt (checksum mismatch); run `harness install-tools`", a.Name, a.Version)
		}
		return bin, nil
	case !errors.Is(statErr, os.ErrNotExist):
		return "", statErr
	}
	// Absent from the cache.
	if devMode() {
		return resolvePATH(a)
	}
	return "", fmt.Errorf("%s not installed; run `harness install-tools`", a.Name)
}

// resolvePATH accepts a PATH binary only when its reported version matches the
// pin (dev mode only).
func resolvePATH(a toolchain.Analyzer) (string, error) {
	p, err := exec.LookPath(a.Name)
	if err != nil {
		return "", fmt.Errorf("%s not installed and not on PATH; run `harness install-tools`", a.Name)
	}
	v, err := reportedVersion(a.Name)
	if err != nil {
		return "", fmt.Errorf("%s on PATH but its version could not be verified (want v%s); run `harness install-tools`", a.Name, a.Version)
	}
	if !strings.Contains(v, a.Version) {
		return "", fmt.Errorf("%s on PATH is %q but v%s is pinned; run `harness install-tools`", a.Name, strings.TrimSpace(v), a.Version)
	}
	return p, nil
}

var versionArgs = map[string][]string{
	"gofumpt":       {"--version"},
	"gci":           {"--version"},
	"golangci-lint": {"version"},
}

func reportedVersion(name string) (string, error) {
	args, ok := versionArgs[name]
	if !ok {
		return "", fmt.Errorf("no version command for %s", name)
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// State classifies an analyzer's cache status for doctor.
type State string

const (
	StateCached  State = "cached"
	StatePathDev State = "path-dev"
	StateMissing State = "missing"
	StateCorrupt State = "corrupt"
)

// Status is one analyzer's resolution state.
type Status struct {
	Analyzer toolchain.Analyzer
	State    State
	Path     string
}

// Statuses reports the cache state of every required analyzer (for doctor).
func Statuses(lock *lockfile.Lock) []Status {
	req := Required(lock)
	out := make([]Status, 0, len(req))
	for _, a := range req {
		s := Status{Analyzer: a}
		bin, err := binPath(a)
		if err != nil {
			s.State = StateMissing
			out = append(out, s)
			continue
		}
		if ok, verr := valid(a); verr == nil && ok {
			s.State, s.Path = StateCached, bin
		} else if _, statErr := os.Stat(bin); statErr == nil {
			s.State = StateCorrupt // present but checksum missing/mismatched
		} else if devMode() {
			if p, perr := resolvePATH(a); perr == nil {
				s.State, s.Path = StatePathDev, p
			} else {
				s.State = StateMissing
			}
		} else {
			s.State = StateMissing
		}
		out = append(out, s)
	}
	return out
}

func find(lang, name string) (toolchain.Analyzer, bool) {
	for _, a := range toolchain.Analyzers(lang) {
		if a.Name == name {
			return a, true
		}
	}
	return toolchain.Analyzer{}, false
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
