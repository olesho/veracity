// Package gitq wraps the git plumbing the veracity needs, always using
// NUL-delimited output so filenames with spaces, newlines, quotes, renames, or
// leading dashes are handled correctly (never shell-word-split or misparsed).
package gitq

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a git repository rooted at Root (an absolute path).
type Repo struct {
	Root string
}

// Root resolves the git top-level directory containing dir. It returns an error
// if dir is not inside a git work tree.
func Root(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", errors.New("not a git repository")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// Open returns a Repo for the git work tree containing dir.
func Open(dir string) (*Repo, error) {
	root, err := Root(dir)
	if err != nil {
		return nil, err
	}
	return &Repo{Root: root}, nil
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	_, err := Root(dir)
	return err == nil
}

// HasCommits reports whether HEAD resolves (false for an unborn repo with no
// commits yet).
func (r *Repo) HasCommits() bool {
	_, err := run(r.Root, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// HeadRef returns the current HEAD commit hash, or "" for an unborn repo.
func (r *Repo) HeadRef() string {
	out, err := run(r.Root, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TrackedFiles lists all tracked files (repo-relative, slash paths).
func (r *Repo) TrackedFiles() ([]string, error) {
	out, err := run(r.Root, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	return splitZ(out), nil
}

// UntrackedFiles lists untracked, non-ignored files (repo-relative).
func (r *Repo) UntrackedFiles() ([]string, error) {
	out, err := run(r.Root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitZ(out), nil
}

// StagedFiles lists files staged for commit (added/copied/modified/renamed),
// repo-relative. Deletions are omitted (nothing to lint).
func (r *Repo) StagedFiles() ([]string, error) {
	out, err := run(r.Root, "diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR")
	if err != nil {
		return nil, err
	}
	return splitZ(out), nil
}

// ChangedSince lists files that differ from the given ref plus untracked files,
// repo-relative. If ref is empty or the repo is unborn, it returns all tracked
// plus untracked files (treating everything as changed).
func (r *Repo) ChangedSince(ref string) ([]string, error) {
	set := map[string]struct{}{}
	if ref == "" || !r.HasCommits() {
		tracked, err := r.TrackedFiles()
		if err != nil {
			return nil, err
		}
		for _, f := range tracked {
			set[f] = struct{}{}
		}
	} else {
		out, err := run(r.Root, "diff", "--name-only", "-z", "--diff-filter=ACMR", ref)
		if err != nil {
			return nil, err
		}
		for _, f := range splitZ(out) {
			set[f] = struct{}{}
		}
	}
	untracked, err := r.UntrackedFiles()
	if err != nil {
		return nil, err
	}
	for _, f := range untracked {
		set[f] = struct{}{}
	}
	return sortedKeys(set), nil
}

// WorkingTreeChanges lists every file that differs from HEAD: staged, unstaged,
// and untracked, plus renames and deletions' new paths. Uses porcelain v1 with
// NUL records so unusual filenames are safe.
func (r *Repo) WorkingTreeChanges() ([]string, error) {
	out, err := run(r.Root, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parsePorcelainZ(out), nil
}

func run(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, errors.New("git " + strings.Join(args, " ") + ": " + msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// splitZ splits NUL-delimited git output into records, dropping the trailing
// empty record.
func splitZ(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		out = append(out, string(p))
	}
	return out
}

// parsePorcelainZ parses `git status --porcelain -z` records. Each record is
// "XY <path>"; a rename/copy record ("R"/"C") is followed by a separate NUL
// record holding the original path, which we skip (we want the new path).
func parsePorcelainZ(b []byte) []string {
	records := bytes.Split(b, []byte{0})
	var out []string
	skipNext := false
	for _, rec := range records {
		if len(rec) == 0 {
			continue
		}
		if skipNext {
			skipNext = false
			continue
		}
		if len(rec) < 4 {
			continue
		}
		status := rec[:2]
		path := string(rec[3:])
		if status[0] == 'R' || status[0] == 'C' {
			// The next NUL record is the original path; skip it.
			skipNext = true
		}
		out = append(out, path)
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// stable, deterministic
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
