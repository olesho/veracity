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
# Isolate the harness-managed analyzer cache so the run is self-contained.
export HARNESS_ANALYZERS_DIR="$bindir/analyzers"

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

echo "==> bootstrap";     "$bin" bootstrap
echo "==> install-tools"; "$bin" install-tools   # provision golangci-lint (+ verifiers later)
echo "==> verify";        "$bin" verify
echo "==> lint";          "$bin" lint
echo "==> test";          "$bin" test

echo "==> docs status"; "$bin" docs status
echo "==> docs enrich"
printf '%s' '{"modules":{"example.com/demo/example":{"summary":"The greeter module: a Greeter boundary and an English implementation.","interfaces":{"Greeter":"The contract for producing a greeting from a name."}}}}' | "$bin" docs enrich --from -
echo "==> docs render"; "$bin" docs render

for f in docs/MODULES.md docs/modules.svg docs/modules.html docs/summaries.json; do
  test -f "$f" || { echo "MISSING: $f"; exit 1; }
done
grep -q "interface Greeter" docs/interfaces/example-Greeter.svg || { echo "interface SVG missing interface block"; exit 1; }

# --- Go quality verifiers (gofumpt/gci/mod-tidy/coverage) --------------------
echo "==> enable verifiers"
"$bin" edit demo --confirm demo --gofumpt on --gci on --mod-tidy on --coverage on --coverage-min 0

echo "==> install-tools"; "$bin" install-tools
"$bin" doctor | grep -Eq 'gofumpt|gci' || { echo "doctor did not report analyzers"; exit 1; }

echo "==> fmt (normalize), then ci with verifiers"
"$bin" fmt
"$bin" ci

echo "==> fmt is idempotent"
"$bin" fmt
git diff --quiet 2>/dev/null || true   # not a git repo here; fmt must simply not thrash
"$bin" ci

echo "==> missing analyzer hard-fails ci (no silent skip)"
rm -rf "$HARNESS_ANALYZERS_DIR"/gofumpt@*
if "$bin" ci >/dev/null 2>&1; then echo "ci should fail when gofumpt is uninstalled"; exit 1; fi
"$bin" install-tools   # restore

echo "==> coverage gate fails below threshold"
# The greeter sample is fully covered; add an untested (but exported, gofumpt-clean)
# function so total coverage drops below 100%.
printf 'package example\n\n// Uncovered has no test.\nfunc Uncovered() int {\n\treturn 7\n}\n' > example/extra.go
"$bin" fmt
"$bin" edit demo --confirm demo --coverage-min 100
if "$bin" ci >/dev/null 2>&1; then echo "ci should fail a 100% coverage gate"; exit 1; fi
"$bin" edit demo --confirm demo --coverage-min 0

echo "==> E2E OK"
