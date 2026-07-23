// Package txn provides a crash-safe, all-or-nothing transaction for the file
// mutations veracity setup/add/edit/remove/upgrade perform. It uses a
// write-ahead journal plus pre-image backups so a crash at any point — including
// mid-overwrite — is fully recoverable: operations before the designated commit
// point (the lock-file write) roll back to the pre-transaction state, and the
// commit point being reached means the transaction is durable.
package txn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrConflict is returned when a target file changed on disk between planning
// and applying (a TOCTOU guard), or when --force was not given for such a case.
var ErrConflict = errors.New("target changed on disk during transaction")

// errFault is used only by tests to simulate a crash mid-apply.
var errFault = errors.New("injected fault")

const (
	txnRel      = ".veracity/txn/current"
	lockRel     = ".veracity/lock"
	journalName = "journal.json"
	progressN   = "progress"
)

type opKind string

const (
	kWrite  opKind = "write"
	kMove   opKind = "move"
	kDelete opKind = "delete"
)

type op struct {
	Kind       opKind `json:"kind"`
	Path       string `json:"path,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
	StagedRef  string `json:"stagedRef,omitempty"`
	BackupRef  string `json:"backupRef,omitempty"`
	HadTarget  bool   `json:"hadTarget"`
	ExpectHash string `json:"expectHash,omitempty"`
	Commit     bool   `json:"commit,omitempty"`
}

type journal struct {
	CommitIndex int  `json:"commitIndex"`
	Ops         []op `json:"ops"`
}

// Txn accumulates operations and applies them atomically on Commit.
type Txn struct {
	root   string
	dir    string // absolute txn dir
	lock   *fileLock
	ops    []op
	staged int // counter for staged/backup filenames

	// FaultAfter, when > 0, makes Commit simulate a crash after applying that
	// many operations (test-only), leaving the journal for Repair.
	FaultAfter int
}

// Begin repairs any prior incomplete transaction, then starts a new one holding
// the exclusive mutation lock. Callers must Close the returned Txn.
func Begin(root string) (*Txn, error) {
	if err := os.MkdirAll(filepath.Join(root, ".veracity"), 0o755); err != nil {
		return nil, err
	}
	if err := Repair(root); err != nil {
		return nil, fmt.Errorf("repairing prior transaction: %w", err)
	}
	lock, err := acquireLock(filepath.Join(root, filepath.FromSlash(lockRel)))
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, filepath.FromSlash(txnRel))
	if err := os.RemoveAll(dir); err != nil {
		_ = lock.release()
		return nil, err
	}
	for _, sub := range []string{"staged", "backup"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			_ = lock.release()
			return nil, err
		}
	}
	return &Txn{root: root, dir: dir, lock: lock}, nil
}

// Write stages a file creation/overwrite. If commit is true this op is the
// transaction's commit point (there must be exactly one, and it must be last).
// All backup/staging refs are assigned now so the journal is written once.
func (t *Txn) Write(relPath string, content []byte, mode os.FileMode, commit bool) error {
	ref := t.nextRef()
	if err := os.WriteFile(filepath.Join(t.dir, "staged", ref), content, 0o644); err != nil {
		return err
	}
	o := op{Kind: kWrite, Path: filepath.ToSlash(relPath), Mode: uint32(mode), StagedRef: ref, Commit: commit}
	if h, ok := hashFile(t.abs(relPath)); ok {
		o.HadTarget = true
		o.ExpectHash = h
		o.BackupRef = t.nextRef()
	}
	t.ops = append(t.ops, o)
	return nil
}

// Move stages a rename (used by remove --archive to relocate a project dir).
func (t *Txn) Move(fromRel, toRel string) {
	t.ops = append(t.ops, op{Kind: kMove, From: filepath.ToSlash(fromRel), To: filepath.ToSlash(toRel)})
}

// Delete stages a file removal (pre-image backed up for rollback).
func (t *Txn) Delete(relPath string) {
	o := op{Kind: kDelete, Path: filepath.ToSlash(relPath)}
	if _, ok := hashFile(t.abs(relPath)); ok {
		o.HadTarget = true
		o.BackupRef = t.nextRef()
	}
	t.ops = append(t.ops, o)
}

// Commit writes the journal, then applies every staged op atomically. On any
// error it rolls back to the pre-transaction state. The lock is held throughout;
// call Close to release it.
func (t *Txn) Commit() error {
	commitIndex := -1
	for i, o := range t.ops {
		if o.Commit {
			if commitIndex >= 0 {
				return errors.New("txn: multiple commit ops")
			}
			commitIndex = i
		}
	}
	if commitIndex >= 0 && commitIndex != len(t.ops)-1 {
		return errors.New("txn: commit op must be last")
	}

	j := journal{CommitIndex: commitIndex, Ops: t.ops}
	if err := t.writeJournal(j); err != nil {
		return err
	}
	if err := t.setProgress(0); err != nil {
		return err
	}

	for i, o := range t.ops {
		if err := t.applyOp(o); err != nil {
			// Roll back everything applied so far (indices [0, i)).
			_ = rollback(t.root, t.dir, j, i)
			_ = os.RemoveAll(t.dir)
			return err
		}
		if err := t.setProgress(i + 1); err != nil {
			_ = rollback(t.root, t.dir, j, i+1)
			_ = os.RemoveAll(t.dir)
			return err
		}
		if t.FaultAfter > 0 && i+1 >= t.FaultAfter {
			return errFault // simulate crash after applying (i+1) ops: journal + backups remain for Repair
		}
	}
	return os.RemoveAll(t.dir)
}

// Close releases the mutation lock.
func (t *Txn) Close() error {
	if t.lock != nil {
		err := t.lock.release()
		t.lock = nil
		return err
	}
	return nil
}

func (t *Txn) applyOp(o op) error {
	switch o.Kind {
	case kWrite:
		abs := t.abs(o.Path)
		if o.HadTarget {
			// TOCTOU guard: the file must be unchanged since planning.
			if h, ok := hashFile(abs); !ok || h != o.ExpectHash {
				return fmt.Errorf("%w: %s", ErrConflict, o.Path)
			}
			if err := copyFile(abs, filepath.Join(t.dir, "backup", o.BackupRef)); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		content, err := os.ReadFile(filepath.Join(t.dir, "staged", o.StagedRef))
		if err != nil {
			return err
		}
		return atomicWrite(abs, content, os.FileMode(o.Mode))
	case kMove:
		from, to := t.abs(o.From), t.abs(o.To)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.Rename(from, to)
	case kDelete:
		abs := t.abs(o.Path)
		if o.HadTarget {
			if err := copyFile(abs, filepath.Join(t.dir, "backup", o.BackupRef)); err != nil {
				return err
			}
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	default:
		return fmt.Errorf("txn: unknown op kind %q", o.Kind)
	}
}

func (t *Txn) nextRef() string {
	t.staged++
	return strconv.Itoa(t.staged)
}

func (t *Txn) abs(rel string) string { return filepath.Join(t.root, filepath.FromSlash(rel)) }

func (t *Txn) writeJournal(j journal) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(t.dir, journalName), data, 0o644)
}

func (t *Txn) setProgress(n int) error {
	return atomicWrite(filepath.Join(t.dir, progressN), []byte(strconv.Itoa(n)), 0o644)
}

// Repair heals an incomplete transaction left by a crash. It rolls back if the
// commit point was not reached, or rolls forward and finalizes if it was. A
// clean tree (no journal) is a no-op.
func Repair(root string) error {
	dir := filepath.Join(root, filepath.FromSlash(txnRel))
	jpath := filepath.Join(dir, journalName)
	data, err := os.ReadFile(jpath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var j journal
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	progress := readProgress(dir)

	switch {
	case progress >= len(j.Ops):
		// Fully applied but not yet cleaned up: just finalize.
	case j.CommitIndex >= 0 && progress > j.CommitIndex:
		// Past the commit point: roll forward the remaining ops.
		if err := rollForward(root, dir, j, progress); err != nil {
			return err
		}
	default:
		// Commit point not reached: roll back what was applied.
		if err := rollback(root, dir, j, progress); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

func rollback(root, dir string, j journal, applied int) error {
	for i := applied - 1; i >= 0; i-- {
		o := j.Ops[i]
		abs := filepath.Join(root, filepath.FromSlash(o.Path))
		switch o.Kind {
		case kWrite:
			if o.HadTarget && o.BackupRef != "" {
				if err := copyFile(filepath.Join(dir, "backup", o.BackupRef), abs); err != nil {
					return err
				}
			} else {
				_ = os.Remove(abs)
			}
		case kMove:
			from := filepath.Join(root, filepath.FromSlash(o.From))
			to := filepath.Join(root, filepath.FromSlash(o.To))
			_ = os.MkdirAll(filepath.Dir(from), 0o755)
			if err := os.Rename(to, from); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		case kDelete:
			if o.BackupRef != "" {
				if err := copyFile(filepath.Join(dir, "backup", o.BackupRef), abs); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func rollForward(root, dir string, j journal, from int) error {
	for i := from; i < len(j.Ops); i++ {
		o := j.Ops[i]
		switch o.Kind {
		case kWrite:
			content, err := os.ReadFile(filepath.Join(dir, "staged", o.StagedRef))
			if err != nil {
				return err
			}
			abs := filepath.Join(root, filepath.FromSlash(o.Path))
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return err
			}
			if err := atomicWrite(abs, content, os.FileMode(o.Mode)); err != nil {
				return err
			}
		case kMove:
			from := filepath.Join(root, filepath.FromSlash(o.From))
			to := filepath.Join(root, filepath.FromSlash(o.To))
			_ = os.MkdirAll(filepath.Dir(to), 0o755)
			if err := os.Rename(from, to); err != nil {
				return err
			}
		case kDelete:
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(o.Path)))
		}
	}
	return nil
}

func readProgress(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, progressN))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// --- low-level file helpers ---

func atomicWrite(path string, content []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".veracity-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return fsyncDir(dir)
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	// Directory fsync is a no-op / unsupported on some platforms; ignore EINVAL.
	if err := d.Sync(); err != nil {
		return nil
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func hashFile(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
