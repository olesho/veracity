// Package fileset centralizes file discovery, ignore rules, and the mapping
// from files to owning projects. All discovery ignore logic lives here (one
// place to handle vendor/generated/symlink/testdata dirs) so no tool's own
// directory-walk semantics are relied upon.
package fileset

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/olesho/veracity/internal/lockfile"
)

// ignoredSegments are path segments whose subtrees never contain first-party
// lintable or extractable source.
var ignoredSegments = map[string]bool{
	"vendor":        true,
	"node_modules":  true,
	".venv":         true,
	"venv":          true,
	"dist":          true,
	"build":         true,
	"testdata":      true,
	"__pycache__":   true,
	".git":          true,
	".veracity":     true,
	".docgen-cache": true,
	"archived":      true,
}

// Ignored reports whether a repo-relative path should be skipped. A path is
// ignored if any of its segments is a known ignored directory or a dotdir
// (except the leaf, so files like "eslint.config.js" are kept while ".venv/…"
// is not).
func Ignored(rel string) bool {
	rel = filepath.ToSlash(rel)
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		if ignoredSegments[s] {
			return true
		}
		// A dot-prefixed *directory* segment (not the final filename).
		if i < len(segs)-1 && strings.HasPrefix(s, ".") && s != "." && s != ".." {
			return true
		}
	}
	return false
}

// ProjectOf resolves a repo-relative path to its owning project. In single
// layout every non-ignored path maps to the sole project; in monorepo a path
// maps to the project whose "projects/<name>" prefix it shares.
func ProjectOf(l *lockfile.Lock, rel string) (lockfile.Project, bool) {
	rel = filepath.ToSlash(rel)
	if l.Layout == lockfile.LayoutSingle {
		if len(l.Projects) == 1 {
			return l.Projects[0], true
		}
		return lockfile.Project{}, false
	}
	for _, p := range l.Projects {
		base := p.Path(l.Layout) // projects/<name>
		if rel == base || strings.HasPrefix(rel, base+"/") {
			return p, true
		}
	}
	return lockfile.Project{}, false
}

// GroupByProject buckets repo-relative paths by owning project name. Paths that
// belong to no project (or are ignored) are dropped.
func GroupByProject(l *lockfile.Lock, paths []string) map[string][]string {
	out := map[string][]string{}
	for _, p := range paths {
		if Ignored(p) {
			continue
		}
		proj, ok := ProjectOf(l, p)
		if !ok {
			continue
		}
		out[proj.Name] = append(out[proj.Name], p)
	}
	return out
}

// RelToProject converts a repo-relative path to a path relative to the project
// root (the executor's cwd). For single layout (project path ".") it returns the
// path unchanged.
func RelToProject(l *lockfile.Lock, proj lockfile.Project, rel string) string {
	rel = filepath.ToSlash(rel)
	base := proj.Path(l.Layout)
	if base == "." {
		return rel
	}
	return strings.TrimPrefix(rel, base+"/")
}

// FilterByLanguage keeps candidate repo-relative paths that belong to proj and
// match the language's source extensions and are not ignored. Returned paths are
// project-root-relative and sorted.
func FilterByLanguage(l *lockfile.Lock, proj lockfile.Project, candidates []string) []string {
	exts := extSet(proj.Language)
	var out []string
	base := proj.Path(l.Layout)
	for _, c := range candidates {
		c = filepath.ToSlash(c)
		if Ignored(c) {
			continue
		}
		if base != "." && c != base && !strings.HasPrefix(c, base+"/") {
			continue
		}
		if !exts[strings.ToLower(filepath.Ext(c))] {
			continue
		}
		out = append(out, RelToProject(l, proj, c))
	}
	sort.Strings(out)
	return out
}

// EligibleFiles walks a project's directory on disk and returns its source
// files (project-root-relative, sorted) for the project's language, skipping
// ignored directories and symlinks. root is the repo root absolute path.
func EligibleFiles(root string, l *lockfile.Lock, proj lockfile.Project) ([]string, error) {
	exts := extSet(proj.Language)
	base := proj.Path(l.Layout)
	dir := root
	if base != "." {
		dir = filepath.Join(root, filepath.FromSlash(base))
	}
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (ignoredSegments[name] || (strings.HasPrefix(name, ".") && name != "." && name != "..")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never follow or lint symlinks
		}
		if !exts[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func extSet(lang string) map[string]bool {
	m := map[string]bool{}
	switch lang {
	case lockfile.LangGo:
		m[".go"] = true
	case lockfile.LangPython:
		m[".py"] = true
	case lockfile.LangTS:
		m[".ts"] = true
		m[".tsx"] = true
	}
	return m
}
