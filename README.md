# harness

A published, globally-installed CLI for developing Go/Python/TypeScript software with AI coding agents
(Claude Code and OpenAI Codex CLI). `harness` scaffolds and manages **clean projects** — the projects it
manages never vendor the tool itself; they carry only declarative wiring, thin shims, and generated docs
that delegate to the installed `harness` binary.

> This repository **is the harness source** (a Go module producing `cmd/harness`). It is not itself a
> managed project. See `/Users/oleh/.claude/plans/i-want-to-create-optimized-river.md` for the full design.

## Install

From a checkout of this repo, run the install script — it builds the CLI, puts
it on your Go bin path, and registers the `harness-setup` skill so you can drive
setup from Claude Code:

```sh
./scripts/install.sh
```

Then make sure your Go bin dir is on `PATH` (the script tells you if it isn't):

```sh
export PATH="$(go env GOPATH)/bin:$PATH"   # add to your shell profile
harness version
```

Once the module is published to a host, this becomes a one-liner instead of a
checkout: `go install <module>/cmd/harness@latest` followed by
`harness install-skills`. (The module path `github.com/olesho/harness` is a
placeholder until then.)

## Set up in a Go project with Claude Code

After installing, open Claude Code **in your project directory** and ask it to
"set up harness in this project." It runs the `harness-setup` skill: it checks
prerequisites, asks what you want (layout, features, capabilities), and runs
`harness setup` + `harness bootstrap` + `harness verify` for you — you never
hand-write the config.

Prefer to do it yourself without the agent? See
[Adopt an existing Go project](#adopt-an-existing-go-project) below for the
direct `harness setup --adopt` command.

## Quick start (in an empty project directory)

```sh
harness install-skills                 # install the global setup skill/prompt for Claude/Codex (once)
harness setup --preset standard --config -   # or drive it via the /harness-setup agent skill
harness bootstrap                      # install deps + git hooks
harness verify                         # confirm the project matches its lock
```

## Adopt an existing Go project

Run this **from the root of your existing project** (where `go.mod` lives). The
`--adopt` flag tells harness the directory is not empty on purpose: it detects
your real module path from `go.mod`, does **not** inject any sample code, leaves
your source and `go.mod` untouched, and only adds harness's own files
(`harness.lock.json`, `.harness-version`, native lint config if enabled and
missing, and the wiring for whatever capabilities you turn on).

```sh
cd /path/to/your/go/project

harness setup --adopt --config - <<'JSON'
{
  "layout": "single",
  "capabilities": { "agents": ["claude"], "gitHooks": true, "skills": true },
  "projects": [{
    "name": "myapp",
    "language": "go",
    "features": { "lint": true, "test": true, "markdown": true, "diagrams": true }
  }]
}
JSON

harness bootstrap     # install git-hook delegates (and any deps)
harness verify        # confirm the lock matches your project
harness lint          # gofmt + go vet + golangci-lint over your code
harness docs render   # write docs/MODULES.md + modules.{svg,html} from your AST
```

Notes:
- `name` is just a label in single-project layout; `modulePath` is auto-detected
  from `go.mod`, so you don't pass it.
- Turn features off you don't want — e.g. drop `"diagrams"` for markdown-only, or
  set `"lint": false`. A minimal adopt (`"features": {"lint": true}`,
  `capabilities: {}`) adds almost nothing but the lock, the pin, and a
  `.golangci.yml`.
- To let the agent write the diagram prose: `harness docs status --json` lists
  what's pending, then the agent submits it via `harness docs enrich` (the
  `harness-docs` skill, installed when `skills` is on, drives this). harness
  itself never calls an LLM.
- Prefer to hand-edit the config? `harness setup --print-config-template` prints
  a starting point.

## Reruns and changing your mind

`harness setup` is a one-time step: running it again on an initialized repo is
**refused** (it points you at the commands below). Everything is adjustable
afterward without re-running setup:

```sh
# per-project features (name first, then flags; --confirm guards the change)
harness edit myapp --confirm myapp --diagrams on      # enabling diagrams also enables markdown
harness edit myapp --confirm myapp --lint off --test off
harness edit myapp --confirm myapp --gofumpt on --gci on --mod-tidy on   # Go quality verifiers
harness edit myapp --confirm myapp --coverage on --coverage-min 80       # fail below 80% coverage

# repo-level capabilities (no name; add/remove wiring, CI, skills, agents)
harness reconfigure --ci on --agent-docs on           # creates ci.yml, CLAUDE.md, ...
harness reconfigure --ci off                          # prunes the files it added
harness reconfigure --codex on                        # add Codex hook wiring alongside Claude
```

- **Enabling** a capability creates its managed files; **disabling** one prunes
  exactly the files harness manages for it. Your source, `go.mod`, native lint
  config, and generated docs are never pruned.
- If you toggle `git-hooks`, run `harness bootstrap` afterward to (re)install the
  `.git/hooks` delegates.
- If you hand-edited a harness-managed wiring file, a reconcile won't clobber it:
  it writes a `<file>.harness-new` beside it and tells you.
- `language` and `modulePath` are immutable; changing them means removing and
  re-adding the project.

## Go quality verifiers (optional)

Beyond `lint`/`test`, Go projects have four independent, off-by-default quality
verifiers (all on under the `full` preset). The fast formatters **gofumpt** and
**gci** also run in the agent edit-loop (post-edit/stop hooks) so an AI agent gets
blocked-with-feedback and self-corrects each turn — `harness fmt` auto-fixes both.
The heavier **modTidy** and **coverage** checks run at the git **pre-push** hook
and in **`harness ci`**. They are Go-only — `harness setup`/`edit` reject them on
Python/TypeScript projects. Enforcement by tier:

| Tier | Runs |
|---|---|
| agent post-edit / stop | `gofmt` + (enabled) `gofumpt`, `gci` on changed files |
| git pre-commit | lint (`golangci-lint`, `go vet`, `gofmt`) |
| git pre-push | `go test` + (enabled) `gofumpt`, `gci`, `modTidy`, `coverage` |
| `harness ci` | verify + lint + test + all enabled verifiers |

| Feature | What it checks | `edit` flag |
|---|---|---|
| `gofumpt` | stricter formatting (superset of gofmt) | `--gofumpt on\|off` |
| `gci` | deterministic import section ordering | `--gci on\|off` |
| `modTidy` | `go.mod`/`go.sum` are tidy (`go mod tidy -diff`) | `--mod-tidy on\|off` |
| `coverage` | total statement coverage ≥ `coverageMin` | `--coverage on\|off`, `--coverage-min N` |

`coverageMin` is a per-project percent set at setup time (`"coverageMin": 80` in
the project entry) or later via `--coverage-min N`; `0` (the default) measures and
reports coverage but never fails.

These tools are **harness-managed analyzers**: pinned versions installed by
`harness install-tools` into a per-version cache under your user cache dir,
resolved by verified absolute path (sha256-checked). CI runs `install-tools`; run
it once locally too (`harness bootstrap` reminds you). `harness doctor` reports
each analyzer's cache status. If an enabled verifier's tool is missing, the gate
**fails** with a `run: harness install-tools` hint rather than silently skipping.
(`HARNESS_ANALYZERS_DEV=1` allows a version-verified PATH binary for local
development.)

**Roadmap:** a security/supply-chain group — `gosec`, `govulncheck`,
`osv-scanner`, and `syft`/`grype` (SBOM) — is planned as the same kind of Go-only
toggles; it is not implemented yet.

## Concepts

- **One published CLI, clean projects.** The binary embeds templates, wiring, extractors, and the toolchain
  table. Generated projects contain no tool source — only `harness.lock.json`, `.harness-version`, native
  language config, generated docs, and thin declarative wiring.
- **Lightweight by construction.** A tiny mandatory core (`harness.lock.json` + `.harness-version`); every
  other capability (agents, git hooks, CI, docs, skills) is opt-in and generated only when enabled.
- **Tiered enforcement.** Agent hooks (fast in-loop feedback) → native git hooks (universal local backstop)
  → CI (authoritative), all calling the same `harness` logic.

## Development

```sh
go build ./cmd/harness
go test ./...
```
