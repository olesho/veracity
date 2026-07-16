// Package docgen extracts a project's structure into IR (via the AST) and
// renders it as deterministic Markdown, HTML, and SVG. Rendering is pure and
// free — no LLM — so the Stop hook can run it every turn. Prose summaries are
// written by the running Claude Code agent (see status.go/enrich.go) and cached
// by content hash, so they are reused until the code they describe changes.
package docgen

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/olesho/harness/internal/docgen/extract"
	"github.com/olesho/harness/internal/docgen/ir"
	"github.com/olesho/harness/internal/fileset"
	"github.com/olesho/harness/internal/gitq"
	"github.com/olesho/harness/internal/lockfile"
)

// ExtractProject builds the IR for one project.
func ExtractProject(root string, lock *lockfile.Lock, proj lockfile.Project) (ir.IR, error) {
	dir := projectDir(root, lock, proj)
	switch proj.Language {
	case lockfile.LangGo:
		return extract.Go(dir, proj.ModulePath, proj.Name)
	case lockfile.LangPython, lockfile.LangTS:
		// Subprocess extractors are a postponed slice; emit an empty IR so
		// markdown/diagram generation degrades gracefully rather than failing.
		return ir.IR{SchemaVersion: ir.SchemaVersion, Subproject: proj.Name, Language: proj.Language}, nil
	default:
		return ir.IR{}, fmt.Errorf("unsupported language %q", proj.Language)
	}
}

// MarkdownProject renders docs/MODULES.md for one project. It returns whether it
// wrote (a byte-identical output is a no-op, so a deleted file is regenerated
// while an unchanged one causes no churn).
func MarkdownProject(root string, lock *lockfile.Lock, proj lockfile.Project, force bool) (bool, error) {
	dir := projectDir(root, lock, proj)
	doc, err := ExtractProject(root, lock, proj)
	if err != nil {
		return false, err
	}
	store, err := loadSummaries(dir)
	if err != nil {
		return false, err
	}
	return writeIfChanged(filepath.Join(dir, "docs", "MODULES.md"), RenderMarkdown(doc, store), force)
}

// RenderProject regenerates every output enabled by a project's features:
// MODULES.md (markdown) and modules.svg + modules.html (diagrams). Returns
// whether anything was written.
func RenderProject(root string, lock *lockfile.Lock, proj lockfile.Project, force bool) (bool, error) {
	dir := projectDir(root, lock, proj)
	doc, err := ExtractProject(root, lock, proj)
	if err != nil {
		return false, err
	}
	store, err := loadSummaries(dir)
	if err != nil {
		return false, err
	}
	wrote := false
	if proj.Features.Markdown {
		w, werr := writeIfChanged(filepath.Join(dir, "docs", "MODULES.md"), RenderMarkdown(doc, store), force)
		if werr != nil {
			return wrote, werr
		}
		wrote = wrote || w
	}
	if proj.Features.Diagrams {
		ds := BuildDiagrams(doc, store)
		// modules.svg is the whole graph (small projects) or the group overview
		// (chunked projects); per-group detail SVGs live under docs/modules/.
		top := ds.Whole
		if ds.Chunked {
			top = ds.Overview
			for _, g := range ds.Groups {
				gp := filepath.Join(dir, "docs", "modules", g.Slug+".svg")
				if w, werr := writeIfChanged(gp, g.SVG, force); werr != nil {
					return wrote, werr
				} else {
					wrote = wrote || w
				}
			}
		}
		w1, werr := writeIfChanged(filepath.Join(dir, "docs", "modules.svg"), top, force)
		if werr != nil {
			return wrote, werr
		}
		w2, werr := writeIfChanged(filepath.Join(dir, "docs", "modules.html"), RenderHTML(doc, store), force)
		if werr != nil {
			return wrote, werr
		}
		wrote = wrote || w1 || w2
	}
	return wrote, nil
}

// Markdown regenerates MODULES.md for every markdown-enabled project (or only
// those changed in the working tree when changedOnly).
func Markdown(root string, lock *lockfile.Lock, changedOnly, force bool, out io.Writer) (int, error) {
	return runTargets(root, lock, changedOnly, func(p lockfile.Project) bool { return p.Features.Markdown }, out,
		func(proj lockfile.Project) (bool, error) { return MarkdownProject(root, lock, proj, force) })
}

// Render regenerates all enabled outputs (markdown + diagrams) per project.
func Render(root string, lock *lockfile.Lock, changedOnly, force bool, out io.Writer) (int, error) {
	return runTargets(root, lock, changedOnly, func(p lockfile.Project) bool {
		return p.Features.Markdown || p.Features.Diagrams
	}, out, func(proj lockfile.Project) (bool, error) { return RenderProject(root, lock, proj, force) })
}

func runTargets(root string, lock *lockfile.Lock, changedOnly bool, enabled func(lockfile.Project) bool, out io.Writer, do func(lockfile.Project) (bool, error)) (int, error) {
	targets := docTargets(root, lock, changedOnly, enabled)
	n := 0
	for _, proj := range targets {
		wrote, err := do(proj)
		if err != nil {
			// Non-fatal: WIP source may not parse. Report and continue.
			fmt.Fprintf(out, "docs [%s]: skipped (%v)\n", proj.Name, err)
			continue
		}
		if wrote {
			fmt.Fprintf(out, "docs [%s]: regenerated\n", proj.Name)
			n++
		}
	}
	return n, nil
}

func docTargets(root string, lock *lockfile.Lock, changedOnly bool, enabled func(lockfile.Project) bool) []lockfile.Project {
	var all []lockfile.Project
	for _, p := range lock.Projects {
		if enabled(p) {
			all = append(all, p)
		}
	}
	if !changedOnly {
		return all
	}
	repo, err := gitq.Open(root)
	if err != nil {
		return all
	}
	changed, err := repo.WorkingTreeChanges()
	if err != nil {
		return all
	}
	groups := fileset.GroupByProject(lock, changed)
	var out []lockfile.Project
	for _, p := range all {
		if len(groups[p.Name]) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// writeIfChanged writes content to path unless an identical file already exists
// (or force is set). Returns whether it wrote.
func writeIfChanged(path string, content []byte, force bool) (bool, error) {
	if !force {
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, content) {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func projectDir(root string, lock *lockfile.Lock, proj lockfile.Project) string {
	base := proj.Path(lock.Layout)
	if base == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(base))
}
