package docgen

import (
	"fmt"
	"strings"

	"github.com/olesho/harness/internal/docgen/ir"
)

// svgEntityW is the width of consumer/implementer boxes — narrower than the
// central interface card, which needs room for method signatures.
const svgEntityW = 168

// RenderInterfaceSVG draws one interface-centric diagram: the interface (with
// its defining module and methods) in the center, the modules that depend on it
// to the left, and the concrete types that implement it to the right.
func RenderInterfaceSVG(mod ir.Module, iface ir.Interface, store *Summaries) []byte {
	if store == nil {
		store = newSummaries()
	}
	centerBody, centerH := renderInterfaceCard(mod, iface, store.interfaceDescription(mod, iface))

	type ent struct {
		body string
		h    int
	}
	var cons, impls []ent
	for _, c := range iface.Consumers {
		b, h := renderEntityBox(shortName(c), c)
		cons = append(cons, ent{b, h})
	}
	for _, im := range iface.Implementers {
		b, h := renderEntityBox(im.Type, shortName(im.Module))
		impls = append(impls, ent{b, h})
	}
	leftExists, rightExists := len(cons) > 0, len(impls) > 0

	colH := func(n []ent) int {
		if len(n) == 0 {
			return 0
		}
		t := (len(n) - 1) * svgVGap
		for _, e := range n {
			t += e.h
		}
		return t
	}
	leftH, rightH := colH(cons), colH(impls)
	totalH := centerH
	if leftH > totalH {
		totalH = leftH
	}
	if rightH > totalH {
		totalH = rightH
	}

	leftX := svgMargin
	centerX := svgMargin
	if leftExists {
		centerX = svgMargin + svgEntityW + svgHGap
	}
	rightX := centerX + svgCardW + svgHGap
	canvasW := centerX + svgCardW + svgMargin
	if rightExists {
		canvasW = rightX + svgEntityW + svgMargin
	}
	canvasH := totalH + 2*svgMargin

	cy0 := svgMargin + (totalH-centerH)/2
	cardMidY := cy0 + centerH/2

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="ui-sans-serif, system-ui, sans-serif">`, canvasW, canvasH, canvasW, canvasH)
	b.WriteString(`<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="#64748b"/></marker></defs>`)
	b.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)

	// Consumers on the left, arrows pointing into the interface.
	if leftExists {
		y := svgMargin + (totalH-leftH)/2
		for _, e := range cons {
			fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#64748b" stroke-width="1.5" marker-end="url(#arrow)"/>`, leftX+svgEntityW, y+e.h/2, centerX, cardMidY)
			fmt.Fprintf(&b, `<g transform="translate(%d,%d)">%s</g>`, leftX, y, e.body)
			y += e.h + svgVGap
		}
		labelChip(&b, (leftX+svgEntityW+centerX)/2, cardMidY-8, "depends on")
	}

	// Implementers on the right, arrows pointing into the interface.
	if rightExists {
		y := svgMargin + (totalH-rightH)/2
		for _, e := range impls {
			fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#64748b" stroke-width="1.5" stroke-dasharray="6 3" marker-end="url(#arrow)"/>`, rightX, y+e.h/2, centerX+svgCardW, cardMidY)
			fmt.Fprintf(&b, `<g transform="translate(%d,%d)">%s</g>`, rightX, y, e.body)
			y += e.h + svgVGap
		}
		labelChip(&b, (centerX+svgCardW+rightX)/2, cardMidY-8, "implements")
	}

	fmt.Fprintf(&b, `<g transform="translate(%d,%d)">%s</g>`, centerX, cy0, centerBody)
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

func labelChip(b *strings.Builder, cx, cy int, text string) {
	w := len(text)*6 + 8
	fmt.Fprintf(b, `<rect x="%d" y="%d" width="%d" height="15" rx="3" fill="#ffffff" opacity="0.92"/>`, cx-w/2, cy-2, w)
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="10" fill="#475569" text-anchor="middle">%s</text>`, cx, cy+9, xmlEsc(text))
}

// renderInterfaceCard draws the central interface box (dashed amber, matching
// the boundary styling) with its module, description, and method signatures.
func renderInterfaceCard(mod ir.Module, iface ir.Interface, desc string) (string, int) {
	descLines := wrapText(desc, svgWrapChars, svgDescMax)
	y := svgPad + 20 + 16 // title + subtitle
	if len(descLines) > 0 {
		y += len(descLines)*svgLineH + 4
	}
	methY := y
	y += len(iface.Methods) * svgLineH
	h := y + svgPad

	var b strings.Builder
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="8" fill="#fef3c7" stroke="#d97706" stroke-width="2" stroke-dasharray="6 3"/>`, svgCardW, h)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="13" font-weight="700" fill="#92400e">interface %s</text>`, svgPad, svgPad+14, xmlEsc(truncate(iface.Name, svgWrapChars)))
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="10" font-family="ui-monospace, monospace" fill="#a16207">in %s</text>`, svgPad, svgPad+30, xmlEsc(truncate(mod.Path, svgWrapChars-3)))
	ty := svgPad + 40
	for _, line := range descLines {
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="10" fill="#92400e">%s</text>`, svgPad, ty, xmlEsc(line))
		ty += svgLineH
	}
	ty = methY + 11
	for _, m := range iface.Methods {
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="10" font-family="ui-monospace, monospace" fill="#78350f">%s</text>`, svgPad, ty, xmlEsc(truncate(m.Signature, svgMethodMax)))
		ty += svgLineH
	}
	return b.String(), h
}

// renderEntityBox draws a small module/type box (consumer or implementer).
func renderEntityBox(title, subtitle string) (string, int) {
	const h = 42
	const chars = 24 // narrower box → shorter truncation
	var b strings.Builder
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="6" fill="#f8fafc" stroke="#334155" stroke-width="1.5"/>`, svgEntityW, h)
	fmt.Fprintf(&b, `<text x="%d" y="18" font-size="12" font-weight="700" fill="#0f172a">%s</text>`, svgPad, xmlEsc(truncate(title, chars)))
	fmt.Fprintf(&b, `<text x="%d" y="33" font-size="10" font-family="ui-monospace, monospace" fill="#64748b">%s</text>`, svgPad, xmlEsc(truncate(subtitle, chars)))
	return b.String(), h
}

func shortName(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// InterfaceDiagram is one interface's rendered interface-centric diagram.
type InterfaceDiagram struct {
	Module ir.Module
	Iface  ir.Interface
	Slug   string
	SVG    []byte
}

// BuildInterfaceDiagrams renders one interface-centric SVG per interface in the
// project (consumers ← interface → implementers), in a deterministic order.
func BuildInterfaceDiagrams(doc ir.IR, store *Summaries) []InterfaceDiagram {
	if store == nil {
		store = newSummaries()
	}
	var out []InterfaceDiagram
	for _, m := range doc.Modules {
		for _, iface := range m.Interfaces {
			out = append(out, InterfaceDiagram{
				Module: m,
				Iface:  iface,
				Slug:   ifaceSlug(m, iface),
				SVG:    RenderInterfaceSVG(m, iface, store),
			})
		}
	}
	return out
}

func ifaceSlug(m ir.Module, iface ir.Interface) string {
	base := m.Path
	if base == "." || base == "" {
		base = "root"
	}
	return groupSlug(base) + "-" + iface.Name
}
