// Package cli implements harness subcommand dispatch. main is a thin wrapper
// around Run so the CLI surface is testable without spawning a process.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/olesho/harness/internal/analyzers"
	"github.com/olesho/harness/internal/gitq"
	"github.com/olesho/harness/internal/hook"
	"github.com/olesho/harness/internal/lockfile"
	"github.com/olesho/harness/internal/runner"
	"github.com/olesho/harness/internal/setup"
	"github.com/olesho/harness/internal/setup/txn"
	"github.com/olesho/harness/internal/version"
)

// Run dispatches a harness invocation and returns a process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version.Version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	case "doctor":
		return cmdDoctor(stdout)
	case "setup":
		return cmdSetup(rest, stdin, stdout, stderr)
	case "bootstrap":
		return cmdBootstrap(stdout, stderr)
	case "add":
		return cmdAdd(rest, stdin, stdout, stderr)
	case "edit":
		return cmdEdit(rest, stdout, stderr)
	case "reconfigure":
		return cmdReconfigure(rest, stdout, stderr)
	case "remove":
		return cmdRemove(rest, stdout, stderr)
	case "verify":
		return cmdVerify(stdout, stderr)
	case "list":
		return cmdList(rest, stdout, stderr)
	case "lock-query":
		return cmdLockQuery(rest, stdout, stderr)
	case "toolchain":
		return cmdToolchain(rest, stdout, stderr)
	case "lint":
		return cmdRun(rest, stdout, stderr, "lint")
	case "test":
		return cmdRun(rest, stdout, stderr, "test")
	case "fmt", "format":
		return cmdRun(rest, stdout, stderr, "fmt")
	case "hook":
		return cmdHook(rest, stdin, stdout, stderr)
	case "repair":
		return cmdRepair(stdout, stderr)
	case "install-skills":
		return cmdInstallSkills(stdout, stderr)
	case "install-tools":
		return cmdInstallTools(stdout, stderr)
	case "ci":
		return cmdCI(stdout, stderr)
	case "docs":
		return cmdDocs(rest, stdin, stdout, stderr)
	case "upgrade", "migrate":
		fmt.Fprintf(stderr, "harness %s: not yet implemented in this build\n", cmd)
		return 1
	default:
		fmt.Fprintf(stderr, "harness: unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

// resolveRoot returns the managed project root for the current directory. It
// prefers the nearest ancestor containing harness.lock.json (so a managed
// project nested inside another git repo resolves to itself, not the outer
// repo), then the git top-level, then the current directory.
func resolveRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if root := findLockRoot(cwd); root != "" {
		return root
	}
	if root, err := gitq.Root(cwd); err == nil {
		return root
	}
	return cwd
}

// findLockRoot walks up from dir to the nearest directory containing a lock
// file, or "" if none.
func findLockRoot(dir string) string {
	for {
		if lockfile.Exists(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func cmdDoctor(out io.Writer) int {
	fmt.Fprintf(out, "harness %s\n\n", version.Version)
	fmt.Fprintln(out, "toolchain:")
	type tool struct{ name, hint string }
	tools := []tool{
		{"go", "https://go.dev/dl/"},
		{"git", "https://git-scm.com/"},
		{"python3", "https://www.python.org/"},
		{"uv", "https://docs.astral.sh/uv/"},
		{"node", "https://nodejs.org/"},
		{"pnpm", "https://pnpm.io/"},
	}
	for _, t := range tools {
		if path, err := lookPath(t.name); err == nil {
			fmt.Fprintf(out, "  ok    %-14s %s\n", t.name, path)
		} else {
			fmt.Fprintf(out, "  MISS  %-14s install: %s\n", t.name, t.hint)
		}
	}
	// Harness-managed analyzers (golangci-lint/gofumpt/gci) live in a per-version
	// cache, not on PATH — report their cache status, not a LookPath probe.
	if lock, err := lockfile.Load(resolveRoot()); err == nil {
		st := analyzers.Statuses(lock)
		if len(st) > 0 {
			fmt.Fprintln(out, "\nanalyzers (harness-managed cache):")
			for _, s := range st {
				switch s.State {
				case analyzers.StateCached:
					fmt.Fprintf(out, "  ok    %-14s %s\n", s.Analyzer.Name, s.Path)
				case analyzers.StatePathDev:
					fmt.Fprintf(out, "  dev   %-14s %s (PATH; HARNESS_ANALYZERS_DEV)\n", s.Analyzer.Name, s.Path)
				case analyzers.StateCorrupt:
					fmt.Fprintf(out, "  BAD   %-14s cache corrupt; run `harness install-tools`\n", s.Analyzer.Name)
				default:
					fmt.Fprintf(out, "  MISS  %-14s run `harness install-tools`\n", s.Analyzer.Name)
				}
			}
		}
	}
	return 0
}

func cmdSetup(args []string, stdin io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(errw)
	config := fs.String("config", "", "path to a setup config JSON, or - for stdin")
	preset := fs.String("preset", "", "preset: minimal|standard|full (overridable by --config)")
	noGit := fs.Bool("no-git", false, "do not git init when the target is not a repo")
	adopt := fs.Bool("adopt", false, "proceed even if the directory has pre-existing files")
	printTmpl := fs.Bool("print-config-template", false, "print a config skeleton and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *printTmpl {
		fmt.Fprint(out, configTemplate)
		return 0
	}

	var in *setup.Input
	if *config != "" {
		data, err := readConfig(*config, stdin)
		if err != nil {
			fmt.Fprintf(errw, "harness setup: %v\n", err)
			return 1
		}
		parsed, err := setup.ParseInput(data)
		if err != nil {
			fmt.Fprintf(errw, "harness setup: %v\n", err)
			return 1
		}
		in = parsed
		if in.Preset == "" {
			in.Preset = *preset
		}
	} else {
		fmt.Fprintln(errw, "harness setup: --config is required (use - for stdin, or --print-config-template)")
		return 2
	}

	root, _ := os.Getwd()
	res, err := setup.Init(root, in, setup.Options{NoGit: *noGit, Adopt: *adopt})
	if err != nil {
		fmt.Fprintf(errw, "harness setup: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "setup complete: %d files created. Next: run `harness bootstrap`.\n", res.Created)
	reportConflicts(res, errw)
	return 0
}

func cmdBootstrap(out, errw io.Writer) int {
	root := resolveRoot()
	if err := setup.Bootstrap(root, out); err != nil {
		fmt.Fprintf(errw, "harness bootstrap: %v\n", err)
		return 1
	}
	return 0
}

func cmdAdd(args []string, stdin io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(errw)
	config := fs.String("config", "-", "path to a config JSON with the projects to add, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	data, err := readConfig(*config, stdin)
	if err != nil {
		fmt.Fprintf(errw, "harness add: %v\n", err)
		return 1
	}
	in, err := setup.ParseInput(data)
	if err != nil {
		fmt.Fprintf(errw, "harness add: %v\n", err)
		return 1
	}
	res, err := setup.Add(resolveRoot(), in)
	if err != nil {
		fmt.Fprintf(errw, "harness add: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "added: %d files created.\n", res.Created)
	reportConflicts(res, errw)
	return 0
}

func cmdEdit(args []string, out, errw io.Writer) int {
	// The project name is the first argument so that flags may follow it (Go's
	// flag package stops at the first positional, so we peel the name off first).
	if len(args) < 1 || isFlag(args[0]) {
		fmt.Fprintln(errw, "usage: harness edit <name> --confirm <name> [--lint on|off] [--test on|off] [--markdown on|off] [--diagrams on|off] [--gofumpt on|off] [--gci on|off] [--mod-tidy on|off] [--coverage on|off] [--coverage-min N]")
		return 2
	}
	name := args[0]
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(errw)
	confirm := fs.String("confirm", "", "must equal the project name")
	lint := fs.String("lint", "", "on|off")
	test := fs.String("test", "", "on|off")
	markdown := fs.String("markdown", "", "on|off")
	diagrams := fs.String("diagrams", "", "on|off")
	gofumpt := fs.String("gofumpt", "", "on|off (Go)")
	gci := fs.String("gci", "", "on|off (Go)")
	modTidy := fs.String("mod-tidy", "", "on|off (Go)")
	coverage := fs.String("coverage", "", "on|off (Go)")
	coverageMin := fs.Int("coverage-min", 0, "minimum total coverage percent 0-100 (Go)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	// --coverage-min uses a pointer so an explicit 0 differs from an omitted flag.
	var covMin *int
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "coverage-min" {
			v := *coverageMin
			covMin = &v
		}
	})
	edit := setup.EditInput{
		Features: setup.FeaturesInput{
			Lint: onOff(*lint), Test: onOff(*test), Markdown: onOff(*markdown), Diagrams: onOff(*diagrams),
			Gofumpt: onOff(*gofumpt), Gci: onOff(*gci), ModTidy: onOff(*modTidy), Coverage: onOff(*coverage),
		},
		CoverageMin: covMin,
	}
	res, err := setup.Edit(resolveRoot(), name, *confirm, edit)
	if err != nil {
		fmt.Fprintf(errw, "harness edit: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "edited %s: %d created, %d replaced.\n", name, res.Created, res.Replaced)
	reportConflicts(res, errw)
	return 0
}

func cmdRemove(args []string, out, errw io.Writer) int {
	if len(args) < 1 || isFlag(args[0]) {
		fmt.Fprintln(errw, "usage: harness remove <name> --confirm <name> [--archive]")
		return 2
	}
	name := args[0]
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(errw)
	confirm := fs.String("confirm", "", "must equal the project name")
	archive := fs.Bool("archive", false, "move the project dir to archived/<name> instead of refusing")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if err := setup.Remove(resolveRoot(), name, *confirm, *archive); err != nil {
		fmt.Fprintf(errw, "harness remove: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "removed %s from the lock.\n", name)
	return 0
}

func cmdVerify(out, errw io.Writer) int {
	res, err := setup.Verify(resolveRoot())
	if err != nil {
		fmt.Fprintf(errw, "harness verify: %v\n", err)
		return 1
	}
	for _, i := range res.Issues {
		fmt.Fprintf(out, "%s %s\n", i.Level, i.Message)
	}
	if !res.OK() {
		return 1
	}
	fmt.Fprintln(out, "verify: ok")
	return 0
}

func cmdRun(args []string, out, errw io.Writer, kind string) int {
	root := resolveRoot()
	lock, err := lockfile.Load(root)
	if err != nil {
		fmt.Fprintf(errw, "harness %s: %v\n", kind, err)
		return 1
	}
	targets := lock.Projects
	if len(args) > 0 {
		p, ok := lock.Find(args[0])
		if !ok {
			fmt.Fprintf(errw, "harness %s: no such project %q\n", kind, args[0])
			return 1
		}
		targets = []lockfile.Project{p}
	}
	failed := false
	for _, p := range targets {
		var err error
		switch kind {
		case "lint":
			err = runner.Lint(root, lock, p, out)
		case "test":
			err = runner.Test(root, lock, p, out)
		case "fmt":
			err = runner.Format(root, lock, p, out)
		}
		if err != nil {
			failed = true
		}
	}
	if failed {
		return 1
	}
	fmt.Fprintf(out, "%s: ok\n", kind)
	return 0
}

func cmdHook(args []string, stdin io.Reader, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, "usage: harness hook <event> [--agent claude|codex]")
		return 2
	}
	event := args[0]
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(errw)
	agent := fs.String("agent", "claude", "host agent: claude|codex")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	return hook.Run(event, *agent, resolveRoot(), stdin, out, errw)
}

// cmdReconfigure toggles repo-level capabilities (agents, git hooks, CI, agent
// docs, skills) after setup and reconciles the wiring — adding newly enabled
// files and pruning newly disabled ones.
func cmdReconfigure(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("reconfigure", flag.ContinueOnError)
	fs.SetOutput(errw)
	claude := fs.String("claude", "", "on|off — wire Claude Code hooks/skills")
	codex := fs.String("codex", "", "on|off — wire Codex hooks")
	gitHooks := fs.String("git-hooks", "", "on|off — native .git/hooks delegates")
	ci := fs.String("ci", "", "on|off — .github/workflows/ci.yml + docs/ci-setup.md")
	agentDocs := fs.String("agent-docs", "", "on|off — CLAUDE.md / AGENTS.md")
	skills := fs.String("skills", "", "on|off — project-local agent skills")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := resolveRoot()
	lock, err := lockfile.Load(root)
	if err != nil {
		fmt.Fprintf(errw, "harness reconfigure: %v\n", err)
		return 1
	}

	var agentsPtr *[]string
	if *claude != "" || *codex != "" {
		set := map[string]bool{}
		for _, a := range lock.Capabilities.Agents {
			set[a] = true
		}
		if v := onOff(*claude); v != nil {
			set[lockfile.AgentClaude] = *v
		}
		if v := onOff(*codex); v != nil {
			set[lockfile.AgentCodex] = *v
		}
		agents := []string{}
		for _, a := range []string{lockfile.AgentClaude, lockfile.AgentCodex} {
			if set[a] {
				agents = append(agents, a)
			}
		}
		agentsPtr = &agents
	}

	changes := setup.CapsInput{
		Agents: agentsPtr, GitHooks: onOff(*gitHooks), CI: onOff(*ci),
		AgentDocs: onOff(*agentDocs), Skills: onOff(*skills),
	}
	res, err := setup.Reconfigure(root, changes)
	if err != nil {
		fmt.Fprintf(errw, "harness reconfigure: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "reconfigured: %d added, %d updated, %d pruned. Run `harness bootstrap` if you changed git hooks.\n", res.Created, res.Replaced, res.Pruned)
	reportConflicts(res, errw)
	return 0
}

// isFlag reports whether an argument looks like a flag (leading '-').
func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func cmdRepair(out, errw io.Writer) int {
	if err := txn.Repair(resolveRoot()); err != nil {
		fmt.Fprintf(errw, "harness repair: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "repair: ok")
	return 0
}

func cmdCI(out, errw io.Writer) int {
	root := resolveRoot()
	// Pin check.
	if pin, err := os.ReadFile(filepath.Join(root, version.PinFileName)); err == nil {
		if msg := version.Mismatch(string(pin)); msg != "" {
			fmt.Fprintln(errw, msg)
			return 1
		}
	}
	// Verify.
	vr, err := setup.Verify(root)
	if err != nil {
		fmt.Fprintf(errw, "harness ci: %v\n", err)
		return 1
	}
	for _, i := range vr.Issues {
		fmt.Fprintf(out, "%s %s\n", i.Level, i.Message)
	}
	if !vr.OK() {
		return 1
	}
	// Lint + test + enabled quality verifiers, all projects.
	lock, _ := lockfile.Load(root)
	failed := false
	for _, p := range lock.Projects {
		if runner.Lint(root, lock, p, out) != nil {
			failed = true
		}
		if runner.Test(root, lock, p, out) != nil {
			failed = true
		}
		if runner.ExtraChecks(root, lock, p, out) != nil {
			failed = true
		}
	}
	if failed {
		return 1
	}
	fmt.Fprintln(out, "ci: ok")
	return 0
}

// cmdInstallTools installs the pinned analyzers required by the lock's enabled
// features into the harness-managed cache. It is the single acquisition path for
// CI (`ci.yml`), bootstrap, and local provisioning.
func cmdInstallTools(out, errw io.Writer) int {
	lock, err := lockfile.Load(resolveRoot())
	if err != nil {
		fmt.Fprintf(errw, "harness install-tools: %v\n", err)
		return 1
	}
	if err := analyzers.Install(lock, out); err != nil {
		fmt.Fprintf(errw, "harness install-tools: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "install-tools: ok")
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `harness — manage Go/Python/TypeScript projects built with AI coding agents

usage: harness <command> [flags]

setup & lifecycle:
  setup        scaffold a new managed project (--config -, --preset, --print-config-template)
  bootstrap    install dependencies and git-hook delegates
  add          append projects to a monorepo (--config -)
  edit         toggle a project's features (edit <name> --confirm <name> --lint on|off --gofumpt on|off --coverage-min 80 ...)
  reconfigure  toggle repo capabilities (reconfigure --ci on --skills off --codex on ...)
  remove       unregister a project (remove <name> --confirm <name> [--archive])
  verify       check the working tree matches harness.lock.json
  repair       heal an interrupted transaction

enforcement:
  lint | test | fmt [project]   run a phase for all projects (or one)
  ci                            authoritative gate (pin-check + verify + lint + test)
  hook <event> [--agent]        hook entry point (post-edit|stop|session-start|pre-commit|pre-push)

introspection:
  list [--json]                 list projects
  lock-query <name> --json      dump one project's lock entry
  toolchain --language <l> --json   dump the command table
  doctor                        diagnose the toolchain (read-only)
  install-skills                install the global setup skill/prompt
  install-tools                 install pinned analyzers (golangci-lint/gofumpt/gci) into the cache
  version                       print the harness version
`)
}

const configTemplate = `{
  "layout": "single",
  "preset": "standard",
  "capabilities": {
    "agents": ["claude"],
    "gitHooks": true,
    "ci": false,
    "agentDocs": false,
    "skills": false
  },
  "projects": [
    {
      "name": "myproj",
      "language": "go",
      "modulePath": "example.com/myproj",
      "features": { "lint": true, "test": true, "markdown": false, "diagrams": false, "gofumpt": false, "gci": false, "modTidy": false, "coverage": false },
      "coverageMin": 0
    }
  ]
}
`
