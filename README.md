# harness

A published, globally-installed CLI for developing Go/Python/TypeScript software with AI coding agents
(Claude Code and OpenAI Codex CLI). `harness` scaffolds and manages **clean projects** — the projects it
manages never vendor the tool itself; they carry only declarative wiring, thin shims, and generated docs
that delegate to the installed `harness` binary.

> This repository **is the harness source** (a Go module producing `cmd/harness`). It is not itself a
> managed project. See `/Users/oleh/.claude/plans/i-want-to-create-optimized-river.md` for the full design.

## Install

```sh
go install github.com/olesho/harness/cmd/harness@vX.Y.Z
```

(The module path `github.com/olesho/harness` is a placeholder — set it to your published module.)

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
