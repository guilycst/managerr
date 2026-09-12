#!/bin/sh

set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
lint_tool=github.com/golangci/golangci-lint/v2/cmd/golangci-lint
lint_mode=full

case "${1:-}" in
  "") ;;
  --architecture-only) lint_mode=architecture ;;
  *)
    echo "usage: $0 [--architecture-only]" >&2
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

run_lint() {
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
    GOWORK=off go tool -modfile="$repo_dir/tools/go.mod" "$lint_tool" \
      run \
      --config "$repo_dir/.golangci.yml" \
      --timeout=5m \
      ./...
  )
}

if [ "$lint_mode" = full ]; then
  for module_dir in $module_dirs; do
    run_lint "$module_dir"
  done
fi

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

python3 "$repo_dir/scripts/check-architecture.py"
check_client_architecture

if [ "$lint_mode" = architecture ]; then
  echo "Go architecture checks passed"
else
  echo "Go lint and architecture checks passed"
fi
