//go:build !unix

package txn

import "os"

// On non-unix platforms we fall back to a best-effort lock file handle without
// OS-level advisory locking. The veracity targets macOS/Linux; this stub keeps
// the package buildable elsewhere.
type fileLock struct{ f *os.File }

func acquireLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}
