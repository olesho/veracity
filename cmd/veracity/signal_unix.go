//go:build unix

package main

import (
	"os/signal"
	"syscall"
)

// ignoreSIGPIPE sets SIGPIPE to be ignored so that a broken stdout/stderr pipe
// (every read end closed) fails the offending write with EPIPE instead of
// terminating the process. Go's default disposition re-raises SIGPIPE for writes
// to fd 1 or 2, which killed the pre-push gate (exit 141) after its verifier
// output outran a closed reader; ignoring it lets the gate finish and return its
// real pass/fail code. See https://pkg.go.dev/os/signal#hdr-SIGPIPE.
func ignoreSIGPIPE() {
	signal.Ignore(syscall.SIGPIPE)
}
