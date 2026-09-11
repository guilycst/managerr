#!/bin/sh

set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tools_dir=$repo_dir/tools
mode=check

case "${1:-}" in
  "") ;;
  --check) mode=check ;;
  --write) mode=write ;;
  *)
    echo "usage: $0 [--check|--write]" >&2
    exit 2
    ;;
esac

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/managerr-generate.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT INT TERM

make_config() {
  source=$1
  destination=$2
  output=$3

  awk -v output="$output" '
    /^output:[[:space:]]*/ { print "output: " output; found = 1; next }
    { print }
    END { if (!found) print "output: " output }
  ' "$source" >"$destination"
}

generate_oapi() {
  config=$1
  source_config=$2
  output=$3

  if [ "$mode" = write ]; then
    target=$output
  else
    target=$tmp_dir/$(basename "$output")
  fi

  mkdir -p "$(dirname -- "$target")"
  make_config "$source_config" "$config" "$target"
  (
    cd "$tools_dir"
    GOWORK=off go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
      -config "$config" "$repo_dir/api/openapi.yaml"
  )

  if [ "$mode" = check ] && [ -f "$output" ] && ! cmp -s "$target" "$output"; then
    echo "generated file is out of date: $output" >&2
    exit 1
  fi
}

generate_oapi \
  "$tmp_dir/api-oapi-codegen.yaml" \
  "$repo_dir/api/oapi-codegen.yaml" \
  "$repo_dir/internal/api/generated/server.gen.go"

generate_oapi \
  "$tmp_dir/ui-oapi-codegen.yaml" \
  "$repo_dir/ui/oapi-codegen.yaml" \
  "$repo_dir/ui/internal/api/generated/client.gen.go"

if [ -f "$repo_dir/sqlc.yaml" ]; then
  (
    cd "$repo_dir"
    GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate
  )
fi

if [ "$mode" = check ]; then
  echo "generation checks passed"
else
  echo "generation completed"
fi
