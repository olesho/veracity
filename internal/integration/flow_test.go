// Package integration exercises the full single-Go flow through the real CLI
// entry point (cli.Run), the same code path the binary runs: setup → verify →
// lint → test → docs status → enrich → render.
package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/analyzers"
	"github.com/olesho/harness/internal/cli"
)

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cli.Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func mustExist(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestSingleGoFullFlow(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	// Isolate the analyzer cache (never touch the user's real cache) and allow the
	// version-verified PATH fallback so lint can run offline without compiling.
	t.Setenv("HARNESS_ANALYZERS_DIR", t.TempDir())
	t.Setenv("HARNESS_ANALYZERS_DEV", "1")

	cfg := `{"layout":"single","preset":"standard","capabilities":{"agents":["claude"],"gitHooks":true,"skills":true},` +
		`"projects":[{"name":"demo","language":"go","modulePath":"example.com/demo",` +
		`"features":{"lint":true,"test":true,"markdown":true,"diagrams":true}}]}`

	if code, _, e := run(t, cfg, "setup", "--config", "-"); code != 0 {
		t.Fatalf("setup failed (%d): %s", code, e)
	}
	mustExist(t, root,
		"harness.lock.json", ".harness-version", ".harness/manifest.json",
		"go.mod", ".golangci.yml", "example/greeter.go",
		"hooks/post-edit.sh", ".claude/settings.json",
		".claude/skills/harness-docs/SKILL.md",
	)

	if code, o, e := run(t, "", "bootstrap"); code != 0 {
		t.Fatalf("bootstrap failed (%d): %s%s", code, o, e)
	}
	mustExist(t, root, ".git/hooks/pre-commit", ".git/hooks/pre-push")

	if code, o, e := run(t, "", "verify"); code != 0 {
		t.Fatalf("verify failed (%d): %s%s", code, o, e)
	}

	// Lint depends on golangci-lint resolving (dev-mode PATH fallback here); skip
	// the assertion when it isn't resolvable at the pinned version.
	if _, err := analyzers.Resolve("go", "golangci-lint"); err == nil {
		if code, o, e := run(t, "", "lint"); code != 0 {
			t.Fatalf("lint failed (%d): %s%s", code, o, e)
		}
	} else {
		t.Logf("golangci-lint not resolvable (%v); skipping lint assertion", err)
	}

	if code, o, e := run(t, "", "test"); code != 0 {
		t.Fatalf("test failed (%d): %s%s", code, o, e)
	}

	// docs status --json is the structure list the agent consumes.
	code, out, _ := run(t, "", "docs", "status", "--json")
	if code != 0 || !strings.Contains(out, `"needsSummary": true`) {
		t.Fatalf("expected pending summary in status json (code %d): %s", code, out)
	}

	enr := `{"modules":{"example.com/demo/example":{"summary":"The greeter module.",` +
		`"interfaces":{"Greeter":"The greeting contract."}}}}`
	if code, _, e := run(t, enr, "docs", "enrich", "--from", "-"); code != 0 {
		t.Fatalf("enrich failed (%d): %s", code, e)
	}

	if code, _, e := run(t, "", "docs", "render"); code != 0 {
		t.Fatalf("render failed (%d): %s", code, e)
	}
	mustExist(t, root, "docs/MODULES.md", "docs/modules.svg", "docs/modules.html", "docs/summaries.json")

	if _, out, _ := run(t, "", "docs", "status"); !strings.Contains(out, "0 summary/description item(s) pending") {
		t.Fatalf("expected 0 pending after enrich: %s", out)
	}

	// The interface-centric diagram must contain the interface and its methods.
	ifaceSVG, err := os.ReadFile(filepath.Join(root, "docs", "interfaces", "example-Greeter.svg"))
	if err != nil {
		t.Fatalf("interface SVG not written: %v", err)
	}
	for _, want := range []string{"interface Greeter", "Greet(name string) string", "implements"} {
		if !strings.Contains(string(ifaceSVG), want) {
			t.Errorf("interface SVG missing %q", want)
		}
	}
}

// TestAdoptExistingGoProject adopts a pre-existing Go project: harness must
// detect the real module path, NOT inject the sample module, and pass verify.
func TestAdoptExistingGoProject(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	mustWriteFile(t, root, "go.mod", "module github.com/acme/widget\n\ngo 1.24\n")
	mustWriteFile(t, root, "widget.go", "// Package widget is existing code.\npackage widget\n\n// Store is the boundary.\ntype Store interface{ Get(id string) string }\n")

	cfg := `{"layout":"single","preset":"standard","capabilities":{"agents":["claude"]},` +
		`"projects":[{"name":"widget","language":"go","features":{"lint":false,"test":false,"markdown":true,"diagrams":true}}]}`
	if code, _, e := run(t, cfg, "setup", "--config", "-", "--adopt"); code != 0 {
		t.Fatalf("adopt setup failed (%d): %s", code, e)
	}
	// The existing code is untouched and no sample module was injected.
	if _, err := os.Stat(filepath.Join(root, "example", "greeter.go")); err == nil {
		t.Fatal("adopt must not inject the sample module")
	}
	mustExist(t, root, "harness.lock.json", "widget.go", ".claude/settings.json")

	// verify must pass — the lock's modulePath was detected from go.mod.
	if code, o, e := run(t, "", "verify"); code != 0 {
		t.Fatalf("verify failed after adopt (%d): %s%s", code, o, e)
	}
	// docs sees the real Store interface.
	if code, out, _ := run(t, "", "docs", "status", "--json"); code != 0 || !strings.Contains(out, `"name": "Store"`) {
		t.Fatalf("expected the existing Store interface in status: %s", out)
	}
}

// TestToggleFeaturesAndCapabilities covers subsequent reruns: setup refuses a
// second time, `edit` toggles per-project features (with flags after the name),
// and `reconfigure` toggles repo capabilities (adding files, then pruning them).
func TestToggleFeaturesAndCapabilities(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	cfg := `{"layout":"single","preset":"standard","capabilities":{"agents":["claude"]},` +
		`"projects":[{"name":"app","language":"go","modulePath":"example.com/app",` +
		`"features":{"lint":true,"test":true,"markdown":false,"diagrams":false}}]}`
	if code, _, e := run(t, cfg, "setup", "--config", "-"); code != 0 {
		t.Fatalf("setup failed: %s", e)
	}

	// A second setup is refused.
	if code, _, _ := run(t, cfg, "setup", "--config", "-"); code == 0 {
		t.Fatal("expected second setup to be refused")
	}

	// edit: flags placed AFTER the name must parse; wrong --confirm is rejected.
	if code, _, _ := run(t, "", "edit", "app", "--confirm", "WRONG", "--lint", "off"); code == 0 {
		t.Fatal("edit with wrong --confirm should fail")
	}
	if code, _, e := run(t, "", "edit", "app", "--confirm", "app", "--diagrams", "on"); code != 0 {
		t.Fatalf("edit --diagrams on failed: %s", e)
	}
	// diagrams forces markdown.
	code, out, _ := run(t, "", "lock-query", "app", "--json")
	if code != 0 || !strings.Contains(out, `"markdown": true`) || !strings.Contains(out, `"diagrams": true`) {
		t.Fatalf("diagrams should force markdown on: %s", out)
	}

	// reconfigure: enable CI adds files; disable CI prunes them.
	if code, _, e := run(t, "", "reconfigure", "--ci", "on"); code != 0 {
		t.Fatalf("reconfigure --ci on failed: %s", e)
	}
	mustExist(t, root, ".github/workflows/ci.yml", "docs/ci-setup.md")
	if code, _, e := run(t, "", "reconfigure", "--ci", "off"); code != 0 {
		t.Fatalf("reconfigure --ci off failed: %s", e)
	}
	if _, err := os.Stat(filepath.Join(root, ".github/workflows/ci.yml")); err == nil {
		t.Fatal("disabling ci should prune ci.yml")
	}

	// Adding codex wiring works and verify stays clean.
	if code, _, e := run(t, "", "reconfigure", "--codex", "on"); code != 0 {
		t.Fatalf("reconfigure --codex on failed: %s", e)
	}
	mustExist(t, root, ".codex/config.toml")
	if code, o, e := run(t, "", "verify"); code != 0 {
		t.Fatalf("verify failed after toggles: %s%s", o, e)
	}
}

func mustWriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
