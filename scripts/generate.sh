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

# A direct check must enter through the stable staged-check caller before this
# worktree copy can perform any generation. The caller extracts and executes
# the exact git write-tree script; its snapshot marker prevents recursion.
if [ "$mode" = check ] && [ -z "${MASTARR_GENERATION_SNAPSHOT:-}" ] && \
  git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  exec "$repo_dir/scripts/check-guardrails.sh" --generation
fi

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/mastarr-generate.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT INT TERM

require_sqlc_config() {
  sqlc_config_path=$repo_dir/sqlc.yaml
  if [ ! -f "$sqlc_config_path" ]; then
    echo "authoritative SQLC config is missing: $sqlc_config_path" >&2
    exit 1
  fi

  # A staged deletion leaves the working-tree file available to generators.
  # Check the index as well so hooks fail before a commit can remove the
  # authoritative configuration while generation still appears successful.
  if git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    if ! git -C "$repo_dir" ls-files --error-unmatch -- sqlc.yaml >/dev/null 2>&1; then
      echo "authoritative SQLC config is absent from the index: $sqlc_config_path" >&2
      exit 1
    fi
  fi
}

require_sqlc_config

staged_path_exists() {
  staged_tree=$1
  staged_path=$2
  staged_entry=$(git -C "$repo_dir" ls-tree -r --name-only "$staged_tree" -- "$staged_path")
  [ "$staged_entry" = "$staged_path" ]
}

run_staged_check() {
  staged_tree=$(git -C "$repo_dir" write-tree)
  if ! staged_path_exists "$staged_tree" sqlc.yaml; then
    echo "authoritative SQLC config is absent from the staged tree: $repo_dir/sqlc.yaml" >&2
    exit 1
  fi
  if ! staged_path_exists "$staged_tree" scripts/generate.sh; then
    echo "generation script is absent from the staged tree: $repo_dir/scripts/generate.sh" >&2
    exit 1
  fi
  staged_script_blob=$(git -C "$repo_dir" rev-parse "$staged_tree:scripts/generate.sh")
  working_script_blob=$(git -C "$repo_dir" hash-object --path=scripts/generate.sh -- "$repo_dir/scripts/generate.sh")
  if [ "$staged_script_blob" != "$working_script_blob" ]; then
    echo "staged generation script differs from the working script: $repo_dir/scripts/generate.sh" >&2
    exit 1
  fi
  staged_script_mode=$(git -C "$repo_dir" ls-tree "$staged_tree" -- scripts/generate.sh | awk '{ print $1 }')
  if [ "$staged_script_mode" != 100755 ] || [ ! -x "$repo_dir/scripts/generate.sh" ]; then
    echo "staged generation script is not executable: $repo_dir/scripts/generate.sh" >&2
    exit 1
  fi

  staged_root=$tmp_dir/staged-root
  staged_archive=$tmp_dir/staged-tree.tar
  mkdir -p "$staged_root"
  git -C "$repo_dir" archive --format=tar "$staged_tree" >"$staged_archive"
  tar -xf "$staged_archive" -C "$staged_root"

  # Verify that archive extraction preserved the staged generator byte-for-
  # byte, then execute that executable directly. The blob and mode checks
  # above make a staged generator differing from the current worktree fail
  # before this point; no worktree script is copied into the snapshot.
  archived_script_blob=$(git -C "$repo_dir" hash-object -- "$staged_root/scripts/generate.sh")
  if [ "$staged_script_blob" != "$archived_script_blob" ]; then
    echo "staged generation script changed while creating the snapshot: $repo_dir/scripts/generate.sh" >&2
    exit 1
  fi
  MASTARR_GENERATION_SNAPSHOT=1 "$staged_root/scripts/generate.sh" --check
}

if [ "$mode" = check ] && [ -z "${MASTARR_GENERATION_SNAPSHOT:-}" ]; then
  if git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    run_staged_check
    exit 0
  fi
fi

make_config() {
  config_source=$1
  config_destination=$2
  generated_output=$3

  awk -v output="$generated_output" '
    /^output:[[:space:]]*/ { print "output: " output; found = 1; next }
    { print }
    END { if (!found) print "output: " output }
  ' "$config_source" >"$config_destination"
}

bundle_openapi() {
  bundle_expected_output=$repo_dir/api/openapi.yaml
  if [ "$mode" = write ]; then
    bundle_candidate_output=$bundle_expected_output
  else
    bundle_candidate_output=$tmp_dir/openapi.yaml
  fi

  (
    cd "$tools_dir"
    GOWORK=off go run ./internal/openapi-bundle \
      --input "$repo_dir/api/fragments" \
      --output "$bundle_candidate_output"
  )

  if [ "$mode" = check ]; then
    if [ ! -f "$bundle_expected_output" ]; then
      echo "bundled OpenAPI document is missing: $bundle_expected_output" >&2
      exit 1
    fi
    if ! cmp -s "$bundle_candidate_output" "$bundle_expected_output"; then
      echo "bundled OpenAPI document is out of date: $bundle_expected_output" >&2
      exit 1
    fi
  fi
}

generate_oapi() {
  oapi_config=$1
  oapi_source_config=$2
  oapi_expected_output=$3
  oapi_contract=$4

  if [ "$mode" = write ]; then
    oapi_candidate_output=$oapi_expected_output
  else
    oapi_candidate_output=$tmp_dir/$(basename "$oapi_expected_output")
  fi

  mkdir -p "$(dirname -- "$oapi_candidate_output")"
  make_config "$oapi_source_config" "$oapi_config" "$oapi_candidate_output"
  (
    cd "$tools_dir"
    GOWORK=off go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
      -config "$oapi_config" "$oapi_contract"
  )

  if [ "$mode" = check ]; then
    if [ ! -f "$oapi_expected_output" ]; then
      echo "generated file is missing: $oapi_expected_output" >&2
      exit 1
    fi
    if ! cmp -s "$oapi_candidate_output" "$oapi_expected_output"; then
      echo "generated file is out of date: $oapi_expected_output" >&2
      exit 1
    fi
  fi
}

bundle_openapi

generate_oapi \
  "$tmp_dir/api-oapi-codegen.yaml" \
  "$repo_dir/api/oapi-codegen.yaml" \
  "$repo_dir/internal/api/generated/server.gen.go" \
  "$repo_dir/api/openapi.yaml"

generate_oapi \
  "$tmp_dir/ui-oapi-codegen.yaml" \
  "$repo_dir/ui/oapi-codegen.yaml" \
  "$repo_dir/ui/internal/api/generated/client.gen.go" \
  "$repo_dir/api/openapi.yaml"

# The standalone qBittorrent module keeps raw generated transport under its
# module-internal package. Keep a short fallback while that migration lands so
# an older checkout can still regenerate its published path.
qbittorrent_generated_output="$repo_dir/clients/qbittorrent/generated/client.gen.go"
if [ -f "$repo_dir/clients/qbittorrent/internal/generated/client.gen.go" ]; then
  qbittorrent_generated_output="$repo_dir/clients/qbittorrent/internal/generated/client.gen.go"
fi

generate_oapi \
  "$tmp_dir/qbittorrent-oapi-codegen.yaml" \
  "$repo_dir/clients/qbittorrent/oapi-codegen.yaml" \
  "$qbittorrent_generated_output" \
  "$repo_dir/clients/qbittorrent/openapi.yaml"

generate_nzbget() {
  nzbget_expected_output=$repo_dir/clients/nzbget/generated.go
  if [ "$mode" = write ]; then
    nzbget_candidate_output=$nzbget_expected_output
  else
    nzbget_candidate_output=$tmp_dir/nzbget-generated.go
  fi

  (
    cd "$tools_dir"
    GOWORK=off go run -mod=readonly ./internal/nzbgetgen \
      -input "$repo_dir/clients/nzbget/openrpc.json" \
      -output "$nzbget_candidate_output"
  )

  if [ "$mode" = check ]; then
    if [ ! -f "$nzbget_expected_output" ]; then
      echo "generated file is missing: $nzbget_expected_output" >&2
      exit 1
    fi
    if ! cmp -s "$nzbget_candidate_output" "$nzbget_expected_output"; then
      echo "generated file is out of date: $nzbget_expected_output" >&2
      exit 1
    fi
  fi
}

generate_nzbget

generate_envdoc() {
  envdoc_expected_output=$repo_dir/docs/generated/environment.md
  if [ "$mode" = write ]; then
    envdoc_candidate_output=$envdoc_expected_output
  else
    envdoc_candidate_output=$tmp_dir/environment.md
  fi

  mkdir -p "$(dirname -- "$envdoc_candidate_output")"
  envdoc_line=$(awk '/^\/\/go:generate .*envdoc/ { print NR; exit }' "$repo_dir/internal/bootstrap/environment.go")
  (
    cd "$repo_dir/internal/bootstrap"
    GOFILE=environment.go GOLINE="$envdoc_line" GOWORK=off \
      go tool -modfile=../../tools/go.mod github.com/g4s8/envdoc \
      -output "$envdoc_candidate_output" -types=Environment
  )
  awk '{ lines[NR] = $0 } END { n = NR; for (; n > 0 && lines[n] == ""; n--) {} for (i = 1; i <= n; i++) print lines[i] }' \
    "$envdoc_candidate_output" >"$envdoc_candidate_output.tmp"
  mv "$envdoc_candidate_output.tmp" "$envdoc_candidate_output"

  if [ "$mode" = check ]; then
    if [ ! -f "$envdoc_expected_output" ]; then
      echo "generated file is missing: $envdoc_expected_output" >&2
      exit 1
    fi
    if ! cmp -s "$envdoc_candidate_output" "$envdoc_expected_output"; then
      echo "generated file is out of date: $envdoc_expected_output" >&2
      exit 1
    fi
  fi
}

generate_envdoc

generate_sqlc() {
  sqlc_expected_output=$repo_dir/internal/storage/sqlc
  if [ "$mode" = write ]; then
    (
      cd "$tools_dir"
      GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate \
        -f "$repo_dir/sqlc.yaml"
    )
    return
  fi

  # Generate into a fresh copy of the SQLC inputs.  Running `sqlc generate`
  # against the checkout would repair a stale worktree and make --check
  # unable to detect stale staged output.
  sqlc_candidate_root=$tmp_dir/sqlc-root
  mkdir -p "$sqlc_candidate_root/internal/storage"
  cp "$repo_dir/sqlc.yaml" "$sqlc_candidate_root/sqlc.yaml"
  cp -R "$repo_dir/migrations" "$sqlc_candidate_root/migrations"
  cp "$repo_dir/internal/storage/query.sql" "$sqlc_candidate_root/internal/storage/query.sql"
  (
    cd "$tools_dir"
    GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate \
      -f "$sqlc_candidate_root/sqlc.yaml"
  )

  sqlc_candidate_output=$sqlc_candidate_root/internal/storage/sqlc
  if [ ! -d "$sqlc_expected_output" ]; then
    echo "SQLC output directory is missing: $sqlc_expected_output" >&2
    exit 1
  fi
  if [ ! -d "$sqlc_candidate_output" ]; then
    echo "SQLC generated no output directory: $sqlc_expected_output" >&2
    exit 1
  fi
  if ! diff -ru "$sqlc_candidate_output" "$sqlc_expected_output" >/dev/null; then
    echo "SQLC generated output is out of date: $sqlc_expected_output" >&2
    exit 1
  fi
}

generate_sqlc

if [ "$mode" = check ]; then
  echo "generation checks passed"
else
  echo "generation completed"
fi
