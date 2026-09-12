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

check_client_architecture() {
  python3 - "$repo_dir" <<'PY'
from __future__ import annotations

import pathlib
import re
import sys


root = pathlib.Path(sys.argv[1])
project_prefix = "github.com/guilycst/mastarr/"
root_module = project_prefix.rstrip("/")
violations: list[str] = []

for path in sorted((root / "clients").rglob("*.go")):
    relative = path.relative_to(root).as_posix()
    client_name = relative.split("/", 2)[1]
    lines = path.read_text(encoding="utf-8").splitlines()
    index = 0
    imported: list[str] = []
    while index < len(lines):
        line = lines[index]
        single = re.match(r"^\s*import\s+\"([^\"]+)\"", line)
        if single:
            imported.append(single.group(1))
            index += 1
            continue
        if re.match(r"^\s*import\s*\(", line):
            index += 1
            while index < len(lines) and not re.match(r"^\s*\)\s*$", lines[index]):
                match = re.search(r'\"([^\"]+)\"', lines[index])
                if match:
                    imported.append(match.group(1))
                index += 1
        index += 1
    for dependency in imported:
        blocked = dependency == root_module or dependency.startswith(project_prefix)
        if dependency.startswith(project_prefix + "clients/"):
            imported_client = dependency[len(project_prefix + "clients/") :].split("/", 1)[0]
            blocked = imported_client != client_name
        if blocked:
            violations.append(
                f"{relative}: standalone clients must not import Mastarr root "
                f"modules, internal modules or another client: {dependency}"
            )

if violations:
    print("standalone client import boundary violations:", file=sys.stderr)
    print("\n".join(f"- {violation}" for violation in violations), file=sys.stderr)
    raise SystemExit(1)
print("standalone client import boundaries passed")
PY
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
  python3 "$repo_dir/scripts/check-architecture.py"
  check_client_architecture
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
