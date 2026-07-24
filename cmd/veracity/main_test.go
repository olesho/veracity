//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// TestBrokenStdoutPipeDoesNotSIGPIPE is the regression guard for the pre-push
// gate dying with exit 141: veracity must survive a stdout whose readers have
// all closed. We build the binary, then run `veracity version` with no reader on
// its stdout pipe, so the child's first write to fd 1 hits EPIPE — which, with
// SIGPIPE ignored, the process shrugs off and exits 0 instead of being killed.
func TestBrokenStdoutPipeDoesNotSIGPIPE(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary; skipped under -short")
	}
	bin := filepath.Join(t.TempDir(), "veracity")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	// A pipe with both ends closed in the parent before Start: the child inherits
	// only the write end as fd 1, so with zero readers its write EPIPEs at once —
	// no dependence on filling the pipe buffer, hence no timing race.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close read end: %v", err)
	}

	cmd := exec.Command(bin, "version")
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = w.Close() // drop the parent's write end; child keeps its own copy

	err = cmd.Wait()

	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGPIPE {
			t.Fatalf("veracity was killed by SIGPIPE on a broken stdout pipe (the exit-141 regression)")
		}
		t.Fatalf("veracity exited nonzero on a broken pipe: %v (exit %d)", err, ee.ExitCode())
	}
	if err != nil {
		t.Fatalf("unexpected error running veracity: %v", err)
	}
}
