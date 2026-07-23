# veracity

A published, globally-installed CLI for developing Go/Python/TypeScript software with AI coding agents
(Claude Code and OpenAI Codex CLI). `veracity` scaffolds and manages **clean projects** — the projects it
manages never vendor the tool itself; they carry only declarative wiring, thin shims, and generated docs
that delegate to the installed `veracity` binary.

> This repository **is the veracity source** (a Go module producing `cmd/veracity`). It is not itself a
> managed project. See `/Users/oleh/.claude/plans/i-want-to-create-optimized-river.md` for the full design.

## Install

From a checkout of this repo, run the install script — it builds the CLI, puts
it on your Go bin path, and registers the `veracity-setup` skill so you can drive
setup from Claude Code:

```sh
./scripts/install.sh
```

Then make sure your Go bin dir is on `PATH` (the script tells you if it isn't):

```sh
export PATH="$(go env GOPATH)/bin:$PATH"   # add to your shell profile
veracity version
```

Once a tagged release is published, this becomes a one-liner instead of a
checkout: `go install github.com/olesho/veracity/cmd/veracity@latest` followed
by `veracity install-skills`.

## Set up in a Go project with Claude Code

After installing, open Claude Code **in your project directory** and ask it to
"set up veracity in this project." It runs the `veracity-setup` skill: it checks
prerequisites, asks what you want (layout, features, capabilities), and runs
`veracity setup` + `veracity bootstrap` + `veracity verify` for you — you never
hand-write the config.

Prefer to do it yourself without the agent? See
[Adopt an existing Go project](#adopt-an-existing-go-project) below for the
direct `veracity setup --adopt` command.

## Quick start (in an empty project directory)

```sh
veracity install-skills                 # install the global setup skill/prompt for Claude/Codex (once)
veracity setup --preset standard --config -   # or drive it via the /veracity-setup agent skill
veracity bootstrap                      # install deps + git hooks
veracity verify                         # confirm the project matches its lock
```

## Adopt an existing Go project

Run this **from the root of your existing project** (where `go.mod` lives). The
`--adopt` flag tells veracity the directory is not empty on purpose: it detects
your real module path from `go.mod`, does **not** inject any sample code, leaves
your source and `go.mod` untouched, and only adds veracity's own files
(`veracity.lock.json`, `.veracity-version`, native lint config if enabled and
missing, and the wiring for whatever capabilities you turn on).

```sh
cd /path/to/your/go/project

veracity setup --adopt --config - <<'JSON'
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

veracity bootstrap     # install git-hook delegates (and any deps)
veracity verify        # confirm the lock matches your project
veracity lint          # gofmt + go vet + golangci-lint over your code
veracity docs render   # write docs/MODULES.md + modules.{svg,html} from your AST
```

Notes:
- `name` is just a label in single-project layout; `modulePath` is auto-detected
  from `go.mod`, so you don't pass it.
- Turn features off you don't want — e.g. drop `"diagrams"` for markdown-only, or
  set `"lint": false`. A minimal adopt (`"features": {"lint": true}`,
  `capabilities: {}`) adds almost nothing but the lock, the pin, and a
  `.golangci.yml`.
- To let the agent write the diagram prose: `veracity docs status --json` lists
  what's pending, then the agent submits it via `veracity docs enrich` (the
  `veracity-docs` skill, installed when `skills` is on, drives this). veracity
  itself never calls an LLM.
- Prefer to hand-edit the config? `veracity setup --print-config-template` prints
  a starting point.

## Reruns and changing your mind

`veracity setup` is a one-time step: running it again on an initialized repo is
**refused** (it points you at the commands below). Everything is adjustable
afterward without re-running setup:

```sh
# per-project features (name first, then flags; --confirm guards the change)
veracity edit myapp --confirm myapp --diagrams on      # enabling diagrams also enables markdown
veracity edit myapp --confirm myapp --lint off --test off
veracity edit myapp --confirm myapp --gofumpt on --gci on --mod-tidy on   # Go quality verifiers
veracity edit myapp --confirm myapp --coverage on --coverage-min 80       # fail below 80% coverage (Go/TS)
veracity edit web --confirm web --audit on --semgrep on                   # TS/SAST verifiers

# repo-level capabilities (no name; add/remove wiring, CI, skills, agents)
veracity reconfigure --ci on --agent-docs on           # creates ci.yml, CLAUDE.md, ...
veracity reconfigure --ci off                          # prunes the files it added
veracity reconfigure --codex on                        # add Codex hook wiring alongside Claude
```

- **Enabling** a capability creates its managed files; **disabling** one prunes
  exactly the files veracity manages for it. Your source, `go.mod`, native lint
  config, and generated docs are never pruned.
- If you toggle `git-hooks`, run `veracity bootstrap` afterward to (re)install the
  `.git/hooks` delegates.
- If you hand-edited a veracity-managed wiring file, a reconcile won't clobber it:
  it writes a `<file>.veracity-new` beside it and tells you.
- `language` and `modulePath` are immutable; changing them means removing and
  re-adding the project.

## Go quality verifiers (optional)

Beyond `lint`/`test`, Go projects have four independent, off-by-default quality
verifiers (all on under the `full` preset). The fast formatters **gofumpt** and
**gci** also run in the agent edit-loop (post-edit/stop hooks) so an AI agent gets
blocked-with-feedback and self-corrects each turn — `veracity fmt` auto-fixes both.
The heavier **modTidy** and **coverage** checks run at the git **pre-push** hook
and in **`veracity ci`**. `gofumpt`/`gci`/`modTidy` are Go-only (`coverage` is shared
with TypeScript — see below); `veracity setup`/`edit` reject a verifier on a language
it doesn't support. Enforcement by tier:

| Tier | Runs |
|---|---|
| agent post-edit / stop | `gofmt` + (enabled) `gofumpt`, `gci` on changed files |
| git pre-commit | lint (`golangci-lint`, `go vet`, `gofmt`) |
| git pre-push | `go test` + (enabled) `gofumpt`, `gci`, `modTidy`, `coverage` |
| `veracity ci` | verify + lint + test + all enabled verifiers |

| Feature | What it checks | `edit` flag |
|---|---|---|
| `gofumpt` | stricter formatting (superset of gofmt) | `--gofumpt on\|off` |
| `gci` | deterministic import section ordering | `--gci on\|off` |
| `modTidy` | `go.mod`/`go.sum` are tidy (`go mod tidy -diff`) | `--mod-tidy on\|off` |
| `coverage` | total statement coverage ≥ `coverageMin` | `--coverage on\|off`, `--coverage-min N` |

`coverageMin` is a per-project percent set at setup time (`"coverageMin": 80` in
the project entry) or later via `--coverage-min N`; `0` (the default) measures and
reports coverage but never fails.

These tools are **veracity-managed analyzers**: pinned versions installed by
`veracity install-tools` into a per-version cache under your user cache dir,
resolved by verified absolute path (sha256-checked). CI runs `install-tools`; run
it once locally too (`veracity bootstrap` reminds you). `veracity doctor` reports
each analyzer's cache status. If an enabled verifier's tool is missing, the gate
**fails** with a `run: veracity install-tools` hint rather than silently skipping.
(`VERACITY_ANALYZERS_DEV=1` allows a version-verified PATH binary for local
development.)

## TypeScript quality & security verifiers (optional)

TypeScript projects come with the same tiered enforcement as Go. Two things are
**baseline** (always on for scaffolded projects, no flag):

- **Strict ESLint** — the generated `eslint.config.js` uses type-aware
  `strictTypeChecked` (+ `stylisticTypeChecked`), catching unsafe `any`, floating
  promises, and unnecessary conditions the non-type-aware rules miss. `strict` is
  also on in `tsconfig.json`. (Adopted projects with an existing ESLint config are
  left untouched.)
- **Prettier** — formatting is enforced in the file-lint and project-lint phases.
- **Native git hooks replace Husky + lint-staged** — veracity installs
  `.git/hooks` delegates directly, so you don't add those dev-dependencies.

Beyond that, three independent, off-by-default verifiers (all on under the `full`
preset) toggle like the Go ones:

| Feature | What it checks | `edit` flag | Runs at |
|---|---|---|---|
| `coverage` | total line coverage ≥ `coverageMin` (via `vitest --coverage`) | `--coverage on\|off`, `--coverage-min N` | pre-push, `veracity ci` |
| `audit` | production dependency vulnerabilities (`pnpm audit --prod --audit-level high`) | `--audit on\|off` | pre-push, `veracity ci` |
| `semgrep` | SAST scan (`semgrep --config auto`, any language) | `--semgrep on\|off` | pre-push, `veracity ci` |

`coverage` is now shared by Go and TypeScript (same `coverageMin` field; `0`
measures-and-reports without failing). `audit` scans **production** dependencies
only, so a scaffold (dev-deps only) passes clean while real runtime deps are
gated. `semgrep` is language-agnostic and runs via Docker — like the `sonar` verifier it **soft-skips** (WARN, no failure) when Docker
is unavailable, so it never blocks a machine that lacks it. `veracity doctor` reports
Docker availability for both. `pnpm audit` and vitest coverage come from the
project's own `package.json` (veracity adds `@vitest/coverage-v8`), not the
`go install`-based analyzer cache.

**Roadmap:**
- Go security/supply-chain group — `gosec`, `govulncheck`, `osv-scanner`, and
  `syft`/`grype` (SBOM) — planned as Go-only toggles; not implemented yet.
- CI-native checks that need GitHub Actions PR context — **CodeQL** and
  **Danger JS** — will be generated as standalone workflow jobs (they can't run
  inside `veracity ci`); not implemented yet.

## What agents are told (signals, not context)

veracity puts **nothing** in an agent's context up front. There is no preamble, no
always-loaded instruction block, no session briefing — the `session-start` hook
records the HEAD ref and prints nothing at all. Agents explore the project the
normal way; veracity speaks only when something breaks.

Every signal is a hook exit code plus a message on stderr, and each one is shaped
for one of exactly two recovery paths:

1. **The error explains itself → the agent just fixes it.** Lint, formatting,
   type, test, coverage, and security-scan failures carry the underlying tool's
   own output plus a concrete next command. No skill, no extra context.
2. **The error names a skill → the agent loads that skill, which handles it.**
   Reserved for work an error string genuinely cannot describe — today only the
   documentation / diagram prose flow (`veracity-docs`).

### Signals implemented today

| When | Trigger | Signal | Path |
|---|---|---|---|
| post-edit | lint / `gofumpt` / `gci` fail on the file just edited | exit 2 + stderr, blocks the turn | (1) — names `veracity fmt` |
| post-edit | installed binary ≠ `.veracity-version` pin | stderr warning, non-blocking | (1) — carries the exact `go install` line |
| Stop | lint / format fail on any file changed this session | exit 2 + stderr, blocks | (1) |
| Stop | **managed wiring hand-edited or deleted** | exit 2 + stderr, blocks | (1) — names `veracity restore` |
| Stop | diagram prose stale for a changed project | stderr note, non-blocking | (2) — names the `veracity-docs` skill |
| pre-commit | pin mismatch, **drift**, then lint | exit 1, git aborts | (1) |
| pre-push | pin mismatch, **drift**, tests + enabled verifiers (`gofumpt`, `gci`, `modTidy`, `coverage`, `audit`, `semgrep`, `sonar`) | exit 1, git aborts | (1) |
| `veracity ci` | verify + lint + test + all enabled verifiers | job failure | (1) |

Path (2) covers exactly one case, and only when the user has enabled `diagrams`
**and** `skills`. Everything else is path (1): a string the model can act on
directly, with no skill load and no context cost.

### Managed-wiring drift

`hooks/`, `.claude/`, `.codex/`, the generated CI workflow, and the agent docs are
**veracity's to own**. Editing them by hand is silently ineffective — the next
reconcile would side-file the change — so drift is a `FAIL`, enforced at all three
tiers (agent Stop hook, git pre-commit/pre-push, and `veracity ci`).

The remedy is named in the failure message and is always safe, because managed
wiring is generated content:

```sh
veracity restore     # rewrite managed wiring from the generated content
```

`restore` discards local edits to managed files and recreates deleted ones. Your
source, `go.mod`, native lint config, and generated docs are Owned — it never
touches them. To keep a change to managed wiring, change the thing that generates
it (`veracity edit` / `veracity reconfigure`) rather than the file.

### Known gaps (things that break silently)

Listed so the intent is explicit rather than assumed:

- **Structural doc regeneration failures are discarded.** The Stop hook calls
  `docgen.Render(..., io.Discard)` and ignores the returned error, so a docs build
  that fails is completely silent.
- **Unreconciled `<file>.veracity-new` side files are never re-surfaced.** A
  reconcile that hits a locally-modified managed file writes one and reports it at
  that moment; nothing mentions it again afterward.

## Concepts

- **One published CLI, clean projects.** The binary embeds templates, wiring, extractors, and the toolchain
  table. Generated projects contain no tool source — only `veracity.lock.json`, `.veracity-version`, native
  language config, generated docs, and thin declarative wiring.
- **Lightweight by construction.** A tiny mandatory core (`veracity.lock.json` + `.veracity-version`); every
  other capability (agents, git hooks, CI, docs, skills) is opt-in and generated only when enabled.
- **Zero context by default.** veracity never preloads an agent's context. It communicates only by failing a
  hook with an actionable message — self-explanatory for code issues, or naming a skill for the rare flow
  (docs/diagrams) that needs one.
- **Tiered enforcement.** Agent hooks (fast in-loop feedback) → native git hooks (universal local backstop)
  → CI (authoritative), all calling the same `veracity` logic.

## Development

```sh
go build ./cmd/veracity
go test ./...
```
