// Package extract turns project source into the language-agnostic IR. The Go
// extractor is native (go/parser + go/doc + go/printer); Python and TypeScript
// are handled by embedded subprocess extractors (see subprocess.go).
package extract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/olesho/harness/internal/docgen/ir"
	"github.com/olesho/harness/internal/fileset"
)

// Go extracts the IR for a Go project. projectDir is the absolute project
// directory; modulePath is its go module path; subproject is the lock name.
func Go(projectDir, modulePath, subproject string) (ir.IR, error) {
	out := ir.IR{SchemaVersion: ir.SchemaVersion, Subproject: subproject, Language: "go"}

	dirs, err := goPackageDirs(projectDir)
	if err != nil {
		return out, err
	}

	// First pass: build modules keyed by import path.
	modules := map[string]*ir.Module{}
	imports := map[string][]string{} // importPath -> imported paths (intra-module)
	// implSets[moduleID][typeName] = set of that type's method names.
	implSets := map[string]map[string]map[string]bool{}
	// refSets[moduleID][importPath] = set of referenced symbol names (for
	// consumer detection).
	refSets := map[string]map[string]map[string]bool{}
	for _, dir := range dirs {
		r, err := extractGoPackage(projectDir, dir, modulePath)
		if err != nil {
			return out, err
		}
		if r == nil || r.mod == nil {
			continue
		}
		modules[r.mod.ID] = r.mod
		imports[r.mod.ID] = r.imports
		implSets[r.mod.ID] = r.implSet
		refSets[r.mod.ID] = r.refs
	}

	ids := make([]string, 0, len(modules))
	for id := range modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// Populate, per interface, the entities on each side of the boundary:
	// implementers (concrete types anywhere that satisfy it) and consumers
	// (modules that reference it without implementing it).
	for _, mid := range ids {
		m := modules[mid]
		for i := range m.Interfaces {
			iface := &m.Interfaces[i]
			want := interfaceMethodNames(*iface)
			if len(want) == 0 {
				continue
			}
			implModules := map[string]bool{}
			for _, gid := range ids {
				tnames := make([]string, 0, len(implSets[gid]))
				for tn := range implSets[gid] {
					tnames = append(tnames, tn)
				}
				sort.Strings(tnames)
				for _, tn := range tnames {
					if hasAllMethods(implSets[gid][tn], want) {
						iface.Implementers = append(iface.Implementers, ir.Implementer{Module: gid, Type: tn})
						implModules[gid] = true
					}
				}
			}
			for _, gid := range ids {
				if gid == mid || implModules[gid] {
					continue
				}
				if refSets[gid][mid] != nil && refSets[gid][mid][iface.Name] {
					iface.Consumers = append(iface.Consumers, gid)
				}
			}
		}
	}

	for _, id := range ids {
		out.Modules = append(out.Modules, *modules[id])
	}

	// Module-level edges (kept for the module dependency overview): "implements"
	// when a type in the source satisfies an interface in the target, else
	// "depends on".
	for _, id := range ids {
		for _, imp := range imports[id] {
			target, ok := modules[imp]
			if !ok {
				continue
			}
			rel := ir.RelDependsOn
			for _, iface := range target.Interfaces {
				if moduleImplements(implSets[id], iface) {
					rel = ir.RelImplements
					break
				}
			}
			out.Edges = append(out.Edges, ir.Edge{From: id, To: imp, Rel: rel})
		}
	}
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].From != out.Edges[j].From {
			return out.Edges[i].From < out.Edges[j].From
		}
		return out.Edges[i].To < out.Edges[j].To
	})
	return out, nil
}

// pkgResult bundles one package's extraction outputs.
type pkgResult struct {
	mod     *ir.Module
	imports []string
	implSet map[string]map[string]bool // typeName -> method-name set
	refs    map[string]map[string]bool // importPath -> referenced symbol names
}

// goPackageDirs returns directories under projectDir that contain non-test .go
// files, skipping ignored directories.
func goPackageDirs(projectDir string) ([]string, error) {
	seen := map[string]bool{}
	var dirs []string
	err := filepath.WalkDir(projectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != projectDir && (isIgnoredDir(name)) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			dir := filepath.Dir(path)
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs, err
}

func isIgnoredDir(name string) bool {
	// Reuse fileset's notion of ignorable directories.
	return fileset.Ignored(name + "/x")
}

func extractGoPackage(projectDir, dir, modulePath string) (*pkgResult, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	var fileNames []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		f, perr := parser.ParseFile(fset, full, nil, parser.ParseComments)
		if perr != nil {
			return nil, perr
		}
		files = append(files, f)
		fileNames = append(fileNames, full)
	}
	if len(files) == 0 {
		return nil, nil
	}
	name := files[0].Name.Name

	rel, _ := filepath.Rel(projectDir, dir)
	rel = filepath.ToSlash(rel)
	importPath := modulePath
	if rel != "." {
		importPath = modulePath + "/" + rel
	}

	// AllDecls so that unexported implementer types are visible for the
	// implements/depends-on classification.
	dpkg, err := doc.NewFromFiles(fset, files, importPath, doc.AllDecls)
	if err != nil {
		return nil, err
	}

	mod := &ir.Module{
		ID:         importPath,
		Name:       name,
		Path:       rel,
		DocComment: strings.TrimSpace(dpkg.Doc),
		Exports:    []ir.Export{},
		Interfaces: []ir.Interface{},
	}
	// Files (project-root-relative, sorted).
	for _, fn := range fileNames {
		frel, _ := filepath.Rel(projectDir, fn)
		mod.Files = append(mod.Files, filepath.ToSlash(frel))
	}
	sort.Strings(mod.Files)

	// Module content hash: sorted (relPath, bytes) of its files.
	mh := sha256.New()
	for _, rf := range mod.Files {
		b, rerr := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(rf)))
		if rerr != nil {
			return nil, rerr
		}
		fmt.Fprintf(mh, "%s\x00", rf)
		mh.Write(b)
		mh.Write([]byte{0})
	}
	mod.ContentHash = hex.EncodeToString(mh.Sum(nil))

	// Top-level functions.
	for _, fn := range dpkg.Funcs {
		if !ast.IsExported(fn.Name) {
			continue
		}
		mod.Exports = append(mod.Exports, ir.Export{
			Kind:       "func",
			Name:       fn.Name,
			Signature:  funcSignature(fset, fn.Decl),
			DocComment: strings.TrimSpace(fn.Doc),
			Line:       fset.Position(fn.Decl.Pos()).Line,
		})
	}

	// Types: interfaces become boundaries; others become type exports. Their
	// constructor funcs are recorded as exports too. Every type's method-name
	// set is collected (exported or not) for the implements/depends-on check.
	implSet := map[string]map[string]bool{}
	for _, ty := range dpkg.Types {
		if methods := methodNameSet(ty); len(methods) > 0 {
			implSet[ty.Name] = methods
		}
		if !ast.IsExported(ty.Name) {
			continue
		}
		if iface := interfaceType(ty); iface != nil {
			ifc := ir.Interface{
				Name:       ty.Name,
				DocComment: strings.TrimSpace(ty.Doc),
				Methods:    interfaceMethods(fset, iface),
			}
			ifc.ContentHash = interfaceHash(ifc)
			mod.Interfaces = append(mod.Interfaces, ifc)
		} else {
			mod.Exports = append(mod.Exports, ir.Export{
				Kind:       "type",
				Name:       ty.Name,
				Signature:  "type " + ty.Name,
				DocComment: strings.TrimSpace(ty.Doc),
			})
		}
		for _, fn := range ty.Funcs {
			if !ast.IsExported(fn.Name) {
				continue
			}
			mod.Exports = append(mod.Exports, ir.Export{
				Kind:       "func",
				Name:       fn.Name,
				Signature:  funcSignature(fset, fn.Decl),
				DocComment: strings.TrimSpace(fn.Doc),
				Line:       fset.Position(fn.Decl.Pos()).Line,
			})
		}
	}
	sort.SliceStable(mod.Interfaces, func(i, j int) bool { return mod.Interfaces[i].Name < mod.Interfaces[j].Name })

	// Intra-module imports for the edge pass.
	var imps []string
	for _, f := range files {
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, modulePath) {
				imps = append(imps, p)
			}
		}
	}
	sort.Strings(imps)
	imps = dedup(imps)
	return &pkgResult{mod: mod, imports: imps, implSet: implSet, refs: collectReferences(files, modulePath)}, nil
}

// collectReferences maps each imported project package to the set of its symbols
// this package references (via qualified selectors like `ticket.Store`), used to
// find the consumers of an interface.
func collectReferences(files []*ast.File, modulePath string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, f := range files {
		local := map[string]string{} // local package name -> import path
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(p, modulePath) {
				continue
			}
			nm := p[strings.LastIndex(p, "/")+1:]
			if imp.Name != nil {
				nm = imp.Name.Name
			}
			if nm == "_" || nm == "." {
				continue
			}
			local[nm] = p
		}
		if len(local) == 0 {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if p, ok := local[id.Name]; ok {
				if out[p] == nil {
					out[p] = map[string]bool{}
				}
				out[p][sel.Sel.Name] = true
			}
			return true
		})
	}
	return out
}

// hasAllMethods reports whether have contains every name in want.
func hasAllMethods(have map[string]bool, want []string) bool {
	if len(have) < len(want) {
		return false
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

func interfaceType(ty *doc.Type) *ast.InterfaceType {
	if ty.Decl == nil {
		return nil
	}
	for _, spec := range ty.Decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok || ts.Name.Name != ty.Name {
			continue
		}
		if it, ok := ts.Type.(*ast.InterfaceType); ok {
			return it
		}
	}
	return nil
}

func interfaceMethods(fset *token.FileSet, it *ast.InterfaceType) []ir.Method {
	var out []ir.Method
	if it.Methods == nil {
		return out
	}
	for _, field := range it.Methods.List {
		ft, ok := field.Type.(*ast.FuncType)
		if !ok || len(field.Names) == 0 {
			continue // embedded interface; skip for now
		}
		name := field.Names[0].Name
		if !ast.IsExported(name) {
			continue
		}
		out = append(out, ir.Method{
			Signature:  name + printNode(fset, ft)[len("func"):],
			DocComment: strings.TrimSpace(field.Doc.Text()),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Signature < out[j].Signature })
	return out
}

// funcSignature renders a function declaration without its body.
func funcSignature(fset *token.FileSet, decl *ast.FuncDecl) string {
	if decl == nil {
		return ""
	}
	clone := *decl
	clone.Body = nil
	clone.Doc = nil
	return strings.TrimSuffix(strings.TrimSpace(printNode(fset, &clone)), "{}")
}

func printNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
	_ = cfg.Fprint(&buf, fset, node)
	return strings.TrimSpace(buf.String())
}

// methodNameSet returns the set of a type's method names (used to decide
// whether the type structurally satisfies an interface).
func methodNameSet(ty *doc.Type) map[string]bool {
	if len(ty.Methods) == 0 {
		return nil
	}
	out := make(map[string]bool, len(ty.Methods))
	for _, m := range ty.Methods {
		out[m.Name] = true
	}
	return out
}

// moduleImplements reports whether some type in a module (its type→method-set
// map) satisfies iface, i.e. has every method name the interface declares. This
// is a structural, name-level heuristic (no full type checking), which is enough
// to distinguish an implementer from a mere consumer.
func moduleImplements(typeMethods map[string]map[string]bool, iface ir.Interface) bool {
	want := interfaceMethodNames(iface)
	if len(want) == 0 {
		return false // empty interface would match everything
	}
	for _, have := range typeMethods {
		if len(have) < len(want) {
			continue
		}
		all := true
		for _, w := range want {
			if !have[w] {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// interfaceMethodNames extracts the leading identifier of each method signature
// (everything before the first "(").
func interfaceMethodNames(iface ir.Interface) []string {
	var names []string
	for _, m := range iface.Methods {
		if i := strings.Index(m.Signature, "("); i > 0 {
			if n := strings.TrimSpace(m.Signature[:i]); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

// interfaceHash digests an interface's own declaration so its description is
// reused until the interface itself changes.
func interfaceHash(ifc ir.Interface) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", ifc.Name, ifc.DocComment)
	for _, m := range ifc.Methods {
		fmt.Fprintf(h, "%s\x00%s\x00", m.Signature, m.DocComment)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func dedup(s []string) []string {
	if len(s) == 0 {
		return s
	}
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
