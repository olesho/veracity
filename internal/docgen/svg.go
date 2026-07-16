package docgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/olesho/harness/internal/docgen/ir"
)

// SVG layout constants (deterministic; text is wrapped by character count so no
// browser measurement is needed and output is byte-stable).
const (
	svgCardW      = 224
	svgPad        = 10
	svgLineH      = 15
	svgHeaderH    = 34
	svgIfacePad   = 7
	svgHGap       = 96 // room for the "implements" / "depends on" edge label
	svgVGap       = 30
	svgMargin     = 20
	svgSummaryMax = 3 // wrapped lines
	svgDescMax    = 2
	svgWrapChars  = 34
	svgMethodMax  = 32 // truncate long method signatures
)

// svgNode is a positioned box with a pre-rendered body fragment (local origin).
type svgNode struct {
	id   string
	h    int
	body string
}

// svgEdge is a labeled connector between two node ids.
type svgEdge struct {
	from, to string
	label    string
	dashed   bool
}

// RenderSVG produces a deterministic architecture diagram for a whole project's
// IR, using stored summaries where fresh. Modules are cards; each card contains
// its interfaces as boundary blocks; edges are labeled implements/depends on.
func RenderSVG(doc ir.IR, store *Summaries) []byte {
	mods := append([]ir.Module(nil), doc.Modules...)
	sort.SliceStable(mods, func(i, j int) bool { return mods[i].ID < mods[j].ID })
	nodes := make([]svgNode, 0, len(mods))
	for _, m := range mods {
		body, h := renderCard(m, store)
		nodes = append(nodes, svgNode{id: m.ID, h: h, body: body})
	}
	return layoutSVG(nodes, edgesToSVG(doc.Edges))
}

func edgesToSVG(edges []ir.Edge) []svgEdge {
	out := make([]svgEdge, 0, len(edges))
	for _, e := range edges {
		out = append(out, svgEdge{from: e.From, to: e.To, label: e.Rel, dashed: e.Rel == ir.RelImplements})
	}
	return out
}

// layoutSVG lays out nodes in dependency layers (left→right), stacks them within
// a layer (sorted), and draws the edges. All columns share svgCardW width.
func layoutSVG(nodes []svgNode, edges []svgEdge) []byte {
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].id < nodes[j].id })
	present := map[string]bool{}
	for _, n := range nodes {
		present[n.id] = true
	}
	// keep only edges whose endpoints are both present
	var e2 []svgEdge
	for _, e := range edges {
		if present[e.from] && present[e.to] {
			e2 = append(e2, e)
		}
	}
	edges = e2

	layer := map[string]int{}
	for _, n := range nodes {
		layer[n.id] = 0
	}
	for range nodes {
		for _, e := range edges {
			if layer[e.to] < layer[e.from]+1 {
				layer[e.to] = layer[e.from] + 1
			}
		}
	}

	byLayerY := map[int]int{}
	boxes := map[string]*box{}
	var order []string
	maxLayer := 0
	for _, n := range nodes {
		l := layer[n.id]
		if l > maxLayer {
			maxLayer = l
		}
		x := svgMargin + l*(svgCardW+svgHGap)
		y := svgMargin + byLayerY[l]
		byLayerY[l] += n.h + svgVGap
		boxes[n.id] = &box{id: n.id, x: x, y: y, h: n.h, body: n.body}
		order = append(order, n.id)
	}

	width := svgMargin*2 + (maxLayer+1)*svgCardW + maxLayer*svgHGap
	height := svgMargin
	for _, y := range byLayerY {
		if y+svgMargin > height {
			height = y + svgMargin
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="ui-sans-serif, system-ui, sans-serif">`, width, height, width, height)
	b.WriteString(`<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="#64748b"/></marker></defs>`)
	b.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)

	for _, e := range edges {
		from, to := boxes[e.from], boxes[e.to]
		sx := from.x + svgCardW
		sy := from.y + from.h/2
		tx := to.x
		ty := to.y + to.h/2
		dash := ""
		if e.dashed || (to.y < from.y && layer[e.to] <= layer[e.from]) {
			dash = ` stroke-dasharray="6 3"`
		}
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#64748b" stroke-width="1.5" marker-end="url(#arrow)"%s/>`, sx, sy, tx, ty, dash)
		if e.label != "" {
			mx, my := (sx+tx)/2, (sy+ty)/2
			w := len(e.label)*6 + 8
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="15" rx="3" fill="#ffffff" opacity="0.9"/>`, mx-w/2, my-14, w)
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="10" fill="#475569" text-anchor="middle">%s</text>`, mx, my-3, xmlEsc(e.label))
		}
	}

	for _, id := range order {
		bx := boxes[id]
		fmt.Fprintf(&b, `<g transform="translate(%d,%d)">%s</g>`, bx.x, bx.y, bx.body)
	}
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

type box struct {
	id   string
	x, y int
	h    int
	body string
}

// renderCard draws one module card at local origin and returns its SVG fragment
// and computed height.
func renderCard(m ir.Module, store *Summaries) (string, int) {
	var b strings.Builder
	y := svgPad + svgHeaderH

	summary := store.moduleSummary(m)
	summaryLines := wrapText(summary, svgWrapChars, svgSummaryMax)
	if summary == "" {
		summaryLines = []string{"(summary pending — run harness-docs)"}
	}
	y += len(summaryLines) * svgLineH
	y += svgPad

	type ib struct {
		iface ir.Interface
		desc  []string
		h     int
	}
	var blocks []ib
	for _, iface := range m.Interfaces {
		desc := store.interfaceDescription(m, iface)
		descLines := wrapText(desc, svgWrapChars, svgDescMax)
		if desc == "" {
			descLines = nil
		}
		h := svgIfacePad*2 + svgLineH + len(iface.Methods)*svgLineH + len(descLines)*svgLineH
		blocks = append(blocks, ib{iface: iface, desc: descLines, h: h})
		y += h + svgIfacePad
	}
	cardH := y + svgPad

	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="8" fill="#f8fafc" stroke="#334155" stroke-width="2"/>`, svgCardW, cardH)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="14" font-weight="700" fill="#0f172a">%s</text>`, svgPad, svgPad+15, xmlEsc(m.Name))
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="10" font-family="ui-monospace, monospace" fill="#64748b">%s</text>`, svgPad, svgPad+30, xmlEsc(truncate(m.Path, svgWrapChars)))

	ty := svgPad + svgHeaderH + 11
	for _, line := range summaryLines {
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="#334155">%s</text>`, svgPad, ty, xmlEsc(line))
		ty += svgLineH
	}
	ty += svgPad

	for _, blk := range blocks {
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="5" fill="#fef3c7" stroke="#d97706" stroke-width="1.5" stroke-dasharray="5 3"/>`, svgPad, ty, svgCardW-2*svgPad, blk.h)
		iy := ty + svgIfacePad + 11
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" font-weight="700" fill="#92400e">interface %s</text>`, svgPad+svgIfacePad, iy, xmlEsc(blk.iface.Name))
		iy += svgLineH
		for _, meth := range blk.iface.Methods {
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="10" font-family="ui-monospace, monospace" fill="#78350f">%s</text>`, svgPad+svgIfacePad, iy, xmlEsc(truncate(meth.Signature, svgMethodMax)))
			iy += svgLineH
		}
		for _, line := range blk.desc {
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="10" fill="#92400e">%s</text>`, svgPad+svgIfacePad, iy, xmlEsc(line))
			iy += svgLineH
		}
		ty += blk.h + svgIfacePad
	}
	return b.String(), cardH
}

// wrapText greedily wraps s into lines of at most maxChars, capping at maxLines
// (the last line gets an ellipsis if content remains).
func wrapText(s string, maxChars, maxLines int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	words := strings.Fields(s)
	var lines []string
	var cur string
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		if len(cur)+1+len(w) <= maxChars {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
			if len(lines) == maxLines {
				break
			}
		}
	}
	if len(lines) < maxLines && cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == maxLines {
		joined := strings.Join(lines, " ")
		if len(strings.Fields(joined)) < len(words) {
			lines[maxLines-1] = truncate(lines[maxLines-1], maxChars-1)
		}
	}
	return lines
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
