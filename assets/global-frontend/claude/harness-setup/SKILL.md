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
   - **Capabilities** (repo-level): which agents to wire (`claude`, `codex`, or
     none), and whether to enable `gitHooks`, `ci`, `agentDocs`, `skills`.
   - Offer the presets as shortcuts: **minimal**, **standard** (default), **full**.

4. **Compose the config JSON and run setup** — do not write a file. For an
   **existing** project (marker present or non-empty dir) pass `--adopt` and OMIT
   `modulePath` (it is auto-detected):

   ```sh
   printf '%s' '<the JSON you composed>' | harness setup --config - --adopt
   ```

   For a **brand-new empty** directory, drop `--adopt` (harness scaffolds a
   sample module). If the user wants their own module path in an empty dir, run
   `go mod init <path>` first, then use `--adopt`.

5. **Bootstrap and verify.** Run `harness bootstrap` (installs git-hook delegates,
   dependencies, and renders the initial docs), then `harness verify`, then
   `harness lint` and `harness test`. Relay results. If `harness lint` flags
   pre-existing formatting, run `harness fmt` (or `gofmt -w`) and re-lint.

6. **Populate diagram prose (if diagrams enabled).** `harness` never calls an
   LLM — you write the prose. Do it inline (the project-local harness-docs skill
   only loads in a later session):
   - `harness docs status --json` lists modules/interfaces with `needsSummary`.
   - `harness docs status --template` prints a ready-to-fill payload with the
     exact keys. Fill in each `summary`/interface string (grounded only in the
     names, signatures, and doc-comments — don't invent behavior) and submit it:

     ```sh
     printf '%s' '{"modules":{"<module-id>":{"summary":"...","interfaces":{"<Name>":"..."}}}}' \
       | harness docs enrich --from -
     ```

     (`modules` is an object keyed by module id; an array of `{"id":...}` objects
     is also accepted.)
   - Then `harness docs render`.

7. **Tell the user the hooks activate next session.** The agent hooks and git
   hooks were just written; Claude Code loads hook config at session start, so
   they take effect the next time they open Claude Code here. If `codex` is wired,
   they must run `codex` once and use `/hooks` to trust the project hooks.
