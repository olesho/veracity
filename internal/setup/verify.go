package setup

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/olesho/veracity/internal/lockfile"
	"github.com/olesho/veracity/internal/ownership"
)

// Level classifies a verify finding.
type Level int

const (
	LevelNotice Level = iota // informational (e.g. an intentionally disabled feature)
	LevelWarn                // drift a human should notice, but not a hard failure
	LevelFail                // the project does not match its lock
)

func (l Level) String() string {
	switch l {
	case LevelFail:
		return "FAIL"
	case LevelWarn:
		return "WARN"
	default:
		return "NOTICE"
	}
}

// Issue is one verify finding.
type Issue struct {
	Level   Level
	Message string
}

// VerifyResult aggregates verify findings.
type VerifyResult struct{ Issues []Issue }

// OK reports whether verification passed (no FAIL-level issues).
func (r *VerifyResult) OK() bool {
	for _, i := range r.Issues {
		if i.Level == LevelFail {
			return false
		}
	}
	return true
}

func (r *VerifyResult) add(level Level, format string, args ...any) {
	r.Issues = append(r.Issues, Issue{Level: level, Message: fmt.Sprintf(format, args...)})
}

// langMarker is the file whose presence identifies a language on disk.
func langMarker(lang string) string {
	switch lang {
	case lockfile.LangGo:
		return "go.mod"
	case lockfile.LangPython:
		return "pyproject.toml"
	case lockfile.LangTS:
		return "package.json"
	default:
		return ""
	}
}

func lintConfigs(lang string) []string {
	switch lang {
	case lockfile.LangGo:
		return []string{".golangci.yml"}
	case lockfile.LangPython:
		return []string{"pyproject.toml"}
	case lockfile.LangTS:
		return []string{"eslint.config.js", ".prettierrc.json", "tsconfig.json"}
	default:
		return nil
	}
}

// Verify checks that a managed project on disk matches its lock. It is
// capability-aware: it never requires files for a disabled capability/feature.
func Verify(root string) (*VerifyResult, error) {
	res := &VerifyResult{}
	lock, err := lockfile.Load(root)
	if err != nil {
		res.add(LevelFail, "veracity.lock.json: %v", err)
		return res, nil
	}
	manifest, err := ownership.Load(root)
	if err != nil {
		return nil, err
	}

	// Monorepo: every projects/* dir must be registered.
	if lock.Layout == lockfile.LayoutMonorepo {
		verifyNoOrphanProjects(root, lock, res)
	}

	for _, p := range lock.Projects {
		base := p.Path(lock.Layout)
		dir := root
		if base != "." {
			dir = filepath.Join(root, filepath.FromSlash(base))
		}
		info, err := os.Lstat(dir)
		if err != nil {
			res.add(LevelFail, "project %q: directory %s is missing", p.Name, base)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			res.add(LevelFail, "project %q: %s is a symlink", p.Name, base)
			continue
		}

		// Language marker must exist and match.
		marker := langMarker(p.Language)
		if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
			res.add(LevelFail, "project %q: expected %s marker %q not found (language drift?)", p.Name, p.Language, marker)
		} else if p.Language == lockfile.LangGo {
			verifyGoModule(dir, p, res)
		}

		// Lint config existence (only when lint is enabled).
		if p.Features.Lint {
			for _, cfg := range lintConfigs(p.Language) {
				if _, err := os.Stat(filepath.Join(dir, cfg)); err != nil {
					res.add(LevelFail, "project %q: lint is enabled but config %q is missing", p.Name, cfg)
				}
			}
		} else {
			res.add(LevelNotice, "project %q: linting is disabled by its lock configuration", p.Name)
		}
		if !p.Features.Test {
			res.add(LevelNotice, "project %q: testing is disabled by its lock configuration", p.Name)
		}
	}

	// Managed wiring files: must exist and match the manifest hash.
	verifyManagedFiles(root, manifest, res)

	return res, nil
}

func verifyNoOrphanProjects(root string, lock *lockfile.Lock, res *VerifyResult) {
	entries, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil {
		return // no projects/ dir yet is fine
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := lock.Find(e.Name()); !ok {
			res.add(LevelFail, "unregistered project directory projects/%s (run `veracity add`)", e.Name())
		}
	}
}

func verifyGoModule(dir string, p lockfile.Project, res *VerifyResult) {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module ") {
			got := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			if got != p.ModulePath {
				res.add(LevelFail, "project %q: go.mod module %q does not match lock modulePath %q", p.Name, got, p.ModulePath)
			}
			return
		}
	}
}

func verifyManagedFiles(root string, m *ownership.Manifest, res *VerifyResult) {
	for _, rel := range m.SortedPaths() {
		entry, _ := m.Get(rel)
		if entry.Kind != ownership.Managed {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			res.add(LevelFail, "managed file %s is missing — run `veracity restore` to regenerate it", rel)
			continue
		}
		if ownership.Hash(data) != entry.Hash {
			// Managed wiring is veracity's to own: a local edit is drift, not a
			// preference. FAIL (not WARN) so the hooks and CI actually stop it.
			res.add(LevelFail, "managed file %s was edited by hand; veracity owns it and will not honor the change — run `veracity restore` to discard the edit, or `git checkout -- %s`", rel, rel)
		}
	}
}
