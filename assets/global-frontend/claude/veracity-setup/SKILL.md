---
name: veracity-setup
description: Set up a new veracity-managed Go/Python/TypeScript project in the current directory. Use when the user wants to scaffold a project with veracity, run `veracity setup`, or asks to initialize a veracity project.
---

# veracity-setup

Drive `veracity setup` conversationally. You orchestrate; the `veracity` binary
does all file writing. Never hand-write project files yourself.

## Procedure

1. **Diagnose the environment.** Run `veracity doctor`. Relay any missing
   prerequisites (go/python3/node/uv/pnpm) and the install commands it prints.
   Do not proceed until the prerequisites for the chosen languages are present.

2. **Detect whether this is an existing project.** List the directory and look
   for a language marker (`go.mod`, `pyproject.toml`, or `package.json`) or any
   other files. Remember the answer — it decides whether you pass `--adopt`.

3. **Ask the setup questions** (use the host's structured-question UI if
   available; keep option previews to plain strings or omit them entirely — never
   pass a null preview):
   - **Layout**: a single project at the repo root, or a monorepo of several
     projects under `projects/<name>/`?
   - For each project: a **name** (lowercase kebab, e.g. `api`) and a **language**
     (`go` | `python` | `typescript`). For a Go project, **do not ask for a module
     path when adopting an existing project — it is detected from `go.mod`.** Only
     ask (default `example.com/<name>`) for a brand-new project.
   - **Features** per project: `lint`, `test`, `markdown` docs, `diagrams`
     (enabling diagrams enables markdown automatically).
   - **Go quality verifiers** (ask only for `go` projects; all off by
     default, all on under the `full` preset): `gofumpt` (stricter formatting),
     `gci` (import ordering), `modTidy` (`go mod tidy` hygiene), and `coverage`
     (a total-coverage gate). gofumpt/gci also run in the agent edit-loop
     (post-edit/stop hooks) so the agent auto-fixes formatting each turn via
     `veracity fmt`; modTidy/coverage run at git pre-push and in `veracity ci`.
   - **TypeScript quality/security verifiers** (ask only for `typescript`
     projects; all off by default, all on under the `full` preset): `coverage`
     (a `vitest --coverage` gate), `audit` (`pnpm audit` dependency scan), and
     `semgrep` (SAST). Strict type-aware ESLint + Prettier are already baseline
     (no flag). Run at git pre-push and in `veracity ci`.
   - **If any project enables `coverage` (Go or TS), ask for the minimum
     coverage percent** — "minimum coverage % (blank = report only)" — and put it
     in the project entry as `"coverageMin": <N>` (omit or `0` = measure and
     report, never fail).
   - `semgrep` is language-agnostic (any project); it runs via Docker and
     soft-skips when Docker is unavailable. Offer each verifier only for the
     language(s) that support it — `veracity` rejects a mismatch.
   - **Capabilities** (repo-level): which agents to wire (`claude`, `codex`, or
     none), and whether to enable `gitHooks`, `ci`, `agentDocs`, `skills`.
   - Offer the presets as shortcuts: **minimal**, **standard** (default), **full**.

4. **Compose the config JSON and run setup** — do not write a file. For an
   **existing** project (marker present or non-empty dir) pass `--adopt` and OMIT
   `modulePath` (it is auto-detected):

   ```sh
   printf '%s' '<the JSON you composed>' | veracity setup --config - --adopt
   ```

   For a **brand-new empty** directory, drop `--adopt` (veracity scaffolds a
   sample module). If the user wants their own module path in an empty dir, run
   `go mod init <path>` first, then use `--adopt`.

5. **Bootstrap, provision tools, verify.** Run `veracity bootstrap` (installs
   git-hook delegates and dependencies, renders the initial docs, and — if any
   analyzers are needed — reminds you to install them). Then run
   `veracity install-tools` to install the pinned analyzers (golangci-lint, and
   gofumpt/gci if their verifiers are on) into the veracity cache; the git hooks
   and CI need them. Then `veracity verify`, `veracity lint`, and `veracity test`.
   Relay results. If `veracity lint` flags pre-existing formatting, run
   `veracity fmt` (or `gofmt -w`) and re-lint.

6. **Populate diagram prose (if diagrams enabled).** `veracity` never calls an
   LLM — you write the prose. Do it inline (the project-local veracity-docs skill
   only loads in a later session):
   - `veracity docs status --json` lists modules/interfaces with `needsSummary`.
   - `veracity docs status --template` prints a ready-to-fill payload with the
     exact keys. Fill in each `summary`/interface string (grounded only in the
     names, signatures, and doc-comments — don't invent behavior) and submit it:

     ```sh
     printf '%s' '{"modules":{"<module-id>":{"summary":"...","interfaces":{"<Name>":"..."}}}}' \
       | veracity docs enrich --from -
     ```

     (`modules` is an object keyed by module id; an array of `{"id":...}` objects
     is also accepted.)
   - Then `veracity docs render`.

7. **Tell the user the hooks activate next session.** The agent hooks and git
   hooks were just written; Claude Code loads hook config at session start, so
   they take effect the next time they open Claude Code here. If `codex` is wired,
   they must run `codex` once and use `/hooks` to trust the project hooks.
