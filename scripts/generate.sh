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

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/mastarr-generate.XXXXXX")
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

bundle_openapi() {
  if [ "$mode" = write ]; then
    output=$repo_dir/api/openapi.yaml
  else
    output=$tmp_dir/openapi.yaml
  fi

  (
    cd "$tools_dir"
    GOWORK=off go run ./internal/openapi-bundle \
      --input "$repo_dir/api/fragments" \
      --output "$output"
  )

  if [ "$mode" = check ] && ! cmp -s "$output" "$repo_dir/api/openapi.yaml"; then
    echo "bundled OpenAPI document is out of date: $repo_dir/api/openapi.yaml" >&2
    exit 1
  fi
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

bundle_openapi

generate_oapi \
  "$tmp_dir/api-oapi-codegen.yaml" \
  "$repo_dir/api/oapi-codegen.yaml" \
  "$repo_dir/internal/api/generated/server.gen.go"

generate_oapi \
  "$tmp_dir/ui-oapi-codegen.yaml" \
  "$repo_dir/ui/oapi-codegen.yaml" \
  "$repo_dir/ui/internal/api/generated/client.gen.go"

generate_envdoc() {
  output=$repo_dir/docs/generated/environment.md
  if [ "$mode" = write ]; then
    target=$output
  else
    target=$tmp_dir/environment.md
  fi

  mkdir -p "$(dirname -- "$target")"
  envdoc_line=$(awk '/^\/\/go:generate .*envdoc/ { print NR; exit }' "$repo_dir/internal/bootstrap/environment.go")
  (
    cd "$repo_dir/internal/bootstrap"
    GOFILE=environment.go GOLINE="$envdoc_line" GOWORK=off \
      go tool -modfile=../../tools/go.mod github.com/g4s8/envdoc \
      -output "$target" -types=Environment
  )
  awk '{ lines[NR] = $0 } END { n = NR; for (; n > 0 && lines[n] == ""; n--) {} for (i = 1; i <= n; i++) print lines[i] }' \
    "$target" >"$target.tmp"
  mv "$target.tmp" "$target"

  if [ "$mode" = check ]; then
    if [ ! -f "$output" ]; then
      echo "generated file is missing: $output" >&2
      exit 1
    fi
    if ! cmp -s "$target" "$output"; then
      echo "generated file is out of date: $output" >&2
      exit 1
    fi
  fi
}

generate_envdoc

if [ -f "$repo_dir/sqlc.yaml" ]; then
  (
    cd "$tools_dir"
    GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate \
      -f "$repo_dir/sqlc.yaml"
  )
fi

if [ "$mode" = check ]; then
  echo "generation checks passed"
else
  echo "generation completed"
fi
