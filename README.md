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
