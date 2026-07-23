// Package ownership tracks which generated files the veracity manages versus
// which it merely seeds and hands to the user. The committed manifest records
// the last-generated content hash of every managed file, so regeneration can
// tell "unchanged since I wrote it" (safe to replace) from "the user edited it"
// (never silently overwrite) — and native language config, once seeded, is
// owned by the user and never clobbered.
package ownership

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// RelPath of the committed manifest inside a managed repo.
const RelPath = ".veracity/manifest.json"

// SchemaVersion of the manifest document.
const SchemaVersion = 1

// Kind classifies a generated file.
type Kind string

const (
	// Managed files are authored and owned by the veracity (hook shims, agent
	// wiring, CI). They are reconciled on regeneration and drift-checked by verify.
	Managed Kind = "managed"
	// Owned files are seeded once by the veracity then owned by the user (native
	// lint config). They are never overwritten after creation.
	Owned Kind = "owned"
)

// Entry records a managed/owned file's provenance.
type Entry struct {
	Hash string `json:"hash"` // sha256 hex of the last content the veracity wrote
	Kind Kind   `json:"kind"`
}

// Manifest is the committed ownership document.
type Manifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Files         map[string]Entry `json:"files"`
}

// New returns an empty manifest.
func New() *Manifest {
	return &Manifest{SchemaVersion: SchemaVersion, Files: map[string]Entry{}}
}

// Hash returns the sha256 hex digest of content.
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Load reads the manifest from a repo root. A missing manifest is not an error
// (a minimal project has no managed files yet) — it yields an empty manifest.
func Load(root string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(RelPath)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return New(), nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Files == nil {
		m.Files = map[string]Entry{}
	}
	return &m, nil
}

// Marshal renders the manifest deterministically (map keys are sorted by
// encoding/json), with a trailing newline.
func (m *Manifest) Marshal() ([]byte, error) {
	if m.Files == nil {
		m.Files = map[string]Entry{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Empty reports whether the manifest tracks no files (so it need not be written).
func (m *Manifest) Empty() bool { return len(m.Files) == 0 }

// Set records a file's kind and last-generated hash.
func (m *Manifest) Set(rel string, kind Kind, content []byte) {
	m.Files[filepath.ToSlash(rel)] = Entry{Hash: Hash(content), Kind: kind}
}

// Remove drops a file from the manifest (used when disabling a capability).
func (m *Manifest) Remove(rel string) { delete(m.Files, filepath.ToSlash(rel)) }

// Get returns a file's entry.
func (m *Manifest) Get(rel string) (Entry, bool) {
	e, ok := m.Files[filepath.ToSlash(rel)]
	return e, ok
}

// SortedPaths returns manifest paths in deterministic order.
func (m *Manifest) SortedPaths() []string {
	out := make([]string, 0, len(m.Files))
	for k := range m.Files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Decision is what regeneration should do with a rendered file.
type Decision int

const (
	// Create: the target is absent — write it.
	Create Decision = iota
	// Replace: a managed target is unchanged since the veracity wrote it — safe
	// to overwrite with freshly rendered content.
	Replace
	// Skip: an owned target already exists — leave the user's file untouched.
	Skip
	// Conflict: a managed target was edited locally — do not overwrite; write a
	// "<path>.veracity-new" side file and report.
	Conflict
	// NoChange: a managed target already matches the freshly rendered content.
	NoChange
)

// Decide computes what to do for a rendered managed/owned file, given the
// current on-disk bytes (onDisk is nil if the file is absent).
func (m *Manifest) Decide(rel string, kind Kind, onDisk, rendered []byte) Decision {
	rel = filepath.ToSlash(rel)
	if onDisk == nil {
		return Create
	}
	if kind == Owned {
		return Skip // seeded once; user owns it forever
	}
	// Managed:
	if bytes.Equal(onDisk, rendered) {
		return NoChange
	}
	entry, tracked := m.Files[rel]
	if tracked && Hash(onDisk) == entry.Hash {
		return Replace // unchanged since we wrote it → safe to update
	}
	return Conflict // locally modified (or untracked pre-existing) → don't clobber
}
