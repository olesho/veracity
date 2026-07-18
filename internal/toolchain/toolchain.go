// Package toolchain is the single authority for the exact commands the harness
// runs per language and phase. No shell script or other package hard-codes tool
// invocations; they all resolve commands here (directly, or via the
// `harness toolchain --language <l> --json` machine interface), so lint/format/
// test rules can never disagree between the agent hooks, the git-hook delegates,
// and CI.
package toolchain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/olesho/harness/internal/lockfile"
)

// FilesMode describes how a command receives the file set the harness computed.
type FilesMode string

const (
	// NoFiles runs the command once from the project root with no file args
	// (project/package-oriented analyzers: go vet ./..., mypy ., tsc --noEmit).
	NoFiles FilesMode = "none"
	// AppendFiles appends the harness-computed, project-root-relative file list
	// (file-oriented tools: gofmt, ruff, eslint, prettier). The harness owns
	// discovery so the tool's own directory-walk semantics are never relied on.
	AppendFiles FilesMode = "append"
)

// Phase groups commands by when/where they run.
type Phase string

const (
	PhaseFormat      Phase = "format"       // rewrite in place (harness fmt --fix)
	PhaseFileLint    Phase = "file_lint"    // fast per-file checks (tier 1 post-edit)
	PhaseProjectLint Phase = "project_lint" // full bundle (tiers 2-4)
	PhaseTest        Phase = "test"         // the verifier
	// Go-only quality-verifier phases (each gated by its own per-project feature;
	// not part of the global lifecycle invariant below).
	PhaseGofumpt  Phase = "gofumpt"  // gofumpt -l (stricter format check)
	PhaseGci      Phase = "gci"      // gci list (import ordering check)
	PhaseModTidy  Phase = "mod_tidy" // go mod tidy -diff (module hygiene)
	PhaseCoverage Phase = "coverage" // coverage run → threshold gate (Go/TS)
	PhaseAudit    Phase = "audit"    // pnpm audit (TS dependency-vulnerability gate)
)

// Phases is the canonical, language-independent lifecycle order that every
// language must implement (see TestEveryLanguageHasEveryPhase). Language-specific
// verifier phases are declared per language and surfaced via AllPhases.
var Phases = []Phase{PhaseFormat, PhaseFileLint, PhaseProjectLint, PhaseTest}

// verifierPhases are the optional, language-specific verifier phases appended
// after the global lifecycle phases for each language's machine interface and
// phase iteration. Each is gated by its own per-project feature in the runner.
var verifierPhases = map[string][]Phase{
	lockfile.LangGo: {PhaseGofumpt, PhaseGci, PhaseModTidy, PhaseCoverage},
	lockfile.LangTS: {PhaseCoverage, PhaseAudit},
}

// AllPhases returns the global lifecycle phases plus any language-specific
// verifier phases, in deterministic order.
func AllPhases(lang string) []Phase {
	out := append([]Phase{}, Phases...)
	out = append(out, verifierPhases[lang]...)
	return out
}

// Command is one invocation. Argv is the base command; the executor sets cwd to
// the resolved project root and, for AppendFiles, appends the file list.
type Command struct {
	Argv         []string
	Files        FilesMode
	FailOnStdout bool   // nonempty stdout means failure (gofmt -l always exits 0)
	Gate         string // "" = always; else the feature JSON key that must be enabled
}

// Analyzer is an external tool not declared in a project's own go.mod, acquired
// and version-pinned by the harness. `harness install-tools` (internal/analyzers)
// `go install`s Module@vVersion — source integrity comes from the Go module
// checksum database — into a per-version cache, and records the produced binary's
// sha256 so the runner can detect a corrupted/tampered cache before executing the
// resolved absolute path. Checksums holds upstream release-binary digests keyed by
// GOOS/GOARCH; it is reserved for a future release-binary download path and is
// empty today (dev builds may fall back to a version-verified PATH binary only
// when HARNESS_ANALYZERS_DEV=1).
type Analyzer struct {
	Name      string
	Module    string // go-install path, e.g. "mvdan.cc/gofumpt"
	Version   string // pinned version without the leading "v", e.g. "0.8.0"
	Feature   string // the feature JSON key that requires it ("lint" for golangci-lint)
	Checksums map[string]string
}

type langSpec struct {
	phases    map[Phase][]Command
	analyzers []Analyzer
}

func cmd(files FilesMode, failOnStdout bool, argv ...string) Command {
	return Command{Argv: argv, Files: files, FailOnStdout: failOnStdout}
}

// gate returns a copy of c that only runs when the named feature is enabled.
func (c Command) gate(feature string) Command { c.Gate = feature; return c }

// CoverProfilePlaceholder is substituted with an exclusive temp profile path by
// runner.Coverage before executing the coverage command.
const CoverProfilePlaceholder = "$COVERPROFILE"

var specs = map[string]langSpec{
	lockfile.LangGo: {
		phases: map[Phase][]Command{
			// Format order gofmt → gci → gofumpt: gofumpt (strictest) runs last so
			// the result is idempotent. gci and gofumpt only apply when enabled.
			PhaseFormat: {
				cmd(AppendFiles, false, "gofmt", "-w"),
				cmd(AppendFiles, false, "gci", "write").gate("gci"),
				cmd(AppendFiles, false, "gofumpt", "-w").gate("gofumpt"),
			},
			PhaseFileLint: {cmd(AppendFiles, true, "gofmt", "-l")},
			PhaseProjectLint: {
				cmd(AppendFiles, true, "gofmt", "-l"),
				cmd(NoFiles, false, "go", "vet", "./..."),
				cmd(NoFiles, false, "golangci-lint", "run", "./..."),
			},
			PhaseTest: {cmd(NoFiles, false, "go", "test", "./...")},
			// Go-only verifier phases (gated by their own features in the runner).
			PhaseGofumpt: {cmd(AppendFiles, true, "gofumpt", "-l")},
			PhaseGci:     {cmd(AppendFiles, true, "gci", "list")},
			PhaseModTidy: {cmd(NoFiles, false, "go", "mod", "tidy", "-diff")},
			// Consumed only by runner.Coverage, which substitutes the placeholder
			// with an exclusive temp profile and computes the percentage itself.
			PhaseCoverage: {cmd(NoFiles, false, "go", "test", "-coverprofile="+CoverProfilePlaceholder, "-covermode=atomic", "./...")},
		},
		analyzers: []Analyzer{
			{Name: "golangci-lint", Module: "github.com/golangci/golangci-lint/v2/cmd/golangci-lint", Version: "2.12.2", Feature: "lint"},
			{Name: "gofumpt", Module: "mvdan.cc/gofumpt", Version: "0.10.0", Feature: "gofumpt"},
			{Name: "gci", Module: "github.com/daixiang0/gci", Version: "0.14.0", Feature: "gci"},
		},
	},
	lockfile.LangPython: {
		// Run from the project root; `uv run` resolves the project's frozen env.
		phases: map[Phase][]Command{
			PhaseFormat: {cmd(AppendFiles, false, "uv", "run", "ruff", "format")},
			PhaseFileLint: {
				cmd(AppendFiles, false, "uv", "run", "ruff", "check"),
				cmd(AppendFiles, false, "uv", "run", "ruff", "format", "--check"),
			},
			PhaseProjectLint: {
				cmd(NoFiles, false, "uv", "run", "ruff", "check", "."),
				cmd(NoFiles, false, "uv", "run", "ruff", "format", "--check", "."),
				cmd(NoFiles, false, "uv", "run", "mypy", "."),
			},
			PhaseTest: {cmd(NoFiles, false, "uv", "run", "pytest")},
		},
	},
	lockfile.LangTS: {
		// Run from the project root; `pnpm exec` uses the project's node_modules.
		phases: map[Phase][]Command{
			PhaseFormat: {cmd(AppendFiles, false, "pnpm", "exec", "prettier", "--write")},
			PhaseFileLint: {
				cmd(AppendFiles, false, "pnpm", "exec", "eslint"),
				cmd(AppendFiles, false, "pnpm", "exec", "prettier", "--check"),
			},
			PhaseProjectLint: {
				cmd(NoFiles, false, "pnpm", "exec", "eslint", "."),
				cmd(NoFiles, false, "pnpm", "exec", "prettier", "--check", "."),
				cmd(NoFiles, false, "pnpm", "exec", "tsc", "--noEmit"),
			},
			PhaseTest: {cmd(NoFiles, false, "pnpm", "exec", "vitest", "run")},
			// TS verifier phases (gated by their own features in the runner).
			// runner.Coverage builds this argv and appends the vitest threshold flag.
			PhaseCoverage: {cmd(NoFiles, false, "pnpm", "exec", "vitest", "run", "--coverage")},
			// --prod audits only production dependencies: a scaffold (dev-deps only)
			// passes clean, while runtime deps the user adds are gated. Dev-tooling
			// advisories (test/build tools) churn constantly and aren't shipped.
			PhaseAudit: {cmd(NoFiles, false, "pnpm", "audit", "--prod", "--audit-level", "high")},
		},
	},
}

// Languages returns the supported languages in a stable order.
func Languages() []string {
	out := make([]string, 0, len(specs))
	for l := range specs {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Commands returns the commands for a language/phase (nil if none).
func Commands(lang string, phase Phase) []Command {
	return specs[lang].phases[phase]
}

// Analyzers returns the harness-managed (non-lockfile) analyzers for a language.
func Analyzers(lang string) []Analyzer {
	return specs[lang].analyzers
}

// FileExtensions returns the source extensions that identify a language's files
// for discovery/scoping.
func FileExtensions(lang string) []string {
	switch lang {
	case lockfile.LangGo:
		return []string{".go"}
	case lockfile.LangPython:
		return []string{".py"}
	case lockfile.LangTS:
		return []string{".ts", ".tsx"}
	default:
		return nil
	}
}

// --- JSON machine interface (`harness toolchain --language <l> --json`) ---

type commandJSON struct {
	Argv         []string `json:"argv"`
	Files        string   `json:"files"`
	FailOnStdout bool     `json:"failOnStdout"`
	Gate         string   `json:"gate,omitempty"`
}

type langJSON struct {
	Language  string                   `json:"language"`
	Phases    map[string][]commandJSON `json:"phases"`
	Analyzers []Analyzer               `json:"analyzers"`
}

// JSON renders a language's full command table as deterministic JSON. Map keys
// are sorted by encoding/json, and slices preserve declaration order, so equal
// inputs serialize identically.
func JSON(lang string) ([]byte, error) {
	spec, ok := specs[lang]
	if !ok {
		return nil, fmt.Errorf("unsupported language %q", lang)
	}
	view := langJSON{Language: lang, Phases: map[string][]commandJSON{}, Analyzers: spec.analyzers}
	if view.Analyzers == nil {
		view.Analyzers = []Analyzer{}
	}
	for _, ph := range AllPhases(lang) {
		cmds := spec.phases[ph]
		list := make([]commandJSON, 0, len(cmds))
		for _, c := range cmds {
			list = append(list, commandJSON{Argv: c.Argv, Files: string(c.Files), FailOnStdout: c.FailOnStdout, Gate: c.Gate})
		}
		view.Phases[string(ph)] = list
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
