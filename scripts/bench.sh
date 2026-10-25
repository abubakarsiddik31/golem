#!/usr/bin/env bash
# Benchmark runner for the three suites whose cost users feel directly:
# the runner loop, PDF extraction, and Office document extraction.
#
# Benchmarks are not part of the CI gate — absolute numbers vary by
# machine and would make it flaky. Run this before and after a change
# that could move them, and compare with benchstat:
#
#   scripts/bench.sh > before.txt
#   # ... change ...
#   scripts/bench.sh > after.txt
#   benchstat before.txt after.txt
#
# Usage: scripts/bench.sh [extra go test flags]
set -euo pipefail

cd "$(dirname "$0")/.."

go test ./internal/runner ./pdfextract ./docextract \
  -run '^$' \
  -bench . \
  -benchmem \
  -count 3 \
  "$@"
