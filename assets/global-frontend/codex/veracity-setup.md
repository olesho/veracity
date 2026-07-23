# veracity-setup

Drive `veracity setup` for a Go/Python/TypeScript project in the current
directory. You orchestrate; the `veracity` binary does all file writing — never
hand-write project files.

1. Run `veracity doctor`; relay missing prerequisites.
2. Detect whether this is an existing project (look for `go.mod` / `pyproject.toml`
   / `package.json`, or any files) — that decides whether you pass `--adopt`.
3. Ask: layout (single vs monorepo); per project name/language (for Go, do NOT
   ask for a module path when adopting — it is detected from `go.mod`);
   per-project features (lint/test/markdown/diagrams — diagrams implies markdown);
   quality verifiers (all off by default / on under `full`) — Go:
   gofumpt/gci/modTidy/coverage; TypeScript: coverage/audit(pnpm audit)/semgrep
   (SAST); semgrep is any-language. TS also gets strict type-aware ESLint +
   Prettier as baseline. If coverage is enabled (Go or TS), ask the minimum
   coverage percent and set `"coverageMin": <N>` (blank/0 = report only); repo
   capabilities (agents claude/codex/none, gitHooks, ci, agentDocs, skills).
   Offer presets minimal/standard/full.
4. Compose config JSON and run it. Existing project (marker present / non-empty):
   `printf '%s' '<json>' | veracity setup --config - --adopt` and OMIT modulePath.
   Brand-new empty dir: drop `--adopt` (veracity scaffolds a sample module).
5. Run `veracity bootstrap` (installs hooks + renders initial docs; reminds about
   analyzers), then `veracity install-tools` (installs pinned analyzers into the
   veracity cache), then `veracity verify`, `veracity lint`, `veracity test`. If lint
   flags formatting, run `veracity fmt` and re-lint.
6. If diagrams are enabled, follow the veracity-docs flow: `veracity docs status
   --json` → write prose → `veracity docs enrich` → `veracity docs render`.
7. Trust project hooks via `/hooks` if codex wiring was enabled; note hooks take
   effect next session.
