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
	for _, dir := range dirs {
		mod, imps, err := extractGoPackage(projectDir, dir, modulePath)
		if err != nil {
			return out, err
		}
		if mod == nil {
			continue
		}
		modules[mod.ID] = mod
		imports[mod.ID] = imps
	}

	// Assemble modules in stable order.
	ids := make([]string, 0, len(modules))
	for id := range modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out.Modules = append(out.Modules, *modules[id])
	}

	// Second pass: intra-project edges (only to modules we extracted).
	for _, id := range ids {
		for _, imp := range imports[id] {
			target, ok := modules[imp]
			if !ok {
				continue // outside the project
			}
			via := mediatingInterface(modules[id], target)
			out.Edges = append(out.Edges, ir.Edge{From: id, To: imp, Via: via})
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

func extractGoPackage(projectDir, dir, modulePath string) (*ir.Module, []string, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
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
			return nil, nil, perr
		}
		files = append(files, f)
		fileNames = append(fileNames, full)
	}
	if len(files) == 0 {
		return nil, nil, nil
	}
	name := files[0].Name.Name

	rel, _ := filepath.Rel(projectDir, dir)
	rel = filepath.ToSlash(rel)
	importPath := modulePath
	if rel != "." {
		importPath = modulePath + "/" + rel
	}

	dpkg, err := doc.NewFromFiles(fset, files, importPath)
	if err != nil {
		return nil, nil, err
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
			return nil, nil, rerr
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
	// constructor funcs are recorded as exports too.
	for _, ty := range dpkg.Types {
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
	return mod, imps, nil
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

// mediatingInterface returns the name of an interface declared in `to` that is
// referenced by any exported signature in `from`, or "" (best-effort heuristic).
func mediatingInterface(from, to *ir.Module) string {
	for _, iface := range to.Interfaces {
		for _, exp := range from.Exports {
			if strings.Contains(exp.Signature, iface.Name) {
				return iface.Name
			}
		}
	}
	return ""
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
