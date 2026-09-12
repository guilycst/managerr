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
import sys


root = pathlib.Path(sys.argv[1])
project_prefix = "github.com/guilycst/mastarr/"
root_module = project_prefix.rstrip("/")
violations: list[str] = []


def _go_tokens(source: str) -> list[tuple[str, str]]:
    """Tokenize enough Go syntax to read import declarations safely.

    The architecture check runs independently of compilation, so it cannot
    rely on package loading to discover imports.  This small lexer skips Go
    comments and literals and retains identifiers, punctuation, and import
    strings.  In particular, it handles named, blank, and dot aliases in both
    single-line and grouped declarations.
    """
    tokens: list[tuple[str, str]] = []
    index = 0
    while index < len(source):
        character = source[index]
        if character in " \t\r":
            index += 1
            continue
        if character == "\n":
            tokens.append(("newline", character))
            index += 1
            continue
        if source.startswith("//", index):
            newline = source.find("\n", index + 2)
            index = len(source) if newline < 0 else newline
            continue
        if source.startswith("/*", index):
            end = source.find("*/", index + 2)
            index = len(source) if end < 0 else end + 2
            continue
        if character in ('"', "`", "'"):
            quote = character
            index += 1
            value_start = index
            while index < len(source):
                if quote == '"' and source[index] == "\\":
                    index += 2
                    continue
                if source[index] == quote:
                    break
                index += 1
            value = source[value_start:index]
            if quote != "'":
                tokens.append(("string", value))
            if index < len(source):
                index += 1
            continue
        if character == "_" or character.isalpha():
            end = index + 1
            while end < len(source) and (source[end] == "_" or source[end].isalnum()):
                end += 1
            tokens.append(("ident", source[index:end]))
            index = end
            continue
        tokens.append((character, character))
        index += 1
    return tokens


def _go_imports(source: str) -> list[str]:
    tokens = _go_tokens(source)
    imports: list[str] = []
    index = 0
    while index < len(tokens):
        kind, value = tokens[index]
        if kind != "ident" or value != "import":
            index += 1
            continue
        index += 1
        if index < len(tokens) and tokens[index][0] == "(":
            depth = 1
            index += 1
            while index < len(tokens) and depth:
                kind, value = tokens[index]
                if kind == "(":
                    depth += 1
                elif kind == ")":
                    depth -= 1
                elif kind == "string" and depth == 1:
                    imports.append(value)
                index += 1
            continue
        while index < len(tokens) and tokens[index][0] not in ("newline", ";"):
            kind, value = tokens[index]
            if kind == "string":
                imports.append(value)
                break
            index += 1
        continue
    return imports


for path in sorted((root / "clients").rglob("*.go")):
    relative = path.relative_to(root).as_posix()
    client_name = relative.split("/", 2)[1]
    imported = _go_imports(path.read_text(encoding="utf-8"))
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
