package analyzers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/toolchain"
)

func isolatedCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HARNESS_ANALYZERS_DIR", dir)
	t.Setenv("HARNESS_ANALYZERS_DEV", "") // force cache-only resolution
	return dir
}

func goAnalyzer(t *testing.T, name string) toolchain.Analyzer {
	t.Helper()
	for _, a := range toolchain.Analyzers(lockfile.LangGo) {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no analyzer %q", name)
	return toolchain.Analyzer{}
}

// seed writes a fake analyzer binary (and optional sidecar) into the cache root.
func seed(t *testing.T, root string, a toolchain.Analyzer, content, sidecar string) string {
	t.Helper()
	dir := filepath.Join(root, a.Name+"@"+a.Version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, a.Name)
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	if sidecar != "" {
		if err := os.WriteFile(bin+".sha256", []byte(sidecar), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestRequiredFeatureSelectAndDedup(t *testing.T) {
	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutMonorepo,
		Projects: []lockfile.Project{
			{Name: "a", Language: lockfile.LangGo, ModulePath: "example.com/a",
				Features: lockfile.Features{Lint: true, Gofumpt: true}},
			{Name: "b", Language: lockfile.LangGo, ModulePath: "example.com/b",
				Features: lockfile.Features{Lint: true}},
			{Name: "c", Language: lockfile.LangPython},
		},
	}
	got := map[string]int{}
	for _, a := range Required(lock) {
		got[a.Name]++
	}
	if got["golangci-lint"] != 1 {
		t.Errorf("golangci-lint should appear once (deduped), got %d", got["golangci-lint"])
	}
	if got["gofumpt"] != 1 {
		t.Errorf("gofumpt should be required (project a), got %d", got["gofumpt"])
	}
	if got["gci"] != 0 {
		t.Errorf("gci should not be required (no project enables it), got %d", got["gci"])
	}
}

func TestResolveCacheHit(t *testing.T) {
	root := isolatedCache(t)
	a := goAnalyzer(t, "gofumpt")
	content := "#!/bin/sh\necho fake\n"
	bin := seed(t, root, a, content, sha(content))
	got, err := Resolve(lockfile.LangGo, "gofumpt")
	if err != nil {
		t.Fatalf("Resolve should succeed on a valid cache: %v", err)
	}
	if got != bin {
		t.Errorf("Resolve = %q, want %q", got, bin)
	}
}

func TestResolveMissingHardFails(t *testing.T) {
	isolatedCache(t) // empty cache, no PATH fallback
	_, err := Resolve(lockfile.LangGo, "gofumpt")
	if err == nil || !strings.Contains(err.Error(), "install-tools") {
		t.Fatalf("missing analyzer must hard-fail with install hint, got %v", err)
	}
}

func TestResolveCorruptAndMissingSidecar(t *testing.T) {
	root := isolatedCache(t)
	a := goAnalyzer(t, "gci")

	// Wrong checksum → corrupt.
	seed(t, root, a, "real-content", sha("different-content"))
	if _, err := Resolve(lockfile.LangGo, "gci"); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("checksum mismatch must be a corrupt error, got %v", err)
	}

	// Remove sidecar → missing checksum.
	if err := os.Remove(filepath.Join(root, a.Name+"@"+a.Version, a.Name+".sha256")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(lockfile.LangGo, "gci"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("missing sidecar must error, got %v", err)
	}
}

func TestResolveUnknownAnalyzer(t *testing.T) {
	isolatedCache(t)
	if _, err := Resolve(lockfile.LangGo, "nope"); err == nil {
		t.Fatal("unknown analyzer should error")
	}
}

func TestStatuses(t *testing.T) {
	root := isolatedCache(t)
	lint := goAnalyzer(t, "golangci-lint")
	content := "bin"
	seed(t, root, lint, content, sha(content)) // cached-ok

	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutSingle,
		Projects: []lockfile.Project{{
			Name: "a", Language: lockfile.LangGo, ModulePath: "example.com/a",
			Features: lockfile.Features{Lint: true, Gofumpt: true}, // gofumpt missing from cache
		}},
	}
	byName := map[string]State{}
	for _, s := range Statuses(lock) {
		byName[s.Analyzer.Name] = s.State
	}
	if byName["golangci-lint"] != StateCached {
		t.Errorf("golangci-lint state = %q, want cached", byName["golangci-lint"])
	}
	if byName["gofumpt"] != StateMissing {
		t.Errorf("gofumpt state = %q, want missing", byName["gofumpt"])
	}
}

func TestInstallSurfacesError(t *testing.T) {
	// Point the cache under a regular file so MkdirAll fails; Install must return
	// the error (callers — install-tools and CI — surface it) rather than skip.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_ANALYZERS_DIR", filepath.Join(blocker, "cache"))
	t.Setenv("HARNESS_ANALYZERS_DEV", "")
	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutSingle,
		Projects: []lockfile.Project{{
			Name: "a", Language: lockfile.LangGo, ModulePath: "example.com/a",
			Features: lockfile.Features{Lint: true},
		}},
	}
	var out bytes.Buffer
	if err := Install(lock, &out); err == nil {
		t.Fatal("Install should surface a cache-directory error")
	}
}

func TestInstallSkipsValidCache(t *testing.T) {
	// A pre-populated valid cache entry means Install does no `go install` (so it
	// succeeds offline) — exercises the idempotent valid()-skip path.
	root := isolatedCache(t)
	a := goAnalyzer(t, "gofumpt")
	content := "cached-binary"
	seed(t, root, a, content, sha(content))

	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutSingle,
		Projects: []lockfile.Project{{
			Name: "a", Language: lockfile.LangGo, ModulePath: "example.com/a",
			Features: lockfile.Features{Gofumpt: true},
		}},
	}
	var out bytes.Buffer
	if err := Install(lock, &out); err != nil {
		t.Fatalf("Install over a valid cache should succeed without network: %v", err)
	}
	if !strings.Contains(out.String(), "cached") {
		t.Errorf("expected a cached notice, got %q", out.String())
	}
}
