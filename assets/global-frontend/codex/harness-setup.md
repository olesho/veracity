# harness-setup

Drive `harness setup` for a new Go/Python/TypeScript project in the current
directory. You orchestrate; the `harness` binary does all file writing — never
hand-write project files.

1. Run `harness doctor`; relay missing prerequisites.
2. Ask: layout (single vs monorepo); per project name/language/(Go module path);
   per-project features (lint/test/markdown/diagrams — diagrams implies markdown);
   repo capabilities (agents claude/codex/none, gitHooks, ci, agentDocs, skills).
   Offer presets minimal/standard/full.
3. Compose config JSON and pipe it: `printf '%s' '<json>' | harness setup --config -`
   (see `harness setup --print-config-template`).
4. Run `harness bootstrap`, `harness verify`, `harness lint`, `harness test`.
5. Trust project hooks via `/hooks` if codex wiring was enabled.
