# harness-setup

Drive `harness setup` for a Go/Python/TypeScript project in the current
directory. You orchestrate; the `harness` binary does all file writing — never
hand-write project files.

1. Run `harness doctor`; relay missing prerequisites.
2. Detect whether this is an existing project (look for `go.mod` / `pyproject.toml`
   / `package.json`, or any files) — that decides whether you pass `--adopt`.
3. Ask: layout (single vs monorepo); per project name/language (for Go, do NOT
   ask for a module path when adopting — it is detected from `go.mod`);
   per-project features (lint/test/markdown/diagrams — diagrams implies markdown);
   repo capabilities (agents claude/codex/none, gitHooks, ci, agentDocs, skills).
   Offer presets minimal/standard/full.
4. Compose config JSON and run it. Existing project (marker present / non-empty):
   `printf '%s' '<json>' | harness setup --config - --adopt` and OMIT modulePath.
   Brand-new empty dir: drop `--adopt` (harness scaffolds a sample module).
5. Run `harness bootstrap` (installs hooks + renders initial docs), `harness
   verify`, `harness lint`, `harness test`. If lint flags formatting, run
   `harness fmt` and re-lint.
6. If diagrams are enabled, follow the harness-docs flow: `harness docs status
   --json` → write prose → `harness docs enrich` → `harness docs render`.
7. Trust project hooks via `/hooks` if codex wiring was enabled; note hooks take
   effect next session.
