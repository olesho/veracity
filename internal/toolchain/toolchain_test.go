package toolchain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
)

func TestEveryLanguageHasEveryPhase(t *testing.T) {
	for _, lang := range Languages() {
		for _, ph := range Phases {
			if len(Commands(lang, ph)) == 0 {
				t.Errorf("language %q phase %q has no commands", lang, ph)
			}
		}
	}
}

func TestGoFileLintFailsOnStdout(t *testing.T) {
	// gofmt -l always exits 0 and lists offenders on stdout, so the harness must
	// treat nonempty stdout as failure.
	cmds := Commands(lockfile.LangGo, PhaseFileLint)
	if len(cmds) != 1 || !cmds[0].FailOnStdout {
		t.Fatalf("go file_lint should be a single FailOnStdout gofmt -l, got %+v", cmds)
	}
	if cmds[0].Files != AppendFiles {
		t.Fatalf("gofmt -l must receive an explicit file list, got %q", cmds[0].Files)
	}
}

func TestProjectLintUsesProjectForm(t *testing.T) {
	// Project-oriented analyzers must not receive per-file args.
	for _, tc := range []struct{ lang, want string }{
		{lockfile.LangGo, "go"},
		{lockfile.LangPython, "mypy"},
		{lockfile.LangTS, "tsc"},
	} {
		found := false
		for _, c := range Commands(tc.lang, PhaseProjectLint) {
			for _, a := range c.Argv {
				if a == tc.want {
					if c.Files != NoFiles {
						t.Errorf("%s project_lint %q should be NoFiles", tc.lang, tc.want)
					}
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s project_lint missing %q", tc.lang, tc.want)
		}
	}
}

func TestJSONDeterministicAndValid(t *testing.T) {
	b1, err := JSON(lockfile.LangGo)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := JSON(lockfile.LangGo)
	if string(b1) != string(b2) {
		t.Fatal("toolchain JSON not deterministic")
	}
	var view map[string]any
	if err := json.Unmarshal(b1, &view); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if view["language"] != "go" {
		t.Fatalf("unexpected language: %v", view["language"])
	}
}

func TestJSONUnknownLanguage(t *testing.T) {
	if _, err := JSON("cobol"); err == nil {
		t.Fatal("expected error for unknown language")
	}
}

func TestAllPhasesGoOnlyVerifiers(t *testing.T) {
	has := func(phs []Phase, p Phase) bool {
		for _, x := range phs {
			if x == p {
				return true
			}
		}
		return false
	}
	goPhases := AllPhases(lockfile.LangGo)
	for _, p := range goVerifierPhases {
		if !has(goPhases, p) {
			t.Errorf("AllPhases(go) missing verifier phase %q", p)
		}
		if len(Commands(lockfile.LangGo, p)) == 0 {
			t.Errorf("go verifier phase %q has no commands", p)
		}
	}
	// Other languages must not carry the Go-only verifier phases.
	for _, lang := range []string{lockfile.LangPython, lockfile.LangTS} {
		for _, p := range goVerifierPhases {
			if has(AllPhases(lang), p) {
				t.Errorf("AllPhases(%s) unexpectedly includes %q", lang, p)
			}
			if len(Commands(lang, p)) != 0 {
				t.Errorf("%s should have no commands for verifier phase %q", lang, p)
			}
		}
	}
}

func TestFormatPhaseGatesFormatters(t *testing.T) {
	// gci/gofumpt in the Go format phase must be gated by their feature; gofmt
	// runs unconditionally, and gofumpt is last so the result is idempotent.
	cmds := Commands(lockfile.LangGo, PhaseFormat)
	gates := map[string]string{}
	order := []string{}
	for _, c := range cmds {
		order = append(order, c.Argv[0])
		gates[c.Argv[0]] = c.Gate
	}
	if gates["gofmt"] != "" {
		t.Errorf("gofmt must be ungated, got gate %q", gates["gofmt"])
	}
	if gates["gci"] != "gci" {
		t.Errorf("gci write must be gated on gci, got %q", gates["gci"])
	}
	if gates["gofumpt"] != "gofumpt" {
		t.Errorf("gofumpt -w must be gated on gofumpt, got %q", gates["gofumpt"])
	}
	if len(order) < 3 || order[len(order)-1] != "gofumpt" {
		t.Errorf("gofumpt must be the last format command, order=%v", order)
	}
}

func TestGoAnalyzersHaveModuleAndFeature(t *testing.T) {
	want := map[string]string{"golangci-lint": "lint", "gofumpt": "gofumpt", "gci": "gci"}
	got := map[string]bool{}
	for _, a := range Analyzers(lockfile.LangGo) {
		if a.Module == "" {
			t.Errorf("analyzer %q missing Module", a.Name)
		}
		if a.Version == "" {
			t.Errorf("analyzer %q missing Version", a.Name)
		}
		if want[a.Name] != a.Feature {
			t.Errorf("analyzer %q feature = %q, want %q", a.Name, a.Feature, want[a.Name])
		}
		got[a.Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("missing analyzer %q", name)
		}
	}
}

func TestJSONIncludesGateAndVerifierPhases(t *testing.T) {
	b, err := JSON(lockfile.LangGo)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"gofumpt"`, `"gci"`, `"mod_tidy"`, `"coverage"`, `"gate"`} {
		if !strings.Contains(s, want) {
			t.Errorf("go toolchain JSON missing %s:\n%s", want, s)
		}
	}
}
