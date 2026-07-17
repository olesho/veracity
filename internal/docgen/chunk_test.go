package docgen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/olesho/harness/internal/docgen/ir"
)

func modulesUnder(prefix string, n int) []ir.Module {
	var out []ir.Module
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("%s/m%d", prefix, i)
		out = append(out, ir.Module{ID: "x/" + p, Name: fmt.Sprintf("m%d", i), Path: p})
	}
	return out
}

func TestGroupModulesThreshold(t *testing.T) {
	small := modulesUnder("internal/a", 5)
	groups, chunked := groupModules(small)
	if chunked {
		t.Fatal("5 modules should not chunk")
	}
	if len(groups) != 1 {
		t.Fatalf("small should be one group, got %d", len(groups))
	}
}

func TestGroupModulesDepthSelection(t *testing.T) {
	// 16 modules under internal/a (8) and internal/b (8): depth 1 ("internal")
	// would be one group of 16 (> maxGroupSize), so depth 2 must be chosen.
	mods := append(modulesUnder("internal/a", 8), modulesUnder("internal/b", 8)...)
	groups, chunked := groupModules(mods)
	if !chunked {
		t.Fatal("16 modules should chunk")
	}
	if len(groups) != 2 {
		keys := []string{}
		for _, g := range groups {
			keys = append(keys, g.Key)
		}
		t.Fatalf("expected 2 groups (internal/a, internal/b), got %d: %v", len(groups), keys)
	}
	if groups[0].Key != "internal/a" || groups[1].Key != "internal/b" {
		t.Fatalf("unexpected group keys: %s, %s", groups[0].Key, groups[1].Key)
	}
}

func TestBuildDiagramsChunked(t *testing.T) {
	mods := append(modulesUnder("internal/a", 8), modulesUnder("internal/b", 8)...)
	doc := ir.IR{
		SchemaVersion: ir.SchemaVersion, Subproject: "big", Language: "go",
		Modules: mods,
		Edges: []ir.Edge{
			{From: "x/internal/a/m0", To: "x/internal/b/m0", Rel: ir.RelDependsOn},
		},
	}
	ds := BuildDiagrams(doc)
	if !ds.Chunked {
		t.Fatal("expected chunked output")
	}
	if len(ds.Overview) == 0 {
		t.Fatal("expected an overview SVG")
	}
	// The overview names the groups and draws the cross-group dependency edge.
	ov := string(ds.Overview)
	if !strings.Contains(ov, "internal/a") || !strings.Contains(ov, "internal/b") {
		t.Errorf("overview missing group labels:\n%s", ov[:min(len(ov), 400)])
	}
	if !strings.Contains(ov, "<line ") {
		t.Errorf("overview missing the cross-group dependency edge")
	}
}

func TestBuildDiagramsSingle(t *testing.T) {
	doc := ir.IR{SchemaVersion: ir.SchemaVersion, Subproject: "s", Modules: modulesUnder("pkg", 3)}
	ds := BuildDiagrams(doc)
	if ds.Chunked {
		t.Fatal("3 modules should be a single diagram")
	}
	if len(ds.Whole) == 0 || len(ds.Overview) != 0 {
		t.Fatal("single mode should populate Whole, not Overview")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
