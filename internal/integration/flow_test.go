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

	"github.com/olesho/veracity/internal/analyzers"
	"github.com/olesho/veracity/internal/cli"
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
	t.Setenv("VERACITY_ANALYZERS_DIR", t.TempDir())
	t.Setenv("VERACITY_ANALYZERS_DEV", "1")

	cfg := `{"layout":"single","preset":"standard","capabilities":{"agents":["claude"],"gitHooks":true,"skills":true},` +
		`"projects":[{"name":"demo","language":"go","modulePath":"example.com/demo",` +
		`"features":{"lint":true,"test":true,"markdown":true,"diagrams":true}}]}`

	if code, _, e := run(t, cfg, "setup", "--config", "-"); code != 0 {
		t.Fatalf("setup failed (%d): %s", code, e)
	}
	mustExist(t, root,
		"veracity.lock.json", ".veracity-version", ".veracity/manifest.json",
		"go.mod", ".golangci.yml", "example/greeter.go",
		"hooks/post-edit.sh", ".claude/settings.json",
		".claude/skills/veracity-docs/SKILL.md",
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

// TestManagedDriftIsGatedAndRestorable is the end-to-end example for the drift
// gate: hand-editing (or deleting) veracity-managed wiring must fail `verify`,
// block the agent Stop hook and the git pre-commit/pre-push gates, and be
// repairable with `veracity restore`.
//
// Lint/test are off so the Stop hook reaches the drift gate without depending on
// analyzer resolution — the only thing under test here is drift.
func TestManagedDriftIsGatedAndRestorable(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	cfg := `{"layout":"single","capabilities":{"agents":["claude"],"gitHooks":true},` +
		`"projects":[{"name":"demo","language":"go","modulePath":"example.com/demo",` +
		`"features":{"lint":false,"test":false}}]}`
	if code, _, e := run(t, cfg, "setup", "--config", "-"); code != 0 {
		t.Fatalf("setup failed (%d): %s", code, e)
	}

	const managed = "hooks/post-edit.sh"
	full := filepath.Join(root, filepath.FromSlash(managed))
	generated, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("reading managed file: %v", err)
	}

	// Baseline: a freshly generated project verifies clean and does not block.
	if code, o, e := run(t, "", "verify"); code != 0 {
		t.Fatalf("baseline verify failed (%d): %s%s", code, o, e)
	}
	stopIn := `{"session_id":"s1","stop_hook_active":false}`
	if code, _, e := run(t, stopIn, "hook", "stop", "--agent", "claude"); code != 0 {
		t.Fatalf("baseline stop hook should pass, got %d: %s", code, e)
	}

	// Drift it, the way an agent "helpfully" would.
	if err := os.WriteFile(full, append(generated, []byte("\necho tampered\n")...), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. verify now FAILs and names the remedy.
	code, o, e := run(t, "", "verify")
	if code == 0 {
		t.Fatalf("verify must fail on managed drift; got 0: %s%s", o, e)
	}
	if !strings.Contains(o+e, "veracity restore") {
		t.Errorf("verify message must name the remedy, got: %s%s", o, e)
	}

	// 2. The agent Stop hook blocks with exit 2 (the block-with-feedback contract).
	code, _, e = run(t, stopIn, "hook", "stop", "--agent", "claude")
	if code != 2 {
		t.Fatalf("stop hook must block on drift with 2, got %d: %s", code, e)
	}
	if !strings.Contains(e, managed) || !strings.Contains(e, "veracity restore") {
		t.Errorf("stop message must name the file and remedy, got: %s", e)
	}

	// 3. Both git gates abort.
	for _, ev := range []string{"pre-commit", "pre-push"} {
		if code, _, e := run(t, "", "hook", ev); code != 1 {
			t.Errorf("%s must abort on drift with 1, got %d: %s", ev, code, e)
		}
	}

	// 4. restore repairs it, and the gates go quiet again.
	if code, o, e := run(t, "", "restore"); code != 0 {
		t.Fatalf("restore failed (%d): %s%s", code, o, e)
	}
	got, err := os.ReadFile(full)
	if err != nil || !bytes.Equal(got, generated) {
		t.Fatalf("restore must rewrite the generated content; got %q", got)
	}
	if code, o, e := run(t, "", "verify"); code != 0 {
		t.Fatalf("verify must pass after restore (%d): %s%s", code, o, e)
	}
	if code, _, e := run(t, stopIn, "hook", "stop", "--agent", "claude"); code != 0 {
		t.Fatalf("stop hook must pass after restore, got %d: %s", code, e)
	}

	// 5. A deleted managed file is caught too, and restore recreates it.
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "", "verify"); code == 0 {
		t.Error("verify must fail when a managed file is deleted")
	}
	if code, _, e := run(t, "", "restore"); code != 0 {
		t.Fatalf("restore after delete failed (%d): %s", code, e)
	}
	mustExist(t, root, managed)
}

// TestAdoptExistingGoProject adopts a pre-existing Go project: veracity must
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
	mustExist(t, root, "veracity.lock.json", "widget.go", ".claude/settings.json")

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
	// Regression: a Go-only repo's CI must not carry Node/pnpm setup steps.
	if ci, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml")); err != nil {
		t.Fatalf("reading ci.yml: %v", err)
	} else if strings.Contains(string(ci), "pnpm") || strings.Contains(string(ci), "setup-node") {
		t.Errorf("go-only ci.yml should not include Node/pnpm setup:\n%s", ci)
	}
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

// TestTSProjectCIAndVerifiers checks that a TypeScript project gets language-aware
// CI (Node/pnpm setup steps) and that the TS verifier toggles round-trip through
// edit and validate.
func TestTSProjectCIAndVerifiers(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("VERACITY_ANALYZERS_DIR", t.TempDir())

	cfg := `{"layout":"single","preset":"standard","capabilities":{"ci":true},` +
		`"projects":[{"name":"web","language":"typescript","features":{"lint":true,"test":true}}]}`
	if code, _, e := run(t, cfg, "setup", "--config", "-", "--no-git"); code != 0 {
		t.Fatalf("ts setup failed (%d): %s", code, e)
	}
	mustExist(t, root, "package.json", "eslint.config.js", "tsconfig.json",
		"pnpm-workspace.yaml", ".prettierignore", ".github/workflows/ci.yml")

	// pnpm-workspace.yaml must keep pnpm's ignored-build gate from hard-failing
	// `pnpm install`/`pnpm exec` on a fresh checkout.
	if ws, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(ws), "strictDepBuilds: false") {
		t.Errorf("pnpm-workspace.yaml should set strictDepBuilds: false:\n%s", ws)
	}
	// .prettierignore must exclude the generated pnpm lockfile.
	if pi, err := os.ReadFile(filepath.Join(root, ".prettierignore")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(pi), "pnpm-lock.yaml") {
		t.Errorf(".prettierignore should exclude pnpm-lock.yaml:\n%s", pi)
	}

	// package.json ships the vitest coverage provider.
	if pj, err := os.ReadFile(filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(pj), "@vitest/coverage-v8") {
		t.Errorf("package.json should include @vitest/coverage-v8:\n%s", pj)
	}

	// eslint.config.js uses the strict type-aware ruleset.
	if ec, err := os.ReadFile(filepath.Join(root, "eslint.config.js")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(ec), "strictTypeChecked") {
		t.Errorf("eslint.config.js should use strictTypeChecked:\n%s", ec)
	}

	// CI must set up Node + pnpm for the TypeScript project.
	ci, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"setup-node", "pnpm/action-setup"} {
		if !strings.Contains(string(ci), want) {
			t.Errorf("ts ci.yml missing %q:\n%s", want, ci)
		}
	}

	// TS verifiers round-trip through edit and pass validation.
	if code, _, e := run(t, "", "edit", "web", "--confirm", "web",
		"--coverage", "on", "--coverage-min", "75", "--audit", "on", "--semgrep", "on"); code != 0 {
		t.Fatalf("edit ts verifiers failed: %s", e)
	}
	if code, o, e := run(t, "", "verify"); code != 0 {
		t.Fatalf("verify failed after ts verifier toggles: %s%s", o, e)
	}
	code, out, _ := run(t, "", "lock-query", "web", "--json")
	if code != 0 || !strings.Contains(out, `"audit": true`) || !strings.Contains(out, `"semgrep": true`) {
		t.Fatalf("ts verifiers not persisted: %s", out)
	}
	// coverageMin lives in the lock file (not the lock-query projection).
	if lb, err := os.ReadFile(filepath.Join(root, "veracity.lock.json")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(lb), `"coverageMin": 75`) {
		t.Errorf("coverageMin 75 not persisted to lock:\n%s", lb)
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
