package docgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/olesho/harness/internal/docgen/ir"
)

// Chunking thresholds. A project with more than chunkThreshold modules is split
// into per-package-group diagrams plus a high-level overview, so no single SVG
// becomes unreadable.
const (
	chunkThreshold = 12
	maxGroupSize   = 12
	maxGroupDepth  = 4
)

// group is a set of modules sharing an import-path prefix.
type group struct {
	Key     string
	Modules []ir.Module
}

// GroupDiagram is one package group's rendered detail diagram.
type GroupDiagram struct {
	Key     string
	Slug    string
	SVG     []byte
	Modules []ir.Module
}

// DiagramSet is the full rendered output for a project: either a single
// whole-graph SVG (small projects) or an overview plus per-group SVGs (large).
type DiagramSet struct {
	Chunked  bool
	Whole    []byte
	Overview []byte
	Groups   []GroupDiagram
}

// BuildDiagrams renders a project's diagrams, chunking by package group when the
// module count exceeds the threshold.
func BuildDiagrams(doc ir.IR, store *Summaries) DiagramSet {
	if store == nil {
		store = newSummaries()
	}
	groups, chunked := groupModules(doc.Modules)
	if !chunked {
		return DiagramSet{Chunked: false, Whole: RenderSVG(doc, store)}
	}
	groupOf := moduleGroupMap(groups)
	ds := DiagramSet{Chunked: true, Overview: renderOverviewSVG(groups, doc.Edges)}
	for _, g := range groups {
		ds.Groups = append(ds.Groups, GroupDiagram{
			Key:     g.Key,
			Slug:    groupSlug(g.Key),
			SVG:     renderGroupSVG(g, doc.Edges, groupOf, store),
			Modules: g.Modules,
		})
	}
	return ds
}

// groupModules buckets modules by an import-path prefix chosen so groups stay a
// readable size. Returns (groups, chunked); when not chunked there is a single
// group holding every module.
func groupModules(mods []ir.Module) ([]group, bool) {
	if len(mods) <= chunkThreshold {
		return []group{{Key: "", Modules: mods}}, false
	}
	depth := chooseGroupDepth(mods)
	byKey := map[string][]ir.Module{}
	for _, m := range mods {
		k := groupKey(m.Path, depth)
		byKey[k] = append(byKey[k], m)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var groups []group
	for _, k := range keys {
		ms := byKey[k]
		sort.SliceStable(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
		groups = append(groups, group{Key: k, Modules: ms})
	}
	return groups, true
}

func groupKey(path string, depth int) string {
	if path == "." || path == "" {
		return "(root)"
	}
	segs := strings.Split(path, "/")
	if len(segs) <= depth {
		return path
	}
	return strings.Join(segs[:depth], "/")
}

// chooseGroupDepth picks the shallowest prefix depth at which no group exceeds
// maxGroupSize (capped at maxGroupDepth).
func chooseGroupDepth(mods []ir.Module) int {
	for d := 1; d <= maxGroupDepth; d++ {
		counts := map[string]int{}
		for _, m := range mods {
			counts[groupKey(m.Path, d)]++
		}
		maxN := 0
		for _, n := range counts {
			if n > maxN {
				maxN = n
			}
		}
		if maxN <= maxGroupSize {
			return d
		}
	}
	return maxGroupDepth
}

func moduleGroupMap(groups []group) map[string]string {
	out := map[string]string{}
	for _, g := range groups {
		for _, m := range g.Modules {
			out[m.ID] = g.Key
		}
	}
	return out
}

func groupLabel(key string) string {
	if key == "" || key == "(root)" {
		return "(root)"
	}
	return key
}

func groupSlug(key string) string {
	if key == "" || key == "(root)" {
		return "root"
	}
	return strings.NewReplacer("/", "-", ".", "-", " ", "-").Replace(key)
}

// renderOverviewSVG draws groups as nodes with aggregated group→group edges.
func renderOverviewSVG(groups []group, edges []ir.Edge) []byte {
	gm := moduleGroupMap(groups)
	nodes := make([]svgNode, 0, len(groups))
	for _, g := range groups {
		body, h := renderGroupBox(g)
		nodes = append(nodes, svgNode{id: "group:" + g.Key, h: h, body: body})
	}
	seen := map[string]bool{}
	var ge []svgEdge
	for _, e := range edges {
		fg, tg := gm[e.From], gm[e.To]
		if fg == "" || tg == "" || fg == tg {
			continue
		}
		k := fg + "->" + tg
		if seen[k] {
			continue
		}
		seen[k] = true
		ge = append(ge, svgEdge{from: "group:" + fg, to: "group:" + tg})
	}
	return layoutSVG(nodes, ge)
}

func renderGroupBox(g group) (string, int) {
	const h = 46
	var b strings.Builder
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="8" fill="#eef2ff" stroke="#4338ca" stroke-width="2"/>`, svgCardW, h)
	fmt.Fprintf(&b, `<text x="%d" y="22" font-size="13" font-weight="700" fill="#312e81">%s</text>`, svgPad, xmlEsc(truncate(groupLabel(g.Key), svgWrapChars)))
	fmt.Fprintf(&b, `<text x="%d" y="39" font-size="10" fill="#4338ca">%d module(s)</text>`, svgPad, len(g.Modules))
	return b.String(), h
}

// renderGroupSVG draws one group's modules with full intra-group edges; edges to
// other groups terminate at a compact "ghost" node naming the target group.
func renderGroupSVG(g group, allEdges []ir.Edge, groupOf map[string]string, store *Summaries) []byte {
	inGroup := map[string]bool{}
	for _, m := range g.Modules {
		inGroup[m.ID] = true
	}
	nodes := make([]svgNode, 0, len(g.Modules))
	for _, m := range g.Modules {
		body, h := renderCard(m, store)
		nodes = append(nodes, svgNode{id: m.ID, h: h, body: body})
	}
	ghosts := map[string]bool{}
	seenEdge := map[string]bool{}
	var edges []svgEdge
	for _, e := range allEdges {
		switch {
		case inGroup[e.From] && inGroup[e.To]:
			edges = append(edges, svgEdge{from: e.From, to: e.To, label: e.Rel, dashed: e.Rel == ir.RelImplements})
		case inGroup[e.From] && !inGroup[e.To]:
			gid := "group:" + groupOf[e.To]
			if !ghosts[gid] {
				body, h := renderGhost(groupLabel(groupOf[e.To]))
				nodes = append(nodes, svgNode{id: gid, h: h, body: body})
				ghosts[gid] = true
			}
			ek := e.From + "|" + gid + "|" + e.Rel
			if !seenEdge[ek] {
				seenEdge[ek] = true
				edges = append(edges, svgEdge{from: e.From, to: gid, label: e.Rel, dashed: e.Rel == ir.RelImplements})
			}
		}
	}
	return layoutSVG(nodes, edges)
}

func renderGhost(label string) (string, int) {
	const h = 34
	var b strings.Builder
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="8" fill="#f1f5f9" stroke="#94a3b8" stroke-width="1.5" stroke-dasharray="4 3"/>`, svgCardW, h)
	fmt.Fprintf(&b, `<text x="%d" y="21" font-size="12" fill="#475569">→ %s</text>`, svgPad, xmlEsc(truncate(label, svgWrapChars)))
	return b.String(), h
}
