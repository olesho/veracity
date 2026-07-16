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
	svgCardW      = 320
	svgPad        = 12
	svgLineH      = 16
	svgHeaderH    = 38
	svgIfacePad   = 8
	svgHGap       = 90
	svgVGap       = 34
	svgMargin     = 24
	svgSummaryMax = 3 // wrapped lines
	svgDescMax    = 2
	svgWrapChars  = 46
	svgMethodMax  = 44 // truncate long method signatures
	maxModules    = 50 // above this, fall back to a table (see RenderHTML)
)

type box struct {
	id   string
	x, y int
	h    int
	body string
}

// RenderSVG produces a deterministic architecture diagram for a project's IR,
// using stored summaries where fresh. Modules are cards; each card contains its
// interfaces as boundary blocks; edges are labeled with the mediating interface.
func RenderSVG(doc ir.IR, store *Summaries) []byte {
	mods := append([]ir.Module(nil), doc.Modules...)
	sort.SliceStable(mods, func(i, j int) bool { return mods[i].ID < mods[j].ID })

	// Layer assignment (longest path; bounded to tolerate cycles).
	layer := map[string]int{}
	for _, m := range mods {
		layer[m.ID] = 0
	}
	for range mods {
		for _, e := range doc.Edges {
			if layer[e.To] < layer[e.From]+1 {
				layer[e.To] = layer[e.From] + 1
			}
		}
	}

	// Build cards and position them by layer (x) and stack order (y).
	byLayerY := map[int]int{}
	boxes := map[string]*box{}
	var order []string
	maxLayer := 0
	for _, m := range mods {
		body, h := renderCard(m, store)
		l := layer[m.ID]
		if l > maxLayer {
			maxLayer = l
		}
		x := svgMargin + l*(svgCardW+svgHGap)
		y := svgMargin + byLayerY[l]
		byLayerY[l] += h + svgVGap
		boxes[m.ID] = &box{id: m.ID, x: x, y: y, h: h, body: body}
		order = append(order, m.ID)
	}

	// Canvas size.
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

	// Edges first (under the cards).
	for _, e := range doc.Edges {
		from, ok1 := boxes[e.From]
		to, ok2 := boxes[e.To]
		if !ok1 || !ok2 {
			continue
		}
		sx := from.x + svgCardW
		sy := from.y + from.h/2
		tx := to.x
		ty := to.y + to.h/2
		dashed := ""
		if to.y < from.y && layer[e.To] <= layer[e.From] { // back/cycle edge
			dashed = ` stroke-dasharray="4 3"`
		}
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#64748b" stroke-width="1.5" marker-end="url(#arrow)"%s/>`, sx, sy, tx, ty, dashed)
		if e.Via != "" {
			mx, my := (sx+tx)/2, (sy+ty)/2
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="#475569" text-anchor="middle">%s</text>`, mx, my-4, xmlEsc(e.Via))
		}
	}

	// Cards.
	for _, id := range order {
		bx := boxes[id]
		fmt.Fprintf(&b, `<g transform="translate(%d,%d)">%s</g>`, bx.x, bx.y, bx.body)
	}

	b.WriteString(`</svg>`)
	return []byte(b.String())
}

// renderCard draws one module card at local origin and returns its SVG fragment
// and computed height.
func renderCard(m ir.Module, store *Summaries) (string, int) {
	var b strings.Builder
	y := svgPad + svgHeaderH

	// Summary (stored if fresh, else placeholder).
	summary := store.moduleSummary(m)
	summaryLines := wrapText(summary, svgWrapChars, svgSummaryMax)
	if summary == "" {
		summaryLines = []string{"(summary pending — run the harness-docs skill)"}
	}
	y += len(summaryLines) * svgLineH
	y += svgPad

	// Interface blocks.
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
		h := svgIfacePad*2 + svgLineH /*name*/ + len(iface.Methods)*svgLineH + len(descLines)*svgLineH
		blocks = append(blocks, ib{iface: iface, desc: descLines, h: h})
		y += h + svgIfacePad
	}
	cardH := y + svgPad

	// Card rect.
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="8" fill="#f8fafc" stroke="#334155" stroke-width="2"/>`, svgCardW, cardH)
	// Header.
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="15" font-weight="700" fill="#0f172a">%s</text>`, svgPad, svgPad+16, xmlEsc(m.Name))
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" font-family="ui-monospace, monospace" fill="#64748b">%s</text>`, svgPad, svgPad+32, xmlEsc(m.Path))

	// Summary lines.
	ty := svgPad + svgHeaderH + 12
	for _, line := range summaryLines {
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="12" fill="#334155">%s</text>`, svgPad, ty, xmlEsc(line))
		ty += svgLineH
	}
	ty += svgPad

	// Interface blocks.
	for _, blk := range blocks {
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="5" fill="#fef3c7" stroke="#d97706" stroke-width="1.5" stroke-dasharray="5 3"/>`, svgPad, ty, svgCardW-2*svgPad, blk.h)
		iy := ty + svgIfacePad + 12
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="12" font-weight="700" fill="#92400e">interface %s</text>`, svgPad+svgIfacePad, iy, xmlEsc(blk.iface.Name))
		iy += svgLineH
		for _, meth := range blk.iface.Methods {
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" font-family="ui-monospace, monospace" fill="#78350f">%s</text>`, svgPad+svgIfacePad, iy, xmlEsc(truncate(meth.Signature, svgMethodMax)))
			iy += svgLineH
		}
		for _, line := range blk.desc {
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="#92400e">%s</text>`, svgPad+svgIfacePad, iy, xmlEsc(line))
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
		// Signal truncation if we didn't consume everything.
		joined := strings.Join(lines, " ")
		if len(strings.Fields(joined)) < len(words) {
			last := lines[maxLines-1]
			lines[maxLines-1] = truncate(last, maxChars-1)
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
