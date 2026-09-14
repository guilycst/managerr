#!/bin/sh
set -eu

module_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/mastarr-qbittorrent-generate.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT INT TERM

cp "$module_dir/openapi.yaml" "$tmp_dir/openapi.yaml"
cp "$module_dir/oapi-codegen.yaml" "$tmp_dir/oapi-codegen.yaml"
mkdir -p "$tmp_dir/internal/generated"

if [ -e "$module_dir/generated/client.gen.go" ]; then
  echo "qBittorrent generated transport must remain module-internal" >&2
  exit 1
fi

(
  cd "$tmp_dir"
  GOWORK=off go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 \
    -config oapi-codegen.yaml openapi.yaml
)

if ! cmp -s "$tmp_dir/internal/generated/client.gen.go" "$module_dir/internal/generated/client.gen.go"; then
  echo "qBittorrent generated client is out of date" >&2
  exit 1
fi

echo "qBittorrent generation checks passed"
