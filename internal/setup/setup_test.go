package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
)

func boolp(b bool) *bool { return &b }

func exists(t *testing.T, root, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func goSingleInput(agents []string) *Input {
	return &Input{
		Layout:       lockfile.LayoutSingle,
		Preset:       PresetStandard,
		Capabilities: &CapsInput{Agents: &agents},
		Projects: []ProjectInput{{
			Name:       "myproj",
			Language:   lockfile.LangGo,
			ModulePath: "example.com/myproj",
		}},
	}
}

func TestInitGoSingleEndToEnd(t *testing.T) {
	root := t.TempDir()
	res, err := Init(root, goSingleInput([]string{lockfile.AgentClaude}), Options{NoGit: true})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", res.Conflicts)
	}

	// Core + owned source + claude wiring present.
	for _, rel := range []string{
		"harness.lock.json", ".harness-version", ".harness/manifest.json",
		"go.mod", ".golangci.yml", "example/greeter.go", "example/greeter_test.go",
		"hooks/post-edit.sh", "hooks/stop.sh", "hooks/session-start.sh",
		".claude/settings.json", ".gitignore",
	} {
		if !exists(t, root, rel) {
			t.Errorf("expected %s to exist", rel)
		}
	}
	// Clean-project assertion: no harness binary/tool source.
	for _, rel := range []string{"cmd", "internal", "assets", "extractors-src"} {
		if exists(t, root, rel) {
			t.Errorf("generated project must not contain tool source %q", rel)
		}
	}
	// Hook shim is executable.
	if info, err := os.Stat(filepath.Join(root, "hooks/post-edit.sh")); err == nil {
		if info.Mode()&0o100 == 0 {
			t.Error("hook shim should be executable")
		}
	}

	// Verify passes.
	vr, err := Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	if !vr.OK() {
		t.Fatalf("verify failed: %+v", vr.Issues)
	}

	// The scaffolded Go project actually builds and tests green.
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scaffolded go test failed: %v\n%s", err, out)
	}
}

func TestInitMinimalFootprint(t *testing.T) {
	root := t.TempDir()
	in := &Input{
		Layout:   lockfile.LayoutSingle,
		Preset:   PresetMinimal,
		Projects: []ProjectInput{{Name: "tiny", Language: lockfile.LangGo}},
	}
	if _, err := Init(root, in, Options{NoGit: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Present: core + owned source.
	for _, rel := range []string{"harness.lock.json", ".harness-version", "go.mod", ".golangci.yml"} {
		if !exists(t, root, rel) {
			t.Errorf("minimal: expected %s", rel)
		}
	}
	// Absent: all opt-in capabilities and the manifest (no managed files).
	for _, rel := range []string{".claude", ".codex", ".github", "hooks", "CLAUDE.md", "AGENTS.md", ".harness/manifest.json"} {
		if exists(t, root, rel) {
			t.Errorf("minimal: %s should not exist", rel)
		}
	}
	vr, _ := Verify(root)
	if !vr.OK() {
		t.Fatalf("minimal verify failed: %+v", vr.Issues)
	}
}

func TestInitRefusesExistingLock(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, goSingleInput(nil), Options{NoGit: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, goSingleInput(nil), Options{NoGit: true}); err == nil {
		t.Fatal("expected second Init to refuse an existing lock")
	}
}

func TestInitRefusesNonEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stranger.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, goSingleInput(nil), Options{NoGit: true}); err == nil {
		t.Fatal("expected refusal on non-empty dir without --adopt")
	}
	// With adopt it proceeds.
	if _, err := Init(root, goSingleInput(nil), Options{NoGit: true, Adopt: true}); err != nil {
		t.Fatalf("adopt should proceed: %v", err)
	}
}

func TestFullPresetVerifiersAreGoOnly(t *testing.T) {
	in := &Input{
		Layout: lockfile.LayoutMonorepo,
		Preset: PresetFull,
		Projects: []ProjectInput{
			{Name: "api", Language: lockfile.LangGo, ModulePath: "example.com/api"},
			{Name: "worker", Language: lockfile.LangPython},
		},
	}
	lock, err := Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	goFeat := lock.Projects[0].Features
	if !goFeat.Gofumpt || !goFeat.Gci || !goFeat.ModTidy || !goFeat.Coverage {
		t.Errorf("full preset should enable all Go verifiers, got %+v", goFeat)
	}
	if lock.Projects[0].CoverageMin != 0 {
		t.Errorf("full preset coverageMin should default to 0, got %d", lock.Projects[0].CoverageMin)
	}
	py := lock.Projects[1].Features
	if py.Gofumpt || py.Gci || py.ModTidy || py.Coverage {
		t.Errorf("python project must not receive Go verifiers, got %+v", py)
	}
}

func TestResolveCoverageMinFromConfig(t *testing.T) {
	min := 80
	in := &Input{
		Layout: lockfile.LayoutSingle,
		Projects: []ProjectInput{{
			Name: "app", Language: lockfile.LangGo, ModulePath: "example.com/app",
			Features:    &FeaturesInput{Coverage: boolp(true)},
			CoverageMin: &min,
		}},
	}
	lock, err := Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if !lock.Projects[0].Features.Coverage || lock.Projects[0].CoverageMin != 80 {
		t.Fatalf("coverageMin not applied from config: %+v / %d",
			lock.Projects[0].Features, lock.Projects[0].CoverageMin)
	}
}

func TestEditVerifiersRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, goSingleInput(nil), Options{NoGit: true}); err != nil {
		t.Fatal(err)
	}
	min := 80
	if _, err := Edit(root, "myproj", "myproj", EditInput{
		Features:    FeaturesInput{Gofumpt: boolp(true), Coverage: boolp(true)},
		CoverageMin: &min,
	}); err != nil {
		t.Fatal(err)
	}
	lock, err := lockfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !lock.Projects[0].Features.Gofumpt || !lock.Projects[0].Features.Coverage || lock.Projects[0].CoverageMin != 80 {
		t.Fatalf("edit did not persist verifiers: %+v / %d", lock.Projects[0].Features, lock.Projects[0].CoverageMin)
	}

	// Turning coverage off without --coverage-min leaves the stored threshold.
	if _, err := Edit(root, "myproj", "myproj", EditInput{
		Features: FeaturesInput{Coverage: boolp(false)},
	}); err != nil {
		t.Fatal(err)
	}
	lock, _ = lockfile.Load(root)
	if lock.Projects[0].Features.Coverage {
		t.Error("coverage should be off after edit")
	}
	if lock.Projects[0].CoverageMin != 80 {
		t.Errorf("omitted --coverage-min should leave coverageMin unchanged, got %d", lock.Projects[0].CoverageMin)
	}
}

func TestBootstrapReportsAnalyzers(t *testing.T) {
	root := t.TempDir()
	// Standard preset enables lint → golangci-lint is a required analyzer.
	if _, err := Init(root, goSingleInput(nil), Options{NoGit: true}); err != nil {
		t.Fatal(err)
	}
	// Bootstrap must stay offline (it must not compile analyzers): an empty,
	// isolated cache should not cause a failure — only a reminder to install.
	t.Setenv("HARNESS_ANALYZERS_DIR", t.TempDir())
	t.Setenv("HARNESS_ANALYZERS_DEV", "")
	var out bytes.Buffer
	if err := Bootstrap(root, &out); err != nil {
		t.Fatalf("bootstrap should not install analyzers or fail: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "install-tools") {
		t.Errorf("bootstrap should remind to run install-tools, got:\n%s", out.String())
	}
}

func TestResolveDiagramsForcesMarkdown(t *testing.T) {
	in := &Input{
		Layout: lockfile.LayoutSingle,
		Projects: []ProjectInput{{
			Name: "p", Language: lockfile.LangGo,
			Features: &FeaturesInput{Diagrams: boolp(true), Markdown: boolp(false)},
		}},
	}
	lock, err := Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if !lock.Projects[0].Features.Markdown {
		t.Fatal("diagrams must force markdown on")
	}
}
