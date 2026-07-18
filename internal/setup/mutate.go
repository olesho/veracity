package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/setup/txn"
	"github.com/olesho/harness/internal/version"
)

// resolveProject builds a lockfile.Project from a ProjectInput using the given
// preset defaults for unset features. Language-specific verifier defaults apply
// only to their language; lockfile.Validate rejects them on other languages.
func resolveProject(p ProjectInput, def presetDefaults) (lockfile.Project, error) {
	feat := def.features
	switch p.Language {
	case lockfile.LangGo:
		g := def.goFeatureDefaults()
		feat.Gofumpt, feat.Gci, feat.ModTidy, feat.Coverage = g.Gofumpt, g.Gci, g.ModTidy, g.Coverage
	case lockfile.LangTS:
		t := def.tsFeatureDefaults()
		feat.Coverage, feat.Audit, feat.Semgrep = t.Coverage, t.Audit, t.Semgrep
	}
	if f := p.Features; f != nil {
		if f.Lint != nil {
			feat.Lint = *f.Lint
		}
		if f.Test != nil {
			feat.Test = *f.Test
		}
		if f.Markdown != nil {
			feat.Markdown = *f.Markdown
		}
		if f.Diagrams != nil {
			feat.Diagrams = *f.Diagrams
		}
		if f.Gofumpt != nil {
			feat.Gofumpt = *f.Gofumpt
		}
		if f.Gci != nil {
			feat.Gci = *f.Gci
		}
		if f.ModTidy != nil {
			feat.ModTidy = *f.ModTidy
		}
		if f.Coverage != nil {
			feat.Coverage = *f.Coverage
		}
		if f.Audit != nil {
			feat.Audit = *f.Audit
		}
		if f.Semgrep != nil {
			feat.Semgrep = *f.Semgrep
		}
		if f.Sonar != nil {
			feat.Sonar = *f.Sonar
		}
	}
	if feat.Diagrams {
		feat.Markdown = true
	}
	proj := lockfile.Project{Name: p.Name, Language: p.Language, Features: feat}
	if p.CoverageMin != nil {
		proj.CoverageMin = *p.CoverageMin
	}
	if p.Language == lockfile.LangGo {
		proj.ModulePath = p.ModulePath
		if proj.ModulePath == "" {
			proj.ModulePath = "example.com/" + p.Name
		}
	}
	return proj, nil
}

// Add appends new projects to an existing monorepo lock (single layout refuses:
// migration to monorepo is a deliberate future operation).
func Add(root string, in *Input) (*Result, error) {
	lock, err := lockfile.Load(root)
	if err != nil {
		return nil, err
	}
	if lock.Layout == lockfile.LayoutSingle {
		return nil, errors.New("cannot add projects to a single-layout repo (choose monorepo up front; single→monorepo migration is not yet supported)")
	}
	def, err := presetFor(in.Preset)
	if err != nil {
		return nil, err
	}
	for _, pi := range in.Projects {
		if _, exists := lock.Find(pi.Name); exists {
			return nil, fmt.Errorf("project %q already exists", pi.Name)
		}
		proj, err := resolveProject(pi, def)
		if err != nil {
			return nil, err
		}
		lock.Projects = append(lock.Projects, proj)
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	// New subprojects get the full scaffold (sample included).
	files, err := Render(filepath.Base(mustAbs(root)), lock, true)
	if err != nil {
		return nil, err
	}
	return execute(root, lock, files)
}

// EditInput carries the mutable per-project settings `harness edit` changes.
// Unset (nil) fields are left unchanged; --coverage-min uses a pointer so an
// explicit 0 is distinguishable from an omitted flag.
type EditInput struct {
	Features    FeaturesInput
	CoverageMin *int
}

// Edit toggles feature flags on an existing project (language/modulePath/layout
// are immutable). confirm must equal name.
func Edit(root, name, confirm string, in EditInput) (*Result, error) {
	if confirm != name {
		return nil, fmt.Errorf("edit requires --confirm %s", name)
	}
	lock, err := lockfile.Load(root)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, p := range lock.Projects {
		if p.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("no such project %q", name)
	}
	p := &lock.Projects[idx]
	f := &p.Features
	feats := in.Features
	if feats.Lint != nil {
		f.Lint = *feats.Lint
	}
	if feats.Test != nil {
		f.Test = *feats.Test
	}
	if feats.Markdown != nil {
		f.Markdown = *feats.Markdown
	}
	if feats.Diagrams != nil {
		f.Diagrams = *feats.Diagrams
	}
	if feats.Gofumpt != nil {
		f.Gofumpt = *feats.Gofumpt
	}
	if feats.Gci != nil {
		f.Gci = *feats.Gci
	}
	if feats.ModTidy != nil {
		f.ModTidy = *feats.ModTidy
	}
	if feats.Coverage != nil {
		f.Coverage = *feats.Coverage
	}
	if feats.Audit != nil {
		f.Audit = *feats.Audit
	}
	if feats.Semgrep != nil {
		f.Semgrep = *feats.Semgrep
	}
	if feats.Sonar != nil {
		f.Sonar = *feats.Sonar
	}
	if f.Diagrams {
		f.Markdown = true // invariant enforced in the engine
	}
	if in.CoverageMin != nil {
		p.CoverageMin = *in.CoverageMin
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	// Reconcile config + wiring only — never re-scaffold sample source on edit.
	files, err := Render(filepath.Base(mustAbs(root)), lock, false)
	if err != nil {
		return nil, err
	}
	return execute(root, lock, files)
}

// Reconfigure changes repo-level capabilities after setup and reconciles the
// wiring: newly enabled capabilities have their files created; newly disabled
// ones have their managed files pruned. Project source and native config are
// untouched.
func Reconfigure(root string, changes CapsInput) (*Result, error) {
	lock, err := lockfile.Load(root)
	if err != nil {
		return nil, err
	}
	c := &lock.Capabilities
	if changes.Agents != nil {
		c.Agents = append([]string{}, (*changes.Agents)...)
	}
	if changes.GitHooks != nil {
		c.GitHooks = *changes.GitHooks
	}
	if changes.CI != nil {
		c.CI = *changes.CI
	}
	if changes.AgentDocs != nil {
		c.AgentDocs = *changes.AgentDocs
	}
	if changes.Skills != nil {
		c.Skills = *changes.Skills
	}
	if c.Agents == nil {
		c.Agents = []string{}
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	// Reconcile config + wiring only (no sample source).
	files, err := Render(filepath.Base(mustAbs(root)), lock, false)
	if err != nil {
		return nil, err
	}
	return execute(root, lock, files)
}

// Remove unregisters a project. It refuses while the project directory still
// exists unless archive is set, in which case the directory is atomically moved
// to archived/<name> so the repo is verify-clean immediately.
func Remove(root, name, confirm string, archive bool) error {
	if confirm != name {
		return fmt.Errorf("remove requires --confirm %s", name)
	}
	lock, err := lockfile.Load(root)
	if err != nil {
		return err
	}
	proj, ok := lock.Find(name)
	if !ok {
		return fmt.Errorf("no such project %q", name)
	}
	base := proj.Path(lock.Layout)
	dirExists := false
	if base != "." {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(base))); err == nil {
			dirExists = true
		}
	}
	if base == "." {
		return errors.New("cannot remove the sole project of a single-layout repo")
	}
	if dirExists && !archive {
		return fmt.Errorf("project directory %s still exists; delete it first, or re-run with --archive to move it to archived/%s", base, name)
	}

	// Drop the entry.
	kept := lock.Projects[:0:0]
	for _, p := range lock.Projects {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	lock.Projects = kept
	if err := lock.Validate(); err != nil {
		return err
	}

	tx, err := txn.Begin(root)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Close() }()
	if dirExists && archive {
		tx.Move(base, "archived/"+name)
	}
	// Rewrite the version pin (unchanged) and the lock (commit point).
	if err := tx.Write(version.PinFileName, []byte(version.Version+"\n"), 0o644, false); err != nil {
		return err
	}
	lb, err := lockfile.Marshal(lock)
	if err != nil {
		return err
	}
	if err := tx.Write(lockfile.FileName, lb, 0o644, true); err != nil {
		return err
	}
	return tx.Commit()
}
