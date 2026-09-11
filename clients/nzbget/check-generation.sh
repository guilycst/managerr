#!/bin/sh
set -eu

module_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$module_dir/../.." && pwd)
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/mastarr-nzbget-generate.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT INT TERM

cp "$module_dir/openrpc.json" "$tmp_dir/openrpc.json"
(
  cd "$repo_dir/tools"
  GOWORK=off go run ./internal/nzbgetgen \
    -input "$tmp_dir/openrpc.json" \
    -output "$tmp_dir/generated.go"
)

if ! cmp -s "$tmp_dir/generated.go" "$module_dir/generated.go"; then
  echo "NZBGet generated client is out of date" >&2
  exit 1
fi

echo "NZBGet generation checks passed"
