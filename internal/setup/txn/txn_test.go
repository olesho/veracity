package txn

import (
	"os"
	"path/filepath"
	"testing"
)

func read(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func seed(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommitCreatesAndOverwrites(t *testing.T) {
	root := t.TempDir()
	seed(t, root, "existing.txt", "old")

	tx, err := Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Close() }()
	if err := tx.Write("new/file.txt", []byte("hello"), 0o644, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.Write("existing.txt", []byte("new"), 0o644, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.Write("veracity.lock.json", []byte("{}"), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if v, _ := read(t, root, "new/file.txt"); v != "hello" {
		t.Fatalf("new file = %q", v)
	}
	if v, _ := read(t, root, "existing.txt"); v != "new" {
		t.Fatalf("overwrite = %q", v)
	}
	// txn dir cleaned up
	if _, err := os.Stat(filepath.Join(root, ".veracity/txn/current")); !os.IsNotExist(err) {
		t.Fatal("txn dir should be removed after commit")
	}
}

func TestFaultRepairRollsBackAndForward(t *testing.T) {
	// 3 ops: create, overwrite, lock(commit). Injecting a crash after each op
	// count exercises both outcomes: faults before the commit op is applied roll
	// back to the pre-transaction state; a fault after all ops (commit reached)
	// finalizes to the committed state.
	total := 3
	for fault := 1; fault <= total; fault++ {
		fault := fault
		t.Run("faultAfter"+string(rune('0'+fault)), func(t *testing.T) {
			root := t.TempDir()
			seed(t, root, "existing.txt", "ORIGINAL")

			tx, err := Begin(root)
			if err != nil {
				t.Fatal(err)
			}
			_ = tx.Write("created.txt", []byte("X"), 0o644, false)
			_ = tx.Write("existing.txt", []byte("MUTATED"), 0o644, false)
			_ = tx.Write("veracity.lock.json", []byte("{lock}"), 0o644, true)
			tx.FaultAfter = fault
			if err := tx.Commit(); err != errFault {
				t.Fatalf("expected injected fault, got %v", err)
			}
			_ = tx.Close()

			if err := Repair(root); err != nil {
				t.Fatalf("repair: %v", err)
			}

			committed := fault == total // commit op (index 2) was applied
			if committed {
				if v, _ := read(t, root, "existing.txt"); v != "MUTATED" {
					t.Fatalf("committed: existing.txt = %q, want MUTATED", v)
				}
				if _, ok := read(t, root, "created.txt"); !ok {
					t.Fatal("committed: created.txt should exist")
				}
				if _, ok := read(t, root, "veracity.lock.json"); !ok {
					t.Fatal("committed: lock should exist")
				}
			} else {
				if v, _ := read(t, root, "existing.txt"); v != "ORIGINAL" {
					t.Fatalf("rollback: existing.txt = %q, want ORIGINAL", v)
				}
				if _, ok := read(t, root, "created.txt"); ok {
					t.Fatal("rollback: created.txt should be gone")
				}
				if _, ok := read(t, root, "veracity.lock.json"); ok {
					t.Fatal("rollback: lock should not exist")
				}
			}
			if _, err := os.Stat(filepath.Join(root, ".veracity/txn/current")); !os.IsNotExist(err) {
				t.Fatalf("fault %d: txn dir should be cleaned after repair", fault)
			}
		})
	}
}

func TestTOCTOUConflict(t *testing.T) {
	root := t.TempDir()
	seed(t, root, "f.txt", "planned")

	tx, err := Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Close() }()
	if err := tx.Write("f.txt", []byte("update"), 0o644, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.Write("veracity.lock.json", []byte("{}"), 0o644, true); err != nil {
		t.Fatal(err)
	}
	// Someone changes f.txt after planning but before Commit.
	seed(t, root, "f.txt", "CHANGED-EXTERNALLY")

	if err := tx.Commit(); err == nil {
		t.Fatal("expected conflict error")
	}
	// Rolled back: f.txt keeps the external change, lock not written.
	if v, _ := read(t, root, "f.txt"); v != "CHANGED-EXTERNALLY" {
		t.Fatalf("f.txt = %q, want external change preserved", v)
	}
	if _, ok := read(t, root, "veracity.lock.json"); ok {
		t.Fatal("lock should not be written on conflict")
	}
}

func TestMoveAndRollback(t *testing.T) {
	root := t.TempDir()
	seed(t, root, "projects/api/main.go", "package main")

	tx, err := Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	tx.Move("projects/api", "archived/api")
	_ = tx.Write("veracity.lock.json", []byte("{}"), 0o644, true)
	tx.FaultAfter = 1 // crash right after the move, before commit
	if err := tx.Commit(); err != errFault {
		t.Fatalf("expected fault, got %v", err)
	}
	_ = tx.Close()

	if err := Repair(root); err != nil {
		t.Fatal(err)
	}
	// Move must be reversed.
	if v, _ := read(t, root, "projects/api/main.go"); v != "package main" {
		t.Fatalf("move not rolled back: %q", v)
	}
	if _, err := os.Stat(filepath.Join(root, "archived/api")); !os.IsNotExist(err) {
		t.Fatal("archived dir should be gone after rollback")
	}
}

func TestLockPreventsConcurrent(t *testing.T) {
	root := t.TempDir()
	tx1, err := Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Close() }()
	if _, err := Begin(root); err == nil {
		t.Fatal("expected second Begin to fail while lock held")
	}
}
