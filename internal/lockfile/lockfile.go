// Package lockfile defines and (de)serializes veracity.lock.json, the committed
// registry that records a managed repo's layout, opt-in capabilities, and per
// project features. It is the single source of truth every other package reads;
// it stores no derived data (paths, tool commands) and no timestamps, so equal
// inputs always produce byte-identical output.
package lockfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileName is the committed lock file at the managed repo root.
const FileName = "veracity.lock.json"

// SchemaVersion is the current lock schema. New optional fields are additive
// (omitempty; absent keys decode to zero on a newer binary), so they do not bump
// the version. Forward compatibility is provided by the `.veracity-version` binary
// pin — a managed repo always runs its pinned veracity — so an older binary reading
// a newer lock (which DisallowUnknownFields rejects) is out of the compatibility
// contract; the fix is to update veracity. `veracity migrate` bumps a project
// forward when a breaking schema change does require a version bump.
const SchemaVersion = 1

// Layout selects the repo shape.
const (
	LayoutSingle   = "single"   // exactly one project, rooted at "."
	LayoutMonorepo = "monorepo" // N projects under projects/<name>
)

// Supported languages.
const (
	LangGo     = "go"
	LangPython = "python"
	LangTS     = "typescript"
)

// Supported agents (repo-level capability).
const (
	AgentClaude = "claude"
	AgentCodex  = "codex"
)

// ErrNotFound is returned by Load when no lock file exists, signaling an
// uninitialized directory (where `veracity setup` is allowed to run).
var ErrNotFound = errors.New("veracity.lock.json not found")

// Lock is the top-level committed document.
type Lock struct {
	SchemaVersion int          `json:"schemaVersion"`
	Layout        string       `json:"layout"`
	Capabilities  Capabilities `json:"capabilities"`
	Projects      []Project    `json:"projects"`
}

// Capabilities are repo-level opt-in features. A false/empty capability means
// the corresponding files are never generated (lightweight by construction).
type Capabilities struct {
	Agents    []string `json:"agents"`    // subset of {claude, codex}; empty = no agent wiring
	GitHooks  bool     `json:"gitHooks"`  // native .git/hooks delegates
	CI        bool     `json:"ci"`        // .github/workflows/ci.yml + docs/ci-setup.md
	AgentDocs bool     `json:"agentDocs"` // CLAUDE.md / AGENTS.md
	Skills    bool     `json:"skills"`    // project-local post-setup skills
}

// Project is one managed subproject (or the sole project in single layout).
type Project struct {
	Name       string   `json:"name"`
	Language   string   `json:"language"`
	ModulePath string   `json:"modulePath,omitempty"` // Go only
	Features   Features `json:"features"`
	// CoverageMin is the minimum total test coverage percent the coverage gate
	// enforces (Go only; 0 = measure-and-report, never fail). Inert unless
	// Features.Coverage is set.
	CoverageMin int `json:"coverageMin,omitempty"`
}

// Features are per-project opt-ins. The universal four (lint/test/markdown/
// diagrams) are always serialized; the Go-only verifier flags use omitempty so
// non-Go and default locks stay byte-identical to pre-verifier locks.
type Features struct {
	Lint     bool `json:"lint"`
	Test     bool `json:"test"`
	Markdown bool `json:"markdown"`
	Diagrams bool `json:"diagrams"`
	// Go-only quality verifiers (independent per-project toggles).
	Gofumpt bool `json:"gofumpt,omitempty"` // stricter formatter (superset of gofmt)
	Gci     bool `json:"gci,omitempty"`     // deterministic import section ordering
	ModTidy bool `json:"modTidy,omitempty"` // `go mod tidy -diff` hygiene check
	// Coverage is a total-coverage gate (see Project.CoverageMin); valid for Go and
	// TypeScript.
	Coverage bool `json:"coverage,omitempty"`
	// TypeScript-only verifiers.
	Audit bool `json:"audit,omitempty"` // `pnpm audit` dependency-vulnerability gate
	// Language-agnostic verifiers.
	Semgrep bool `json:"semgrep,omitempty"` // Semgrep SAST scan (Docker; soft-skips when unavailable)
	Sonar   bool `json:"sonar,omitempty"`   // SonarQube scan (self-hosted; soft-skips when unreachable)
}

// featureLanguages restricts a feature to specific languages. A feature absent
// from this map is valid for every language (e.g. lint, test, semgrep, sonar).
// It powers Validate's per-project language guard.
var featureLanguages = map[string][]string{
	"gofumpt":  {LangGo},
	"gci":      {LangGo},
	"modTidy":  {LangGo},
	"coverage": {LangGo, LangTS},
	"audit":    {LangTS},
}

// restrictedFeatureKeys is the deterministically-ordered set of language-restricted
// feature keys, so Validate reports the same error for equal inputs (map iteration
// order is not stable).
var restrictedFeatureKeys = []string{"gofumpt", "gci", "modTidy", "coverage", "audit"}

// featureAllowed reports whether a feature (by JSON key) may be enabled on the
// given language. Unlisted features are language-agnostic.
func featureAllowed(feature, lang string) bool {
	langs, restricted := featureLanguages[feature]
	if !restricted {
		return true
	}
	for _, l := range langs {
		if l == lang {
			return true
		}
	}
	return false
}

// Enabled reports whether the named feature (by its JSON key) is on. It powers
// the per-command Gate filter in the runner.
func (f Features) Enabled(name string) bool {
	switch name {
	case "lint":
		return f.Lint
	case "test":
		return f.Test
	case "markdown":
		return f.Markdown
	case "diagrams":
		return f.Diagrams
	case "gofumpt":
		return f.Gofumpt
	case "gci":
		return f.Gci
	case "modTidy":
		return f.ModTidy
	case "coverage":
		return f.Coverage
	case "audit":
		return f.Audit
	case "semgrep":
		return f.Semgrep
	case "sonar":
		return f.Sonar
	default:
		return false
	}
}

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// modulePathElemRe validates a single path element of a Go module path:
	// ASCII letters/digits and a conservative set of punctuation, never a
	// dot-only element (blocks "." and ".." traversal).
	modulePathElemRe = regexp.MustCompile(`^[A-Za-z0-9]+(?:[-._~+][A-Za-z0-9]+)*$`)
)

const maxNameLen = 64

// ValidateName checks a project name. Names are lowercase kebab identifiers;
// the regex alone rejects path separators, dots, whitespace, and shell
// metacharacters, so it doubles as traversal protection.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("project name is empty")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("project name %q exceeds %d characters", name, maxNameLen)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid project name %q: must match %s", name, nameRe.String())
	}
	return nil
}

// ValidateModulePath checks a Go module path. It deliberately does not reuse the
// project-name regex (that would reject valid values like "example.com/myproj").
// It enforces non-empty slash-separated elements, rejects leading/trailing
// slashes, dot-only elements ("." / ".."), and control/space characters.
func ValidateModulePath(p string) error {
	if p == "" {
		return errors.New("module path is empty")
	}
	if strings.ContainsAny(p, " \t\r\n") {
		return fmt.Errorf("invalid module path %q: contains whitespace", p)
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return fmt.Errorf("invalid module path %q: leading/trailing slash", p)
	}
	elems := strings.Split(p, "/")
	for _, e := range elems {
		if e == "" {
			return fmt.Errorf("invalid module path %q: empty element", p)
		}
		if e == "." || e == ".." {
			return fmt.Errorf("invalid module path %q: dot element (traversal)", p)
		}
		if !modulePathElemRe.MatchString(e) {
			return fmt.Errorf("invalid module path %q: bad element %q", p, e)
		}
	}
	return nil
}

// ValidateLanguage checks the language enum.
func ValidateLanguage(lang string) error {
	switch lang {
	case LangGo, LangPython, LangTS:
		return nil
	default:
		return fmt.Errorf("unsupported language %q (want go|python|typescript)", lang)
	}
}

// DerivePath returns a project's repo-relative path, which is never stored in
// the lock: "." for single layout, "projects/<name>" for monorepo.
func DerivePath(layout, name string) string {
	if layout == LayoutSingle {
		return "."
	}
	return filepath.ToSlash(filepath.Join("projects", name))
}

// Path returns this project's repo-relative path for the given layout.
func (p Project) Path(layout string) string { return DerivePath(layout, p.Name) }

// Find returns the project with the given name, or false.
func (l *Lock) Find(name string) (Project, bool) {
	for _, p := range l.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// Validate checks structural invariants of the whole lock.
func (l *Lock) Validate() error {
	if l.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schemaVersion %d (want %d)", l.SchemaVersion, SchemaVersion)
	}
	switch l.Layout {
	case LayoutSingle, LayoutMonorepo:
	default:
		return fmt.Errorf("invalid layout %q (want single|monorepo)", l.Layout)
	}
	for _, a := range l.Capabilities.Agents {
		if a != AgentClaude && a != AgentCodex {
			return fmt.Errorf("invalid agent %q (want claude|codex)", a)
		}
	}
	if len(l.Projects) == 0 {
		return errors.New("lock has no projects")
	}
	if l.Layout == LayoutSingle && len(l.Projects) != 1 {
		return fmt.Errorf("single layout requires exactly 1 project, found %d", len(l.Projects))
	}
	seen := map[string]bool{}
	for _, p := range l.Projects {
		if err := ValidateName(p.Name); err != nil {
			return err
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate project name %q", p.Name)
		}
		seen[p.Name] = true
		if err := ValidateLanguage(p.Language); err != nil {
			return fmt.Errorf("project %q: %w", p.Name, err)
		}
		if p.Language == LangGo {
			if err := ValidateModulePath(p.ModulePath); err != nil {
				return fmt.Errorf("project %q: %w", p.Name, err)
			}
		} else if p.ModulePath != "" {
			return fmt.Errorf("project %q: modulePath is only valid for go projects", p.Name)
		}
		// Invariant: diagrams require markdown.
		if p.Features.Diagrams && !p.Features.Markdown {
			return fmt.Errorf("project %q: features.diagrams requires features.markdown", p.Name)
		}
		// Language-restricted verifier features are invalid on other languages.
		for _, name := range restrictedFeatureKeys {
			if p.Features.Enabled(name) && !featureAllowed(name, p.Language) {
				return fmt.Errorf("project %q: features.%s is not valid for %s projects (want %s)",
					p.Name, name, p.Language, strings.Join(featureLanguages[name], "|"))
			}
		}
		// coverageMin only applies where the coverage gate is valid.
		if p.CoverageMin != 0 && !featureAllowed("coverage", p.Language) {
			return fmt.Errorf("project %q: coverageMin is not valid for %s projects", p.Name, p.Language)
		}
		if p.CoverageMin < 0 || p.CoverageMin > 100 {
			return fmt.Errorf("project %q: coverageMin %d out of range (want 0-100)", p.Name, p.CoverageMin)
		}
	}
	return nil
}

// Marshal renders the lock as deterministic, indented JSON with a trailing
// newline. Struct field order is fixed and the schema contains no maps, so
// equal locks always serialize identically.
func Marshal(l *Lock) ([]byte, error) {
	if l.Capabilities.Agents == nil {
		l.Capabilities.Agents = []string{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(l); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal parses lock bytes and validates them.
func Unmarshal(data []byte) (*Lock, error) {
	var l Lock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", FileName, err)
	}
	if l.Capabilities.Agents == nil {
		l.Capabilities.Agents = []string{}
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return &l, nil
}

// Load reads and validates the lock from a repo root. It returns ErrNotFound
// when the file is absent (an uninitialized directory).
func Load(root string) (*Lock, error) {
	data, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return Unmarshal(data)
}

// Exists reports whether a lock file is present at the repo root.
func Exists(root string) bool {
	_, err := os.Stat(filepath.Join(root, FileName))
	return err == nil
}
