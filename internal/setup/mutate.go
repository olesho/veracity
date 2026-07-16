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

// resolveProject builds a validated lockfile.Project from a ProjectInput using
// the given preset defaults for unset features.
func resolveProject(p ProjectInput, def presetDefaults) (lockfile.Project, error) {
	feat := def.features
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
	}
	if feat.Diagrams {
		feat.Markdown = true
	}
	proj := lockfile.Project{Name: p.Name, Language: p.Language, Features: feat}
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
	files, err := Render(filepath.Base(mustAbs(root)), lock)
	if err != nil {
		return nil, err
	}
	return execute(root, lock, files)
}

// Edit toggles feature flags on an existing project (language/modulePath/layout
// are immutable). confirm must equal name.
func Edit(root, name, confirm string, feats FeaturesInput) (*Result, error) {
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
	f := &lock.Projects[idx].Features
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
	if f.Diagrams {
		f.Markdown = true // invariant enforced in the engine
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	files, err := Render(filepath.Base(mustAbs(root)), lock)
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
