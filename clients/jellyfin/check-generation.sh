#!/bin/sh
set -eu

module_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$module_dir/../.." && pwd)
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/mastarr-jellyfin-generate.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT INT TERM

cp "$module_dir/openapi.yaml" "$tmp_dir/openapi.yaml"
mkdir -p "$tmp_dir/internal/generated"

# The generator resolves relative output paths from its process directory. It
# is invoked from the pinned tools module below, so make the temporary output
# path absolute while retaining every other module-local generation setting.
awk -v output="$tmp_dir/internal/generated/client.gen.go" \
  '/^output:[[:space:]]*/ { print "output: " output; found=1; next } { print } END { if (!found) print "output: " output }' \
  "$module_dir/oapi-codegen.yaml" > "$tmp_dir/oapi-codegen.yaml"

if [ -e "$module_dir/generated/client.gen.go" ]; then
  echo "Jellyfin generated transport must remain module-internal" >&2
  exit 1
fi

(
	cd "$repo_dir/tools"
	GOWORK=off GOPROXY=off GOSUMDB=off go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
	  -config "$tmp_dir/oapi-codegen.yaml" "$tmp_dir/openapi.yaml"
)

if ! cmp -s "$tmp_dir/internal/generated/client.gen.go" "$module_dir/internal/generated/client.gen.go"; then
	echo "Jellyfin generated client is out of date" >&2
	exit 1
fi

echo "Jellyfin generation checks passed"
