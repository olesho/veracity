package lockfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validLock() *Lock {
	return &Lock{
		SchemaVersion: SchemaVersion,
		Layout:        LayoutSingle,
		Capabilities:  Capabilities{Agents: []string{AgentClaude}, GitHooks: true},
		Projects: []Project{{
			Name:       "myproj",
			Language:   LangGo,
			ModulePath: "example.com/myproj",
			Features:   Features{Lint: true, Test: true},
		}},
	}
}

func TestValidateName(t *testing.T) {
	good := []string{"api", "web-app", "a", "svc1", "a-b-c-2"}
	bad := []string{"", "Api", "1api", "-api", "api_", "api/x", "..", "a b", "a.b"}
	for _, n := range good {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) unexpected error: %v", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) expected error", n)
		}
	}
}

func TestValidateModulePath(t *testing.T) {
	good := []string{"example.com/myproj", "github.com/org/repo", "internal.host/a/b/c", "example.com/my-proj"}
	bad := []string{"", "/leading", "trailing/", "has space", "a//b", "example.com/../evil", "example.com/."}
	for _, p := range good {
		if err := ValidateModulePath(p); err != nil {
			t.Errorf("ValidateModulePath(%q) unexpected error: %v", p, err)
		}
	}
	for _, p := range bad {
		if err := ValidateModulePath(p); err == nil {
			t.Errorf("ValidateModulePath(%q) expected error", p)
		}
	}
}

func TestDerivePath(t *testing.T) {
	if got := DerivePath(LayoutSingle, "x"); got != "." {
		t.Errorf("single path = %q, want .", got)
	}
	if got := DerivePath(LayoutMonorepo, "api"); got != "projects/api" {
		t.Errorf("monorepo path = %q, want projects/api", got)
	}
}

func TestValidateInvariants(t *testing.T) {
	// diagrams without markdown must fail
	l := validLock()
	l.Projects[0].Features.Diagrams = true
	l.Projects[0].Features.Markdown = false
	if err := l.Validate(); err == nil {
		t.Fatal("expected diagrams-without-markdown to fail validation")
	}

	// single layout with 2 projects must fail
	l = validLock()
	l.Projects = append(l.Projects, Project{Name: "other", Language: LangPython})
	if err := l.Validate(); err == nil {
		t.Fatal("expected single-layout-with-2-projects to fail validation")
	}

	// duplicate names must fail
	l = &Lock{SchemaVersion: SchemaVersion, Layout: LayoutMonorepo,
		Projects: []Project{
			{Name: "api", Language: LangGo, ModulePath: "example.com/api"},
			{Name: "api", Language: LangPython},
		}}
	if err := l.Validate(); err == nil {
		t.Fatal("expected duplicate names to fail validation")
	}

	// modulePath on non-go must fail
	l = &Lock{SchemaVersion: SchemaVersion, Layout: LayoutSingle,
		Projects: []Project{{Name: "p", Language: LangPython, ModulePath: "x/y"}}}
	if err := l.Validate(); err == nil {
		t.Fatal("expected modulePath-on-python to fail validation")
	}
}

func TestValidateFeatureLanguageGuard(t *testing.T) {
	// Each Go-only verifier flag on a python project must fail.
	for _, mut := range []func(*Features){
		func(f *Features) { f.Gofumpt = true },
		func(f *Features) { f.Gci = true },
		func(f *Features) { f.ModTidy = true },
		func(f *Features) { f.Coverage = true },
	} {
		l := &Lock{SchemaVersion: SchemaVersion, Layout: LayoutSingle,
			Projects: []Project{{Name: "p", Language: LangPython}}}
		mut(&l.Projects[0].Features)
		if err := l.Validate(); err == nil {
			t.Errorf("expected go/ts-only verifier on python to fail: %+v", l.Projects[0].Features)
		}
	}

	// Go-only verifiers must also fail on a TypeScript project.
	for _, mut := range []func(*Features){
		func(f *Features) { f.Gofumpt = true },
		func(f *Features) { f.Gci = true },
		func(f *Features) { f.ModTidy = true },
	} {
		l := &Lock{SchemaVersion: SchemaVersion, Layout: LayoutSingle,
			Projects: []Project{{Name: "p", Language: LangTS}}}
		mut(&l.Projects[0].Features)
		if err := l.Validate(); err == nil {
			t.Errorf("expected go-only verifier on ts to fail: %+v", l.Projects[0].Features)
		}
	}

	// The TS-only audit verifier must fail on Go.
	l := validLock()
	l.Projects[0].Features.Audit = true
	if err := l.Validate(); err == nil {
		t.Fatal("expected audit on go to fail validation")
	}

	// coverageMin on python must fail (coverage is not valid for python).
	l = &Lock{SchemaVersion: SchemaVersion, Layout: LayoutSingle,
		Projects: []Project{{Name: "p", Language: LangPython, CoverageMin: 50}}}
	if err := l.Validate(); err == nil {
		t.Fatal("expected coverageMin on python to fail validation")
	}

	// coverageMin out of range on go must fail.
	for _, v := range []int{-1, 101} {
		l := validLock()
		l.Projects[0].Features.Coverage = true
		l.Projects[0].CoverageMin = v
		if err := l.Validate(); err == nil {
			t.Errorf("expected coverageMin %d to fail validation", v)
		}
	}

	// Valid Go project with all Go verifiers on and a bounded coverageMin passes.
	l = validLock()
	l.Projects[0].Features.Gofumpt = true
	l.Projects[0].Features.Gci = true
	l.Projects[0].Features.ModTidy = true
	l.Projects[0].Features.Coverage = true
	l.Projects[0].CoverageMin = 80
	if err := l.Validate(); err != nil {
		t.Fatalf("valid go verifiers rejected: %v", err)
	}

	// Valid TypeScript project with the TS verifiers (coverage/audit/semgrep) on
	// and a bounded coverageMin passes.
	l = &Lock{SchemaVersion: SchemaVersion, Layout: LayoutSingle,
		Projects: []Project{{Name: "web", Language: LangTS,
			Features:    Features{Lint: true, Test: true, Coverage: true, Audit: true, Semgrep: true},
			CoverageMin: 75}}}
	if err := l.Validate(); err != nil {
		t.Fatalf("valid ts verifiers rejected: %v", err)
	}
}

func TestFeaturesEnabled(t *testing.T) {
	f := Features{Lint: true, Gofumpt: true, Coverage: true, Audit: true, Semgrep: true}
	cases := map[string]bool{
		"lint": true, "test": false, "markdown": false, "diagrams": false,
		"gofumpt": true, "gci": false, "modTidy": false, "coverage": true,
		"audit": true, "semgrep": true, "sonar": false, "unknown": false,
	}
	for name, want := range cases {
		if got := f.Enabled(name); got != want {
			t.Errorf("Enabled(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMarshalOmitsUnsetVerifiers(t *testing.T) {
	// A standard Go lock (verifiers off) must not carry the Go-only keys, so
	// existing/default locks stay byte-identical to pre-verifier locks.
	b, err := Marshal(validLock())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"gofumpt", "gci", "modTidy", "coverage", "coverageMin"} {
		if strings.Contains(string(b), key) {
			t.Errorf("unset verifier key %q should be omitted, got:\n%s", key, b)
		}
	}
	// When on, they appear.
	l := validLock()
	l.Projects[0].Features.Gofumpt = true
	l.Projects[0].Features.Coverage = true
	l.Projects[0].CoverageMin = 80
	b, _ = Marshal(l)
	for _, key := range []string{"gofumpt", "coverage", "coverageMin"} {
		if !strings.Contains(string(b), key) {
			t.Errorf("enabled key %q should be present, got:\n%s", key, b)
		}
	}
}

func TestMarshalDeterministicRoundTrip(t *testing.T) {
	l := validLock()
	b1, err := Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatal("Marshal is not deterministic")
	}
	if b1[len(b1)-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
	got, err := Unmarshal(b1)
	if err != nil {
		t.Fatalf("round-trip Unmarshal: %v", err)
	}
	b3, _ := Marshal(got)
	if string(b3) != string(b1) {
		t.Fatal("round-trip not stable")
	}
}

func TestUnmarshalRejectsUnknownFields(t *testing.T) {
	data := []byte(`{"schemaVersion":1,"layout":"single","capabilities":{},"projects":[{"name":"p","language":"go","modulePath":"x/y","features":{}}],"bogus":1}`)
	if _, err := Unmarshal(data); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	l := validLock()
	b, _ := Marshal(l)
	if err := os.WriteFile(filepath.Join(dir, FileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if !Exists(dir) {
		t.Fatal("Exists should report true")
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Projects[0].Name != "myproj" {
		t.Fatalf("loaded wrong project: %+v", got)
	}
}
