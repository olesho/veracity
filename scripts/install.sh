#!/usr/bin/env bash
# Install the veracity CLI from this checkout and register the Claude Code /
# Codex setup skill, so you can drive setup from Claude Code with /veracity-setup.
#
# Usage:  ./scripts/install.sh
#
# (Once veracity is published to a module host you'll be able to skip the checkout
#  and run: go install <module>/cmd/veracity@latest)
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"

command -v go >/dev/null 2>&1 || { echo "error: Go is not installed (https://go.dev/dl/)"; exit 1; }

echo "==> installing the veracity CLI from $repo"
( cd "$repo" && go install ./cmd/veracity )

bindir="$(go env GOBIN)"
[ -z "$bindir" ] && bindir="$(go env GOPATH)/bin"
veracity="$bindir/veracity"
echo "==> installed: $veracity"

case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo ""; echo "WARNING: $bindir is not on your PATH."
     echo "         Add it to your shell profile, e.g.:"
     echo "           export PATH=\"$bindir:\$PATH\"" ;;
esac

echo "==> registering the setup skill for Claude Code (and Codex)"
"$veracity" install-skills

cat <<EOF

Done. To set up veracity in a Go project:

  # A) Driven by Claude Code (recommended):
  cd /path/to/your/project
  #   open Claude Code and ask it to "set up veracity in this project"
  #   (it follows the veracity-setup skill: asks what you want, then runs setup)

  # B) Directly (no agent needed) — existing project with a go.mod:
  cd /path/to/your/project
  veracity setup --adopt --config - <<'JSON'
  { "layout": "single",
    "capabilities": { "agents": ["claude"], "gitHooks": true, "skills": true },
    "projects": [{ "name": "myapp", "language": "go",
      "features": { "lint": true, "test": true, "markdown": true, "diagrams": true } }] }
  JSON
  veracity bootstrap && veracity verify

  # brand-new empty dir instead of --adopt? drop --adopt and veracity scaffolds a
  # sample module (or run \`go mod init\` first, then use --adopt).
EOF
