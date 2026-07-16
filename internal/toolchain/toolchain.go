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
)

// Phases is the canonical phase order.
var Phases = []Phase{PhaseFormat, PhaseFileLint, PhaseProjectLint, PhaseTest}

// Command is one invocation. Argv is the base command; the executor sets cwd to
// the resolved project root and, for AppendFiles, appends the file list.
type Command struct {
	Argv         []string
	Files        FilesMode
	FailOnStdout bool // nonempty stdout means failure (gofmt -l always exits 0)
}

// Analyzer is an external tool not pinned by a project's own lockfile, acquired
// and version-pinned by the harness (checksum-verified cache).
type Analyzer struct {
	Name    string
	Version string
	// Checksums maps GOOS/GOARCH (e.g. "darwin/arm64") to a sha256 hex digest.
	// Populated at release time; empty in dev builds (verification is skipped
	// and a matching PATH binary may be used instead).
	Checksums map[string]string
}

type langSpec struct {
	phases    map[Phase][]Command
	analyzers []Analyzer
}

func cmd(files FilesMode, failOnStdout bool, argv ...string) Command {
	return Command{Argv: argv, Files: files, FailOnStdout: failOnStdout}
}

var specs = map[string]langSpec{
	lockfile.LangGo: {
		phases: map[Phase][]Command{
			PhaseFormat:   {cmd(AppendFiles, false, "gofmt", "-w")},
			PhaseFileLint: {cmd(AppendFiles, true, "gofmt", "-l")},
			PhaseProjectLint: {
				cmd(AppendFiles, true, "gofmt", "-l"),
				cmd(NoFiles, false, "go", "vet", "./..."),
				cmd(NoFiles, false, "golangci-lint", "run", "./..."),
			},
			PhaseTest: {cmd(NoFiles, false, "go", "test", "./...")},
		},
		analyzers: []Analyzer{{Name: "golangci-lint", Version: "2.12.2"}},
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
	for _, ph := range Phases {
		cmds := spec.phases[ph]
		list := make([]commandJSON, 0, len(cmds))
		for _, c := range cmds {
			list = append(list, commandJSON{Argv: c.Argv, Files: string(c.Files), FailOnStdout: c.FailOnStdout})
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
