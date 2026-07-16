package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/lockfile"
)

func TestContentHashStableAndSensitive(t *testing.T) {
	root, lock, proj := goProject(t)
	d1, _ := ExtractProject(root, lock, proj)
	d2, _ := ExtractProject(root, lock, proj)
	if d1.Modules[0].ContentHash != d2.Modules[0].ContentHash {
		t.Fatal("module content hash not stable across runs")
	}
	if d1.Modules[0].Interfaces[0].ContentHash == "" {
		t.Fatal("interface content hash empty")
	}
	// Editing the module changes its hash.
	writeF(t, root, "greeter/extra.go", "package greeter\n\n// Extra does nothing.\nfunc Extra() {}\n")
	d3, _ := ExtractProject(root, lock, proj)
	if d3.Modules[0].ContentHash == d1.Modules[0].ContentHash {
		t.Fatal("module content hash should change after an edit")
	}
}

func TestStatusEnrichRoundTrip(t *testing.T) {
	root, lock, proj := goProject(t)

	rep, err := Status(root, lock, proj)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PendingCount() == 0 {
		t.Fatal("expected pending summaries on a fresh project")
	}
	modID := rep.Modules[0].ID

	// Enrich the module + its interface.
	enr := `{"modules":{"` + modID + `":{"summary":"Greets people.","interfaces":{"Greeter":"The greeting contract."}}}}`
	res, err := Enrich(root, lock, proj, []byte(enr))
	if err != nil {
		t.Fatal(err)
	}
	if res.Modules != 1 || res.Interfaces != 1 {
		t.Fatalf("unexpected enrich result: %+v", res)
	}

	// Now that module is fresh.
	rep2, _ := Status(root, lock, proj)
	if rep2.Modules[0].NeedsSummary {
		t.Fatal("module should be fresh after enrich")
	}
	if rep2.Modules[0].Summary != "Greets people." {
		t.Fatalf("summary not stored: %q", rep2.Modules[0].Summary)
	}

	// Editing the source makes it stale again.
	writeF(t, root, "greeter/more.go", "package greeter\n\n// More is extra.\nfunc More() {}\n")
	rep3, _ := Status(root, lock, proj)
	if !rep3.Modules[0].NeedsSummary {
		t.Fatal("module summary should be stale after a source edit")
	}
}

func TestEnrichAcceptsArrayForm(t *testing.T) {
	root, lock, proj := goProject(t)
	// The array shape an agent might guess first must be accepted.
	arr := `{"modules":[{"id":"example.com/demo/greeter","summary":"Greets.","interfaces":{"Greeter":"Contract."}}]}`
	res, err := Enrich(root, lock, proj, []byte(arr))
	if err != nil {
		t.Fatalf("array form should be accepted: %v", err)
	}
	if res.Modules != 1 || res.Interfaces != 1 {
		t.Fatalf("array form stored wrong counts: %+v", res)
	}
	rep, _ := Status(root, lock, proj)
	if rep.Modules[0].Summary != "Greets." {
		t.Fatalf("array-form summary not stored: %q", rep.Modules[0].Summary)
	}
}

func TestEnrichTemplateListsPending(t *testing.T) {
	root, lock, proj := goProject(t)
	rep, _ := Status(root, lock, proj)
	tmpl, err := rep.EnrichTemplate()
	if err != nil {
		t.Fatal(err)
	}
	// The template must be valid enrich input the parser accepts round-trip.
	var in EnrichInput
	if err := in.UnmarshalJSON(tmpl); err != nil {
		t.Fatalf("template is not valid enrich input: %v\n%s", err, tmpl)
	}
	if _, ok := in.Modules["example.com/demo/greeter"]; !ok {
		t.Fatalf("template missing the pending module: %s", tmpl)
	}
}

func TestEnrichRejectsUnknownIDs(t *testing.T) {
	root, lock, proj := goProject(t)
	if _, err := Enrich(root, lock, proj, []byte(`{"modules":{"nope/bogus":{"summary":"x"}}}`)); err == nil {
		t.Fatal("expected unknown module id to be rejected")
	}
	modID := "example.com/demo/greeter"
	bad := `{"modules":{"` + modID + `":{"interfaces":{"Ghost":"x"}}}}`
	if _, err := Enrich(root, lock, proj, []byte(bad)); err == nil {
		t.Fatal("expected unknown interface name to be rejected")
	}
}

func TestRenderSVGAndHTML(t *testing.T) {
	root, lock, proj := goProject(t)
	proj.Features.Markdown = true
	proj.Features.Diagrams = true
	lock.Projects[0].Features = proj.Features

	// Enrich so summaries appear in output.
	_, _ = Enrich(root, lock, proj, []byte(`{"modules":{"example.com/demo/greeter":{"summary":"Greets people warmly.","interfaces":{"Greeter":"The greeting boundary."}}}}`))

	doc, _ := ExtractProject(root, lock, proj)
	store, _ := loadSummaries(projectDir(root, lock, proj))

	svg := RenderSVG(doc, store)
	svgStr := string(svg)
	if !strings.HasPrefix(svgStr, "<svg") || !strings.Contains(svgStr, "</svg>") {
		t.Fatal("SVG malformed")
	}
	for _, want := range []string{"greeter", "interface Greeter", "Greet(name string) string", "Greets people warmly."} {
		if !strings.Contains(svgStr, want) {
			t.Errorf("SVG missing %q", want)
		}
	}
	// Deterministic.
	if string(RenderSVG(doc, store)) != svgStr {
		t.Fatal("SVG render not deterministic")
	}

	html := string(RenderHTML(doc, store))
	if !strings.Contains(html, "<!doctype html>") || !strings.Contains(html, "The greeting boundary.") {
		t.Fatal("HTML missing expected content")
	}
}

func TestRenderSVGEscapesMarkup(t *testing.T) {
	root := t.TempDir()
	writeF(t, root, "go.mod", "module example.com/x\n\ngo 1.24\n")
	// A doc comment containing HTML/script must be escaped in the SVG.
	writeF(t, root, "m/m.go", "// Package m has <script>alert(1)</script> in its doc.\npackage m\n\n// I is an iface.\ntype I interface{ F() }\n")
	lock := &lockfile.Lock{
		SchemaVersion: lockfile.SchemaVersion, Layout: lockfile.LayoutSingle,
		Projects: []lockfile.Project{{Name: "x", Language: lockfile.LangGo, ModulePath: "example.com/x", Features: lockfile.Features{Diagrams: true, Markdown: true}}},
	}
	doc, _ := ExtractProject(root, lock, lock.Projects[0])
	// Put hostile prose into the store too.
	_, _ = Enrich(root, lock, lock.Projects[0], []byte(`{"modules":{"example.com/x/m":{"summary":"<img src=x onerror=alert(1)>"}}}`))
	store, _ := loadSummaries(root)
	svg := string(RenderSVG(doc, store))
	if strings.Contains(svg, "<script>") || strings.Contains(svg, "<img src=x") {
		t.Fatalf("SVG did not escape hostile markup:\n%s", svg)
	}
}

func TestRenderProjectWritesFiles(t *testing.T) {
	root, lock, _ := goProject(t)
	lock.Projects[0].Features.Markdown = true
	lock.Projects[0].Features.Diagrams = true
	proj := lock.Projects[0]

	wrote, err := RenderProject(root, lock, proj, false)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("first render should write")
	}
	for _, f := range []string{"docs/MODULES.md", "docs/modules.svg", "docs/modules.html"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
			t.Errorf("expected %s: %v", f, err)
		}
	}
	// Second render is a no-op (byte-identical).
	wrote, _ = RenderProject(root, lock, proj, false)
	if wrote {
		t.Fatal("unchanged render should be a no-op")
	}
}
