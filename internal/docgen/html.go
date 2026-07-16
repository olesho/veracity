package docgen

import (
	"bytes"
	"html/template"

	"github.com/olesho/harness/internal/docgen/ir"
)

type htmlData struct {
	Subproject  string
	SVG         template.HTML
	TooManyMods bool
	Modules     []htmlModule
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

// RenderHTML produces a self-contained, escaped HTML page for a project's IR
// with stored summaries. All content is auto-escaped by html/template; the SVG
// (whose own text nodes are pre-escaped) is injected as trusted markup.
func RenderHTML(doc ir.IR, store *Summaries) []byte {
	data := htmlData{Subproject: doc.Subproject}
	if len(doc.Modules) <= maxModules {
		data.SVG = template.HTML(RenderSVG(doc, store)) //nolint:gosec // SVG text nodes are pre-escaped by xmlEsc
	} else {
		data.TooManyMods = true
	}
	for _, m := range doc.Modules {
		hm := htmlModule{Name: m.Name, Path: m.Path, DocComment: m.DocComment, Summary: store.moduleSummary(m), Exports: m.Exports}
		for _, iface := range m.Interfaces {
			hm.Interfaces = append(hm.Interfaces, htmlIface{
				Name:        iface.Name,
				Description: store.interfaceDescription(m, iface),
				DocComment:  iface.DocComment,
				Methods:     iface.Methods,
			})
		}
		data.Modules = append(data.Modules, hm)
	}

	var buf bytes.Buffer
	if err := htmlTmpl.Execute(&buf, data); err != nil {
		return []byte("<!-- render error: " + template.HTMLEscapeString(err.Error()) + " -->")
	}
	return buf.Bytes()
}

var htmlTmpl = template.Must(template.New("modules").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Subproject}} — Modules</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: ui-sans-serif, system-ui, sans-serif; margin: 0; padding: 2rem; line-height: 1.5;
         background: #ffffff; color: #0f172a; }
  @media (prefers-color-scheme: dark) { body { background: #0b1120; color: #e2e8f0; } .card { background: #111827 !important; } code { background: #1f2937 !important; } }
  h1 { font-size: 1.6rem; }
  .diagram { overflow-x: auto; border: 1px solid #cbd5e1; border-radius: 8px; padding: 1rem; margin: 1rem 0 2rem; }
  .card { border: 1px solid #cbd5e1; border-radius: 8px; padding: 1rem 1.25rem; margin: 1rem 0; background: #f8fafc; }
  .path { font-family: ui-monospace, monospace; font-size: .85rem; color: #64748b; }
  .pending { color: #92400e; font-style: italic; }
  .iface { border: 1px dashed #d97706; border-radius: 6px; padding: .75rem 1rem; margin: .75rem 0; }
  .iface h3 { margin: 0 0 .35rem; font-size: 1rem; }
  code { font-family: ui-monospace, monospace; background: #eef2f7; padding: .1rem .3rem; border-radius: 4px; font-size: .85rem; }
  ul { margin: .35rem 0; padding-left: 1.2rem; }
</style>
</head>
<body>
<h1>{{.Subproject}} — Modules &amp; Boundaries</h1>
{{if .TooManyMods}}<p class="pending">Diagram omitted: more than the display limit of modules. See the module list below.</p>{{else}}<div class="diagram">{{.SVG}}</div>{{end}}
{{range .Modules}}
<section class="card">
  <h2>{{.Name}} <span class="path">{{.Path}}</span></h2>
  {{if .Summary}}<p>{{.Summary}}</p>{{else}}<p class="pending">(summary pending — run the harness-docs skill)</p>{{end}}
  {{if .DocComment}}<p><em>{{.DocComment}}</em></p>{{end}}
  {{if .Exports}}<h3>Exports</h3><ul>{{range .Exports}}<li><code>{{.Signature}}</code>{{if .DocComment}} — {{.DocComment}}{{end}}</li>{{end}}</ul>{{end}}
  {{range .Interfaces}}
  <div class="iface">
    <h3>interface {{.Name}}</h3>
    {{if .Description}}<p>{{.Description}}</p>{{else}}<p class="pending">(description pending)</p>{{end}}
    {{if .DocComment}}<p><em>{{.DocComment}}</em></p>{{end}}
    <ul>{{range .Methods}}<li><code>{{.Signature}}</code></li>{{end}}</ul>
  </div>
  {{end}}
</section>
{{end}}
</body>
</html>
`))
