#!/bin/sh

set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
git -C "$repo_dir" config core.hooksPath .githooks
echo "installed versioned hooks for $repo_dir"
