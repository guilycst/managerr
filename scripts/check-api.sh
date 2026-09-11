#!/bin/sh

set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

"$repo_dir/scripts/generate.sh" --check

(
  cd "$repo_dir/tools"
  GOWORK=off go tool github.com/daveshanley/vacuum \
    lint \
    --no-update-check \
    --remote=false \
    --ruleset "$repo_dir/api/vacuum.yaml" \
    --fail-severity=warn \
    --no-banner \
    --no-style \
    "$repo_dir/api/openapi.yaml"
)

echo "API checks passed"
