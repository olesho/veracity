package lockfile

import (
	"os"
	"path/filepath"
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
