#!/usr/bin/env bash
# Exported-API compatibility gate.
#
# Compares the working tree against a base git ref package by package and
# fails on any incompatible change. Additive changes pass silently, which
# is the rule the roadmap commits to once the v1 freeze lands.
#
# Requires apidiff on PATH:
#   go install golang.org/x/exp/cmd/apidiff@latest
#
# Usage: scripts/apidiff.sh [base-ref]   # default: origin/main
set -euo pipefail

cd "$(dirname "$0")/.."

base="${1:-origin/main}"
base_tree=$(mktemp -d)/base
exports=$(mktemp -d)
status=0

git worktree add --detach --quiet "$base_tree" "$base"
cleanup() {
  git worktree remove --force "$base_tree" >/dev/null 2>&1 || true
  rm -rf "$exports" "$(dirname "$base_tree")"
}
trap cleanup EXIT

packages=$(go list ./... | grep -v -e '/internal' -e '/examples')

for pkg in $packages; do
  # A package missing from the base is new, so it cannot break anything.
  if ! ( cd "$base_tree" && go list "$pkg" ) >/dev/null 2>&1; then
    continue
  fi
  ( cd "$base_tree" && apidiff -w "$exports/old" "$pkg" ) >/dev/null
  apidiff -w "$exports/new" "$pkg" >/dev/null

  changes=$(apidiff -incompatible "$exports/old" "$exports/new")
  if [ -n "$changes" ]; then
    printf 'incompatible API changes in %s:\n%s\n' "$pkg" "$changes"
    status=1
  fi
  rm -f "$exports/old" "$exports/new"
done

exit "$status"
