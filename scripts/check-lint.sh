#!/bin/sh

set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
lint_tool=github.com/golangci/golangci-lint/v2/cmd/golangci-lint

GOWORK=off go tool -modfile="$repo_dir/tools/go.mod" "$lint_tool" \
  run \
  --config "$repo_dir/.golangci.yml" \
  --timeout=5m \
  ./...

(
  cd "$repo_dir/ui"
  GOWORK=off go tool -modfile="$repo_dir/tools/go.mod" "$lint_tool" \
    run \
    --config "$repo_dir/.golangci.yml" \
    --timeout=5m \
    ./...
)

python3 "$repo_dir/scripts/check-architecture.py"

echo "Go lint and architecture checks passed"
