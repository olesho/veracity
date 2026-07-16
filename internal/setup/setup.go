// Package setup scaffolds and reconciles harness-managed projects. It resolves a
// preset/config Input into a validated lock, renders the (capability-conditioned)
// file set from embedded assets, and applies it transactionally while honoring
// per-file ownership.
package setup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/olesho/harness/internal/gitq"
	"github.com/olesho/harness/internal/lockfile"
)

// Options control setup behavior.
type Options struct {
	NoGit bool // do not `git init` when the target is not a repo
	Adopt bool // proceed even if the target dir has pre-existing non-harness files
}

// Init scaffolds a brand-new managed project into root. It errors if the target
// is already initialized (a lock exists) or non-empty without --adopt.
func Init(root string, in *Input, opts Options) (*Result, error) {
	if lockfile.Exists(root) {
		return nil, errors.New("already initialized: harness.lock.json exists (use `harness add`/`harness edit`)")
	}
	if !opts.Adopt {
		nonHarness, err := hasForeignFiles(root)
		if err != nil {
			return nil, err
		}
		if nonHarness {
			return nil, errors.New("target directory is not empty; refusing to overwrite unmanaged files (re-run with --adopt to proceed)")
		}
	}

	lock, err := Resolve(in)
	if err != nil {
		return nil, err
	}

	// Adopting an existing project: take the real module path from the on-disk
	// go.mod (so verify passes) and do not scaffold the sample module.
	if opts.Adopt {
		if err := adoptExisting(root, lock); err != nil {
			return nil, err
		}
	}

	if !opts.NoGit && !gitq.IsRepo(root) {
		if err := gitInit(root); err != nil {
			return nil, fmt.Errorf("git init: %w", err)
		}
	}

	files, err := Render(filepath.Base(mustAbs(root)), lock, !opts.Adopt)
	if err != nil {
		return nil, err
	}
	return execute(root, lock, files)
}

// hasForeignFiles reports whether root contains any entry other than .git and
// the .harness state dir.
func hasForeignFiles(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		switch e.Name() {
		case ".git", ".harness":
			continue
		default:
			return true, nil
		}
	}
	return false, nil
}

func gitInit(root string) error {
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	return cmd.Run()
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// projectDir returns the absolute directory of a project within root.
func projectDir(root string, lock *lockfile.Lock, proj lockfile.Project) string {
	base := proj.Path(lock.Layout)
	if base == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(base))
}
