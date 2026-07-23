package setup

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/olesho/veracity/internal/lockfile"
	"github.com/olesho/veracity/internal/ownership"
	"github.com/olesho/veracity/internal/setup/txn"
	"github.com/olesho/veracity/internal/version"
)

// Result reports the outcome of a mutating operation.
type Result struct {
	// Conflicts lists managed files that were locally modified and therefore not
	// overwritten; a "<file>.veracity-new" side file was written for each.
	Conflicts []string
	// Created/Replaced/Skipped/Pruned counts for reporting.
	Created, Replaced, Skipped, Pruned int
	// Restored counts managed files whose local edits were discarded and
	// rewritten from the generated content (`veracity restore` only).
	Restored int
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

// execute applies the rendered file set plus the manifest, .veracity-version, and
// lock (commit point) in a single transaction, honoring ownership rules.
//
// forceManaged makes a locally-modified managed file be overwritten rather than
// side-filed — the deliberate "discard my edits and restore the generated
// content" path behind `veracity restore`. Owned files are never forced.
func execute(root string, lock *lockfile.Lock, files []renderFile, forceManaged bool) (*Result, error) {
	manifest, err := ownership.Load(root)
	if err != nil {
		return nil, err
	}
	res := &Result{}

	// Snapshot the currently-managed files and the set now being rendered, so we
	// can prune managed files that a now-disabled capability no longer emits.
	oldManaged := map[string]bool{}
	for _, rel := range manifest.SortedPaths() {
		if e, _ := manifest.Get(rel); e.Kind == ownership.Managed {
			oldManaged[rel] = true
		}
	}
	renderedManaged := map[string]bool{}
	for _, f := range files {
		if f.Kind == ownership.Managed {
			renderedManaged[f.Rel] = true
		}
	}

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
			if forceManaged {
				// Restore: the caller explicitly asked to discard local edits.
				if err := tx.Write(f.Rel, f.Content, fileMode(f.Rel), false); err != nil {
					return nil, err
				}
				manifest.Set(f.Rel, ownership.Managed, f.Content)
				res.Restored++
				break
			}
			// Managed file was edited locally — never clobber; drop a side file.
			if err := tx.Write(f.Rel+".veracity-new", f.Content, fileMode(f.Rel), false); err != nil {
				return nil, err
			}
			res.Conflicts = append(res.Conflicts, f.Rel)
		}
	}

	// Prune managed files that are no longer rendered (e.g. a disabled
	// capability). Owned files (source, native config, generated docs) are never
	// pruned — only veracity-managed wiring.
	for rel := range oldManaged {
		if !renderedManaged[rel] {
			tx.Delete(rel)
			manifest.Remove(rel)
			res.Pruned++
		}
	}

	// Manifest (managed files only) — write when non-empty; otherwise remove a
	// now-empty manifest so no stale file lingers.
	manifestPath := ownership.RelPath
	if manifest.Empty() {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(manifestPath))); statErr == nil {
			tx.Delete(manifestPath)
		}
	} else {
		mb, err := manifest.Marshal()
		if err != nil {
			return nil, err
		}
		if err := tx.Write(manifestPath, mb, 0o644, false); err != nil {
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
