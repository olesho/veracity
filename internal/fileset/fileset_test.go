package fileset

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
)

func singleLock() *lockfile.Lock {
	return &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutSingle,
		Projects: []lockfile.Project{{Name: "app", Language: lockfile.LangGo, ModulePath: "example.com/app"}},
	}
}

func monorepoLock() *lockfile.Lock {
	return &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutMonorepo,
		Projects: []lockfile.Project{
			{Name: "api", Language: lockfile.LangGo, ModulePath: "example.com/api"},
			{Name: "web", Language: lockfile.LangTS},
		},
	}
}

func TestIgnored(t *testing.T) {
	ignored := []string{"vendor/x.go", "a/node_modules/b.ts", ".venv/lib/x.py", "projects/api/.git/x", "a/testdata/y.go", ".harness/manifest.json"}
	kept := []string{"a.go", "src/x.ts", "eslint.config.js", "projects/api/pkg/x.go"}
	for _, p := range ignored {
		if !Ignored(p) {
			t.Errorf("expected %q to be ignored", p)
		}
	}
	for _, p := range kept {
		if Ignored(p) {
			t.Errorf("expected %q to be kept", p)
		}
	}
}

func TestProjectOfSingle(t *testing.T) {
	l := singleLock()
	p, ok := ProjectOf(l, "pkg/x.go")
	if !ok || p.Name != "app" {
		t.Fatalf("single ProjectOf = %v,%v", p, ok)
	}
}

func TestProjectOfMonorepo(t *testing.T) {
	l := monorepoLock()
	p, ok := ProjectOf(l, "projects/api/pkg/x.go")
	if !ok || p.Name != "api" {
		t.Fatalf("expected api, got %v,%v", p, ok)
	}
	if _, ok := ProjectOf(l, "docs/README.md"); ok {
		t.Fatal("root-level file should belong to no project")
	}
}

func TestGroupByProject(t *testing.T) {
	l := monorepoLock()
	got := GroupByProject(l, []string{
		"projects/api/a.go", "projects/api/b.go", "projects/web/x.ts", "docs/README.md", "vendor/z.go",
	})
	if !reflect.DeepEqual(got["api"], []string{"projects/api/a.go", "projects/api/b.go"}) {
		t.Fatalf("api group wrong: %v", got["api"])
	}
	if len(got["web"]) != 1 {
		t.Fatalf("web group wrong: %v", got["web"])
	}
}

func TestRelToProject(t *testing.T) {
	if got := RelToProject(singleLock(), singleLock().Projects[0], "pkg/x.go"); got != "pkg/x.go" {
		t.Fatalf("single RelToProject = %q", got)
	}
	l := monorepoLock()
	if got := RelToProject(l, l.Projects[0], "projects/api/pkg/x.go"); got != "pkg/x.go" {
		t.Fatalf("monorepo RelToProject = %q", got)
	}
}

func TestFilterByLanguage(t *testing.T) {
	l := monorepoLock()
	got := FilterByLanguage(l, l.Projects[0], []string{
		"projects/api/a.go", "projects/api/README.md", "projects/api/vendor/z.go", "projects/web/x.ts",
	})
	if !reflect.DeepEqual(got, []string{"a.go"}) {
		t.Fatalf("FilterByLanguage = %v, want [a.go]", got)
	}
}

func TestEligibleFiles(t *testing.T) {
	root := t.TempDir()
	l := monorepoLock()
	mk := func(rel string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("projects/api/main.go")
	mk("projects/api/pkg/util.go")
	mk("projects/api/vendor/dep.go") // ignored
	mk("projects/api/README.md")     // wrong ext

	got, err := EligibleFiles(root, l, l.Projects[0])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"main.go", "pkg/util.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EligibleFiles = %v, want %v", got, want)
	}
}
