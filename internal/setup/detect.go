package setup

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/olesho/harness/internal/lockfile"
)

// detectGoModulePath reads the module path from an existing go.mod in dir.
func detectGoModulePath(dir string) (string, bool) {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), true
		}
	}
	return "", false
}

// adoptExisting adjusts a resolved lock for adoption of a pre-existing project:
// it fills each Go project's module path from the on-disk go.mod so the lock
// matches reality (and verify passes) rather than a templated default.
func adoptExisting(root string, lock *lockfile.Lock) error {
	for i := range lock.Projects {
		p := &lock.Projects[i]
		if p.Language != lockfile.LangGo {
			continue
		}
		dir := projectDir(root, lock, *p)
		if mp, ok := detectGoModulePath(dir); ok {
			p.ModulePath = mp
		}
	}
	return lock.Validate()
}
