//go:build unix

package txn

import (
	"fmt"
	"os"
	"syscall"
)

type fileLock struct{ f *os.File }

// acquireLock takes an exclusive, non-blocking advisory lock on path. The lock
// is released automatically if the process dies, so a crash never leaves a
// stale lock that blocks the next run.
func acquireLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another veracity operation is in progress (%s is locked)", path)
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
