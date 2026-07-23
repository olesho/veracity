package extract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olesho/veracity/internal/docgen/ir"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEdgeClassification checks the strict implements/depends-on labeling: an
// implementer of another module's interface gets "implements"; a mere consumer
// gets "depends on".
func TestEdgeClassification(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/x\n\ngo 1.24\n")
	// store: defines the boundary.
	write(t, root, "store/store.go", `package store

// Store is the persistence boundary.
type Store interface {
	Get(id int) (string, error)
	Put(id int, v string) error
}
`)
	// mem: implements Store (has both methods) — should be "implements".
	write(t, root, "mem/mem.go", `package mem

import "example.com/x/store"

// Mem is an in-memory Store.
type Mem struct{ m map[int]string }

func (Mem) Get(id int) (string, error) { return "", nil }
func (Mem) Put(id int, v string) error { return nil }

var _ store.Store = Mem{}
`)
	// app: consumes Store but does not implement it — should be "depends on".
	write(t, root, "app/app.go", `package app

import "example.com/x/store"

// App uses a Store.
type App struct{ s store.Store }

func (a *App) Run() {}
`)

	doc, err := Go(root, "example.com/x", "x")
	if err != nil {
		t.Fatal(err)
	}
	rel := map[string]string{} // from-shortname -> relationship
	for _, e := range doc.Edges {
		if e.To == "example.com/x/store" {
			rel[e.From] = e.Rel
		}
	}
	if got := rel["example.com/x/mem"]; got != ir.RelImplements {
		t.Errorf("mem→store = %q, want implements", got)
	}
	if got := rel["example.com/x/app"]; got != ir.RelDependsOn {
		t.Errorf("app→store = %q, want depends on", got)
	}

	// Per-interface implementers/consumers.
	var store *ir.Interface
	for mi := range doc.Modules {
		for ii := range doc.Modules[mi].Interfaces {
			if doc.Modules[mi].Interfaces[ii].Name == "Store" {
				store = &doc.Modules[mi].Interfaces[ii]
			}
		}
	}
	if store == nil {
		t.Fatal("Store interface not found")
	}
	if len(store.Implementers) != 1 || store.Implementers[0].Module != "example.com/x/mem" || store.Implementers[0].Type != "Mem" {
		t.Errorf("Store.Implementers = %+v, want [{mem Mem}]", store.Implementers)
	}
	if len(store.Consumers) != 1 || store.Consumers[0] != "example.com/x/app" {
		t.Errorf("Store.Consumers = %+v, want [app]", store.Consumers)
	}
}
