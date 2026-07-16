package toolchain

import (
	"encoding/json"
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
