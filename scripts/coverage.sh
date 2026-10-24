#!/usr/bin/env bash
# Statement-coverage gate for the library packages.
#
# Examples are main packages with no tests, so they are dropped from the
# profile: counting them would report 0% for programs that exist to be run,
# not imported. The total below covers every package users can import.
#
# The floor is a ratchet: raise it when coverage rises, never lower it to
# land a change.
#
# Usage: scripts/coverage.sh [minimum-percent]   # default: 77.5
set -euo pipefail

cd "$(dirname "$0")/.."

minimum="${1:-77.5}"
module=$(go list -m)
profile=$(mktemp)
trap 'rm -f "$profile" "$profile.lib"' EXIT

go test ./... -coverprofile="$profile"
grep -v "^${module}/examples/" "$profile" > "$profile.lib"

total=$(go tool cover -func="$profile.lib" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')

printf 'library statement coverage: %s%% (minimum %s%%)\n' "$total" "$minimum"

awk -v total="$total" -v min="$minimum" 'BEGIN { exit !(total + 0 >= min + 0) }' || {
  echo "coverage ${total}% is below the ${minimum}% floor" >&2
  exit 1
}
