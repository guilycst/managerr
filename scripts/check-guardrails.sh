#!/bin/sh

set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mode=fast

case "${1:-}" in
  ""|--fast) mode=fast ;;
  --ci) mode=ci ;;
  --generation) mode=generation ;;
  *)
    echo "usage: $0 [--fast|--ci|--generation]" >&2
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

run_staged_generation() {
  if ! git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "staged generation requires a Git worktree: $repo_dir" >&2
    exit 1
  fi

  staged_generation_tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/mastarr-staged-generation.XXXXXX")
  trap 'rm -rf -- "$staged_generation_tmp_dir"' EXIT INT TERM

  staged_tree=$(git -C "$repo_dir" write-tree)
  staged_script=$(git -C "$repo_dir" ls-tree -r --name-only "$staged_tree" -- scripts/generate.sh)
  if [ "$staged_script" != scripts/generate.sh ]; then
    echo "generation script is absent from the staged tree: $repo_dir/scripts/generate.sh" >&2
    exit 1
  fi
  staged_config=$(git -C "$repo_dir" ls-tree -r --name-only "$staged_tree" -- sqlc.yaml)
  if [ "$staged_config" != sqlc.yaml ]; then
    echo "authoritative SQLC config is absent from the staged tree: $repo_dir/sqlc.yaml" >&2
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

  staged_root=$staged_generation_tmp_dir/staged-root
  staged_archive=$staged_generation_tmp_dir/staged-tree.tar
  mkdir -p "$staged_root"
  git -C "$repo_dir" archive --format=tar "$staged_tree" >"$staged_archive"
  tar -xf "$staged_archive" -C "$staged_root"

  archived_script_blob=$(git -C "$repo_dir" hash-object -- "$staged_root/scripts/generate.sh")
  if [ "$staged_script_blob" != "$archived_script_blob" ]; then
    echo "staged generation script changed while creating the snapshot: $repo_dir/scripts/generate.sh" >&2
    exit 1
  fi
  MASTARR_GENERATION_SNAPSHOT=1 "$staged_root/scripts/generate.sh" --check
}

check_staged_generation_identity() {
  if ! git -C "$repo_dir" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    return
  fi

  # Guard the aggregate entrypoint before check-api invokes generation.  A
  # partially staged generator must never be allowed to validate the staged
  # tree with a different worktree script: that would make pre-commit and CI
  # depend on bytes which are not part of the candidate commit.
  staged_tree=$(git -C "$repo_dir" write-tree)
  staged_script=$(git -C "$repo_dir" ls-tree -r --name-only "$staged_tree" -- scripts/generate.sh)
  if [ "$staged_script" != scripts/generate.sh ]; then
    echo "generation script is absent from the staged tree: $repo_dir/scripts/generate.sh" >&2
    exit 1
  fi
  staged_config=$(git -C "$repo_dir" ls-tree -r --name-only "$staged_tree" -- sqlc.yaml)
  if [ "$staged_config" != sqlc.yaml ]; then
    echo "authoritative SQLC config is absent from the staged tree: $repo_dir/sqlc.yaml" >&2
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
}

run_fast() {
  check_staged_generation_identity
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

if [ "$mode" = generation ]; then
  run_staged_generation
  echo "staged generation checks passed"
  exit 0
fi

run_fast

if [ "$mode" = ci ]; then
  "$repo_dir/scripts/check-lint.sh"

  for module_dir in $module_dirs; do
    run_module_checks "$module_dir"
  done
fi

echo "guardrail checks passed ($mode)"
