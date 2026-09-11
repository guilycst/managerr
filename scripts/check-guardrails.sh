#!/bin/sh

set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mode=fast

case "${1:-}" in
  ""|--fast) mode=fast ;;
  --ci) mode=ci ;;
  *)
    echo "usage: $0 [--fast|--ci]" >&2
    exit 2
    ;;
esac

check_format() {
  unformatted=$(find "$repo_dir" \
    -type f \
    -name '*.go' \
    -not -path '*/.git/*' \
    -not -path '*/vendor/*' \
    -not -path '*/node_modules/*' \
    -exec gofmt -l {} +)
  if [ -n "$unformatted" ]; then
    echo "unformatted Go files:" >&2
    echo "$unformatted" >&2
    exit 1
  fi
  git -C "$repo_dir" diff --check
  git -C "$repo_dir" diff --cached --check
}

run_fast() {
  "$repo_dir/scripts/check-api.sh"
  python3 "$repo_dir/scripts/check-architecture.py"
  check_format

  (
    cd "$repo_dir"
    GOWORK=off go test \
      ./internal/domain \
      ./internal/ports \
      ./internal/adapters/qbittorrent/inventory
  )
  (
    cd "$repo_dir/ui"
    GOWORK=off go test ./...
  )
}

run_fast

if [ "$mode" = ci ]; then
  "$repo_dir/scripts/check-lint.sh"

  (
    cd "$repo_dir"
    GOWORK=off go test ./...
    GOWORK=off go vet ./...
    GOWORK=off go mod verify
  )
  (
    cd "$repo_dir/ui"
    GOWORK=off go test ./...
    GOWORK=off go vet ./...
    GOWORK=off go mod verify
  )
  (
    cd "$repo_dir/tools"
    GOWORK=off go test ./...
    GOWORK=off go vet ./...
    GOWORK=off go mod verify
  )
fi

echo "guardrail checks passed ($mode)"
