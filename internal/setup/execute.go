package setup

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/ownership"
	"github.com/olesho/harness/internal/setup/txn"
	"github.com/olesho/harness/internal/version"
)

// Result reports the outcome of a mutating operation.
type Result struct {
	// Conflicts lists managed files that were locally modified and therefore not
	// overwritten; a "<file>.harness-new" side file was written for each.
	Conflicts []string
	// Created/Replaced/Skipped counts for reporting.
	Created, Replaced, Skipped int
}

func fileMode(rel string) os.FileMode {
	if strings.HasSuffix(rel, ".sh") {
		return 0o755
	}
	return 0o644
}

func readIfExists(root, rel string) []byte {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	return b
}

// execute applies the rendered file set plus the manifest, .harness-version, and
// lock (commit point) in a single transaction, honoring ownership rules.
func execute(root string, lock *lockfile.Lock, files []renderFile) (*Result, error) {
	manifest, err := ownership.Load(root)
	if err != nil {
		return nil, err
	}
	res := &Result{}

	tx, err := txn.Begin(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Close() }()

	for _, f := range files {
		onDisk := readIfExists(root, f.Rel)
		switch manifest.Decide(f.Rel, f.Kind, onDisk, f.Content) {
		case ownership.Create:
			if err := tx.Write(f.Rel, f.Content, fileMode(f.Rel), false); err != nil {
				return nil, err
			}
			if f.Kind == ownership.Managed {
				manifest.Set(f.Rel, ownership.Managed, f.Content)
			}
			res.Created++
		case ownership.Replace:
			if err := tx.Write(f.Rel, f.Content, fileMode(f.Rel), false); err != nil {
				return nil, err
			}
			manifest.Set(f.Rel, ownership.Managed, f.Content)
			res.Replaced++
		case ownership.NoChange:
			// Already matches; keep the manifest entry accurate.
			if f.Kind == ownership.Managed {
				manifest.Set(f.Rel, ownership.Managed, f.Content)
			}
			res.Skipped++
		case ownership.Skip:
			// Owned file already present — leave the user's copy untouched.
			res.Skipped++
		case ownership.Conflict:
			// Managed file was edited locally — never clobber; drop a side file.
			if err := tx.Write(f.Rel+".harness-new", f.Content, fileMode(f.Rel), false); err != nil {
				return nil, err
			}
			res.Conflicts = append(res.Conflicts, f.Rel)
		}
	}

	// Manifest (managed files only) — write only when non-empty.
	if !manifest.Empty() {
		mb, err := manifest.Marshal()
		if err != nil {
			return nil, err
		}
		if err := tx.Write(ownership.RelPath, mb, 0o644, false); err != nil {
			return nil, err
		}
	}

	// Version pin (core file).
	if err := tx.Write(version.PinFileName, []byte(version.Version+"\n"), 0o644, false); err != nil {
		return nil, err
	}

	// Lock file — the commit point, written last.
	lb, err := lockfile.Marshal(lock)
	if err != nil {
		return nil, err
	}
	if err := tx.Write(lockfile.FileName, lb, 0o644, true); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}
