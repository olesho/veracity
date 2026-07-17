package docgen

import (
	"bytes"
	"fmt"
	"html/template"

	"github.com/olesho/harness/internal/docgen/ir"
)

type htmlData struct {
	Subproject  string
	Interfaces  []htmlInterface
	ModuleGraph template.HTML // module dependency overview (secondary)
	Modules     []htmlModule  // flat reference
}

type htmlInterface struct {
	Name         string
	Module       string
	SVG          template.HTML
	Description  string
	Methods      []ir.Method
	Consumers    []string
	Implementers []string
}

type htmlModule struct {
	Name       string
	Path       string
	Summary    string
	DocComment string
	Exports    []ir.Export
	Interfaces []htmlIface
}

type htmlIface struct {
	Name        string
	Description string
	DocComment  string
	Methods     []ir.Method
}

// RenderHTML produces a self-contained, escaped HTML page: a gallery of
// interface-centric diagrams (consumers ← interface → implementers) followed by
// a module dependency overview and a flat module reference. All content is
// auto-escaped by html/template; the SVGs (text nodes pre-escaped by xmlEsc) are
// injected as trusted markup.
func RenderHTML(doc ir.IR, store *Summaries) []byte {
	if store == nil {
		store = newSummaries()
	}
	data := htmlData{Subproject: doc.Subproject}

	for _, d := range BuildInterfaceDiagrams(doc, store) {
		hi := htmlInterface{
			Name:        d.Iface.Name,
			Module:      d.Module.Path,
			SVG:         template.HTML(d.SVG), //nolint:gosec // SVG text nodes pre-escaped
			Description: store.interfaceDescription(d.Module, d.Iface),
			Methods:     d.Iface.Methods,
		}
		for _, c := range d.Iface.Consumers {
			hi.Consumers = append(hi.Consumers, shortName(c))
		}
		for _, im := range d.Iface.Implementers {
			hi.Implementers = append(hi.Implementers, fmt.Sprintf("%s (%s)", im.Type, shortName(im.Module)))
		}
		data.Interfaces = append(data.Interfaces, hi)
	}

	// Module dependency overview (secondary context).
	ds := BuildDiagrams(doc, store)
	if ds.Chunked {
		data.ModuleGraph = template.HTML(ds.Overview) //nolint:gosec // pre-escaped
	} else {
		data.ModuleGraph = template.HTML(ds.Whole) //nolint:gosec // pre-escaped
	}

	for _, m := range doc.Modules {
		data.Modules = append(data.Modules, htmlModuleFrom(m, store))
	}

	var buf bytes.Buffer
	if err := htmlTmpl.Execute(&buf, data); err != nil {
		return []byte("<!-- render error: " + template.HTMLEscapeString(err.Error()) + " -->")
	}
	return buf.Bytes()
}

func htmlModuleFrom(m ir.Module, store *Summaries) htmlModule {
	hm := htmlModule{Name: m.Name, Path: m.Path, DocComment: m.DocComment, Summary: store.moduleSummary(m), Exports: m.Exports}
	for _, iface := range m.Interfaces {
		hm.Interfaces = append(hm.Interfaces, htmlIface{
			Name:        iface.Name,
			Description: store.interfaceDescription(m, iface),
			DocComment:  iface.DocComment,
			Methods:     iface.Methods,
		})
	}
	return hm
}

var htmlTmpl = template.Must(template.New("modules").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Subproject}} — Interfaces &amp; Modules</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: ui-sans-serif, system-ui, sans-serif; margin: 0; padding: 2rem; line-height: 1.5;
         background: #ffffff; color: #0f172a; }
  @media (prefers-color-scheme: dark) { body { background: #0b1120; color: #e2e8f0; } .card,.iface-card { background: #111827 !important; } code { background: #1f2937 !important; } }
  h1 { font-size: 1.6rem; }
  h2.section { margin-top: 2.4rem; border-bottom: 1px solid #cbd5e1; padding-bottom: .3rem; }
  .diagram { overflow-x: auto; border: 1px solid #cbd5e1; border-radius: 8px; padding: 1rem; margin: .75rem 0 1.5rem; }
  .iface-card { border: 1px solid #d97706; border-radius: 8px; padding: 1rem 1.25rem; margin: 1.25rem 0; background: #fffbeb; }
  .card { border: 1px solid #cbd5e1; border-radius: 8px; padding: 1rem 1.25rem; margin: 1rem 0; background: #f8fafc; }
  .path { font-family: ui-monospace, monospace; font-size: .85rem; color: #64748b; }
  .pending { color: #92400e; font-style: italic; }
  .rel { font-size: .9rem; margin: .25rem 0; }
  .rel b { color: #475569; }
  code { font-family: ui-monospace, monospace; background: #eef2f7; padding: .1rem .3rem; border-radius: 4px; font-size: .85rem; }
  ul { margin: .35rem 0; padding-left: 1.2rem; }
  .legend { font-size: .85rem; color: #475569; }
</style>
</head>
<body>
<h1>{{.Subproject}} — Interfaces &amp; Modules</h1>
<p class="legend">Each interface is shown in the center, with the modules that <b>depend on</b> it (left) and the concrete types that <b>implement</b> it (right).</p>

<h2 class="section">Interfaces &amp; Boundaries</h2>
{{if .Interfaces}}
{{range .Interfaces}}
<section class="iface-card">
  <h3>interface {{.Name}} <span class="path">in {{.Module}}</span></h3>
  {{if .Description}}<p>{{.Description}}</p>{{else}}<p class="pending">(description pending — run the harness-docs skill)</p>{{end}}
  <div class="diagram">{{.SVG}}</div>
  <p class="rel"><b>Depends on it:</b> {{if .Consumers}}{{range $i, $c := .Consumers}}{{if $i}}, {{end}}<code>{{$c}}</code>{{end}}{{else}}none{{end}}</p>
  <p class="rel"><b>Implemented by:</b> {{if .Implementers}}{{range $i, $m := .Implementers}}{{if $i}}, {{end}}<code>{{$m}}</code>{{end}}{{else}}none{{end}}</p>
  <details><summary>methods</summary><ul>{{range .Methods}}<li><code>{{.Signature}}</code></li>{{end}}</ul></details>
</section>
{{end}}
{{else}}
<p class="pending">No interfaces found in this project.</p>
{{end}}

<h2 class="section">Module dependencies</h2>
<div class="diagram">{{.ModuleGraph}}</div>

<h2 class="section">Modules</h2>
{{range .Modules}}
<section class="card">
  <h3>{{.Name}} <span class="path">{{.Path}}</span></h3>
  {{if .Summary}}<p>{{.Summary}}</p>{{else}}<p class="pending">(summary pending)</p>{{end}}
  {{if .DocComment}}<p><em>{{.DocComment}}</em></p>{{end}}
  {{if .Exports}}<details><summary>exports</summary><ul>{{range .Exports}}<li><code>{{.Signature}}</code></li>{{end}}</ul></details>{{end}}
</section>
{{end}}
</body>
</html>
`))
