#!/usr/bin/env python3
"""Check the import direction that the module layout promises."""

from __future__ import annotations

import pathlib
import re
import sys


PROJECT_INTERNAL = "github.com/guilycst/mastarr/internal/"


def imports(path: pathlib.Path) -> list[str]:
    lines = path.read_text(encoding="utf-8").splitlines()
    found: list[str] = []
    index = 0
    while index < len(lines):
        line = lines[index]
        single = re.match(r"^\s*import\s+\"([^\"]+)\"", line)
        if single:
            found.append(single.group(1))
            index += 1
            continue
        if re.match(r"^\s*import\s*\(", line):
            index += 1
            while index < len(lines) and not re.match(r"^\s*\)\s*$", lines[index]):
                match = re.search(r'\"([^\"]+)\"', lines[index])
                if match:
                    found.append(match.group(1))
                index += 1
        index += 1
    return found


def violations(root: pathlib.Path) -> list[str]:
    rules: tuple[tuple[str, tuple[str, ...], str], ...] = (
        (
            "ui/",
            (PROJECT_INTERNAL,),
            "UI must not import root internals",
        ),
        (
            "internal/adapters/",
            (
                f"{PROJECT_INTERNAL}storage",
                f"{PROJECT_INTERNAL}workflow",
            ),
            "adapters must not import storage or workflow internals",
        ),
        (
            "internal/domain/",
            (
                f"{PROJECT_INTERNAL}adapters",
                f"{PROJECT_INTERNAL}bootstrap",
                f"{PROJECT_INTERNAL}configuration",
                f"{PROJECT_INTERNAL}filesystem",
                f"{PROJECT_INTERNAL}storage",
                f"{PROJECT_INTERNAL}workflow",
            ),
            "domain must not import adapters or infrastructure internals",
        ),
        (
            "internal/ports/",
            (
                f"{PROJECT_INTERNAL}adapters",
                f"{PROJECT_INTERNAL}bootstrap",
                f"{PROJECT_INTERNAL}configuration",
                f"{PROJECT_INTERNAL}filesystem",
                f"{PROJECT_INTERNAL}storage",
                f"{PROJECT_INTERNAL}workflow",
            ),
            "ports must not import adapters or infrastructure internals",
        ),
    )
    errors: list[str] = []
    for path in sorted(root.rglob("*.go")):
        relative = path.relative_to(root).as_posix()
        if any(part in {".git", "vendor", "node_modules"} for part in path.parts):
            continue
        for scope, blocked, message in rules:
            if not relative.startswith(scope):
                continue
            for imported in imports(path):
                if imported.startswith(blocked):
                    errors.append(f"{relative}: {message}: {imported}")
    return errors


def main() -> int:
    root = pathlib.Path(__file__).resolve().parent.parent
    errors = violations(root)
    if errors:
        print("architecture import boundary violations:", file=sys.stderr)
        print("\n".join(f"- {error}" for error in errors), file=sys.stderr)
        return 1
    print("architecture import boundaries passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
