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

module_dirs="
.
ui
tools
clients/qbittorrent
clients/nzbget
"

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

run_module_checks() {
  module_dir=$1
  if [ ! -f "$repo_dir/$module_dir/go.mod" ]; then
    echo "module manifest is missing: $repo_dir/$module_dir/go.mod" >&2
    exit 1
  fi
  (
    cd "$repo_dir/$module_dir"
    packages=$(GOWORK=off go list ./...)
    if [ -z "$packages" ]; then
      echo "module has no discoverable packages: $module_dir" >&2
      exit 1
    fi
    GOWORK=off go test -mod=readonly $packages
    GOWORK=off go vet -mod=readonly $packages
    GOWORK=off go mod verify
  )
}

run_fast() {
  "$repo_dir/scripts/check-api.sh"
  "$repo_dir/scripts/check-lint.sh" --architecture-only
  check_format

  (
    cd "$repo_dir"
    GOWORK=off go test -mod=readonly \
      ./internal/domain \
      ./internal/ports \
      ./internal/adapters/qbittorrent/inventory
  )
  (
    cd "$repo_dir/ui"
    GOWORK=off go test -mod=readonly ./...
  )
}

run_fast

if [ "$mode" = ci ]; then
  "$repo_dir/scripts/check-lint.sh"

  for module_dir in $module_dirs; do
    run_module_checks "$module_dir"
  done
fi

echo "guardrail checks passed ($mode)"
