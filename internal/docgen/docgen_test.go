package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
)

func goProject(t *testing.T) (string, *lockfile.Lock, lockfile.Project) {
	t.Helper()
	root := t.TempDir()
	writeF(t, root, "go.mod", "module example.com/demo\n\ngo 1.24\n")
	writeF(t, root, "greeter/greeter.go", `// Package greeter greets people.
package greeter

// Greeter is the greeting boundary.
type Greeter interface {
	// Greet greets a name.
	Greet(name string) string
}

// EnglishGreeter greets in English.
type EnglishGreeter struct{}

// Greet returns a greeting.
func (EnglishGreeter) Greet(name string) string { return "Hello, " + name }

// New constructs an EnglishGreeter.
func New() EnglishGreeter { return EnglishGreeter{} }
`)
	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutSingle,
		Projects: []lockfile.Project{{
			Name: "demo", Language: lockfile.LangGo, ModulePath: "example.com/demo",
			Features: lockfile.Features{Markdown: true},
		}},
	}
	return root, lock, lock.Projects[0]
}

func writeF(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractGo(t *testing.T) {
	root, lock, proj := goProject(t)
	doc, err := ExtractProject(root, lock, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(doc.Modules))
	}
	m := doc.Modules[0]
	if m.ID != "example.com/demo/greeter" {
		t.Errorf("module ID = %q", m.ID)
	}
	if m.DocComment == "" || !strings.Contains(m.DocComment, "greets people") {
		t.Errorf("missing package doc: %q", m.DocComment)
	}
	if len(m.Interfaces) != 1 || m.Interfaces[0].Name != "Greeter" {
		t.Fatalf("expected Greeter interface, got %+v", m.Interfaces)
	}
	if len(m.Interfaces[0].Methods) != 1 || !strings.Contains(m.Interfaces[0].Methods[0].Signature, "Greet(name string) string") {
		t.Errorf("method signature wrong: %+v", m.Interfaces[0].Methods)
	}
	// New() constructor should be an exported func.
	foundNew := false
	for _, e := range m.Exports {
		if e.Name == "New" && e.Kind == "func" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("expected New() func export, got %+v", m.Exports)
	}
}

func TestRenderMarkdownDeterministic(t *testing.T) {
	root, lock, proj := goProject(t)
	doc, _ := ExtractProject(root, lock, proj)
	a := RenderMarkdown(doc, nil)
	b := RenderMarkdown(doc, nil)
	if string(a) != string(b) {
		t.Fatal("markdown render not deterministic")
	}
	for _, want := range []string{"# demo — Modules", "## Module: greeter", "#### `Greeter`", "Greet(name string) string"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}

func TestMarkdownProjectCache(t *testing.T) {
	root, lock, proj := goProject(t)
	out := filepath.Join(root, "docs", "MODULES.md")

	// First run writes.
	wrote, err := MarkdownProject(root, lock, proj, false)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("first run should write")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("MODULES.md not written: %v", err)
	}

	// Second run is a cache no-op.
	wrote, err = MarkdownProject(root, lock, proj, false)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatal("unchanged project should be a cache no-op")
	}

	// Deleting the output must force regeneration (cache requires output present).
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	wrote, err = MarkdownProject(root, lock, proj, false)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("deleted output must be regenerated, not skipped")
	}
}
