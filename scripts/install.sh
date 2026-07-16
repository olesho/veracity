#!/usr/bin/env bash
# Install the harness CLI from this checkout and register the Claude Code /
# Codex setup skill, so you can drive setup from Claude Code with /harness-setup.
#
# Usage:  ./scripts/install.sh
#
# (Once harness is published to a module host you'll be able to skip the checkout
#  and run: go install <module>/cmd/harness@latest)
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"

command -v go >/dev/null 2>&1 || { echo "error: Go is not installed (https://go.dev/dl/)"; exit 1; }

echo "==> installing the harness CLI from $repo"
( cd "$repo" && go install ./cmd/harness )

bindir="$(go env GOBIN)"
[ -z "$bindir" ] && bindir="$(go env GOPATH)/bin"
harness="$bindir/harness"
echo "==> installed: $harness"

case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo ""; echo "WARNING: $bindir is not on your PATH."
     echo "         Add it to your shell profile, e.g.:"
     echo "           export PATH=\"$bindir:\$PATH\"" ;;
esac

echo "==> registering the setup skill for Claude Code (and Codex)"
"$harness" install-skills

cat <<EOF

Done. To set up harness in a Go project:

  # A) Driven by Claude Code (recommended):
  cd /path/to/your/project
  #   open Claude Code and ask it to "set up harness in this project"
  #   (it follows the harness-setup skill: asks what you want, then runs setup)

  # B) Directly (no agent needed) — existing project with a go.mod:
  cd /path/to/your/project
  harness setup --adopt --config - <<'JSON'
  { "layout": "single",
    "capabilities": { "agents": ["claude"], "gitHooks": true, "skills": true },
    "projects": [{ "name": "myapp", "language": "go",
      "features": { "lint": true, "test": true, "markdown": true, "diagrams": true } }] }
  JSON
  harness bootstrap && harness verify

  # brand-new empty dir instead of --adopt? drop --adopt and harness scaffolds a
  # sample module (or run \`go mod init\` first, then use --adopt).
EOF
