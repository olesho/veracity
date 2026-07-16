#!/usr/bin/env bash
# End-to-end smoke test of the single-Go flow against the *built* harness binary.
# Builds harness, scaffolds a throwaway single-Go project, and exercises the
# whole pipeline: setup → bootstrap → verify → lint → test → docs status →
# enrich → render. Exits non-zero on any failure.
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
bindir="$(mktemp -d)"
bin="$bindir/harness"
proj="$(mktemp -d)"
trap 'rm -rf "$bindir" "$proj"' EXIT

echo "==> building harness"
go build -o "$bin" "$repo/cmd/harness"

echo "==> scaffolding single-Go project in $proj"
cd "$proj"
printf '%s' '{
  "layout": "single",
  "preset": "standard",
  "capabilities": { "agents": ["claude"], "gitHooks": true, "skills": true },
  "projects": [{
    "name": "demo", "language": "go", "modulePath": "example.com/demo",
    "features": { "lint": true, "test": true, "markdown": true, "diagrams": true }
  }]
}' | "$bin" setup --config -

echo "==> bootstrap"; "$bin" bootstrap
echo "==> verify";    "$bin" verify
echo "==> lint";      "$bin" lint
echo "==> test";      "$bin" test

echo "==> docs status"; "$bin" docs status
echo "==> docs enrich"
printf '%s' '{"modules":{"example.com/demo/example":{"summary":"The greeter module: a Greeter boundary and an English implementation.","interfaces":{"Greeter":"The contract for producing a greeting from a name."}}}}' | "$bin" docs enrich --from -
echo "==> docs render"; "$bin" docs render

for f in docs/MODULES.md docs/modules.svg docs/modules.html docs/summaries.json; do
  test -f "$f" || { echo "MISSING: $f"; exit 1; }
done
grep -q "interface Greeter" docs/modules.svg || { echo "SVG missing interface block"; exit 1; }

echo "==> E2E OK"
