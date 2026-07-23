package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olesho/veracity/internal/docgen/ir"
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

	// RenderSVG is the simple module dependency graph (node names, no interface
	// detail).
	svgStr := string(RenderSVG(doc))
	if !strings.HasPrefix(svgStr, "<svg") || !strings.Contains(svgStr, "</svg>") {
		t.Fatal("SVG malformed")
	}
	if !strings.Contains(svgStr, "greeter") {
		t.Errorf("module graph SVG missing the module name")
	}
	if string(RenderSVG(doc)) != svgStr {
		t.Fatal("SVG render not deterministic")
	}

	// The interface detail lives in the HTML gallery.
	html := string(RenderHTML(doc, store))
	for _, want := range []string{"<!doctype html>", "interface Greeter", "Greet(name string) string", "The greeting boundary."} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func TestRenderInterfaceSVG(t *testing.T) {
	mod := ir.Module{ID: "x/store", Name: "store", Path: "store"}
	iface := ir.Interface{
		Name:         "Store",
		Methods:      []ir.Method{{Signature: "Get(id int) (string, error)"}},
		Consumers:    []string{"x/app", "x/cli"},
		Implementers: []ir.Implementer{{Module: "x/mem", Type: "Mem"}, {Module: "x/sql", Type: "SQL"}},
	}
	svg := string(RenderInterfaceSVG(mod, iface, nil))
	for _, want := range []string{"interface Store", "depends on", "implements", ">app<", ">cli<", ">Mem<", ">SQL<", "Get(id int)"} {
		if !strings.Contains(svg, want) {
			t.Errorf("interface SVG missing %q", want)
		}
	}
	if string(RenderInterfaceSVG(mod, iface, nil)) != svg {
		t.Fatal("interface SVG not deterministic")
	}
	// Hostile consumer name must be escaped.
	bad := ir.Interface{Name: "Y", Consumers: []string{"x/<script>"}, Methods: iface.Methods}
	if strings.Contains(string(RenderInterfaceSVG(mod, bad, nil)), "<script>") {
		t.Fatal("interface SVG did not escape a hostile consumer name")
	}
}

func TestRenderSVGEscapesMarkup(t *testing.T) {
	// A crafted hostile module name/path must be escaped in the dependency graph.
	doc := ir.IR{Subproject: "x", Modules: []ir.Module{
		{ID: "x/m", Name: "<script>alert(1)</script>", Path: "m<b>"},
	}}
	svg := string(RenderSVG(doc))
	if strings.Contains(svg, "<script>") || strings.Contains(svg, "<b>") {
		t.Fatalf("module graph did not escape hostile markup:\n%s", svg)
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
