---
name: harness-setup
description: Set up a new harness-managed Go/Python/TypeScript project in the current directory. Use when the user wants to scaffold a project with harness, run `harness setup`, or asks to initialize a harness project.
---

# harness-setup

Drive `harness setup` conversationally. You orchestrate; the `harness` binary
does all file writing. Never hand-write project files yourself.

## Procedure

1. **Diagnose the environment.** Run `harness doctor`. Relay any missing
   prerequisites (go/python3/node/uv/pnpm) and the install commands it prints.
   Do not proceed until the prerequisites the user's chosen languages need are
   present.

2. **Ask the setup questions** (use the host's structured-question UI if
   available; otherwise ask in plain text):
   - **Layout**: a single project at the repo root, or a monorepo of several
     projects under `projects/<name>/`?
   - For each project: a **name** (lowercase kebab, e.g. `api`), a **language**
     (`go` | `python` | `typescript`), and for Go an optional **module path**
     (default `example.com/<name>`).
   - **Features** per project: `lint`, `test`, `markdown` docs, `diagrams`
     (enabling diagrams enables markdown automatically).
   - **Capabilities** (repo-level): which agents to wire (`claude`, `codex`, or
     none), and whether to enable `gitHooks`, `ci`, `agentDocs`, `skills`.
   - Offer the presets as shortcuts: **minimal** (lint only, no agents/hooks/CI),
     **standard** (lint+test+gitHooks+the agent you use), **full** (everything).

3. **Compose the config JSON and pipe it to the engine** — do not write a file:

   ```sh
   printf '%s' '<the JSON you composed>' | harness setup --config -
   ```

   The JSON shape is what `harness setup --print-config-template` documents.

4. **Bootstrap and verify.** Run `harness bootstrap`, then `harness verify`,
   then `harness lint` and `harness test`. Relay results.

5. **Codex trust.** If `codex` is among the wired agents, tell the user to run
   `codex` in the repo once and use `/hooks` to trust the project hooks.
