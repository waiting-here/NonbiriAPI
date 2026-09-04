#!/usr/bin/env bash
# Race detector gate for concurrency-/cancellation-heavy packages (especially
# egress and secret). Go's -race requires cgo, so this enables cgo with the
# platform's available C compiler.
#
# Production builds stay CGO_ENABLED=0 pure-Go (modernc.org/sqlite needs no cgo).
# The cgo + compiler configuration here is test-only and does not touch the
# production binary.
#
# Same node_modules exclusion as check-go.sh (npm deps ship Go source without a
# go.mod; `go ./...` would otherwise absorb them — see check-go.sh comment).
#
# Exit codes preserved: set -e + pipefail propagate go's real status.
# SQLite-heavy packages can legitimately take more than Go's default 10-minute
# per-package deadline under race instrumentation on shared CI runners, so keep
# an explicit bounded deadline with an override for diagnosis.
#
# With no arguments this remains the complete local gate. Package arguments
# remain supported for targeted diagnosis. CI uses --shard N/6; that mode
# derives the live package/test catalog and uses timing data only for balance.

set -euo pipefail

GO="${GO:-go}"
RACE_TIMEOUT="${RACE_TIMEOUT:-30m}"

cd "$(dirname "$0")/.."

export CGO_ENABLED=1
if [ -z "${CC:-}" ]; then
  export CC=gcc
fi

if [ "${1:-}" = "--shard" ]; then
  if [ "$#" -ne 2 ]; then
    echo "usage: scripts/race-check.sh --shard N/6" >&2
    exit 2
  fi
  "$GO" run ./internal/citools/raceplan \
    -go "$GO" \
    -shard "$2" \
    -timeout "$RACE_TIMEOUT"
elif [ $# -gt 0 ]; then
  "$GO" test -race -count=1 -timeout="$RACE_TIMEOUT" "$@"
else
  pkgs="$("$GO" list ./... | grep -v '/node_modules/')"
  "$GO" test -race -count=1 -timeout="$RACE_TIMEOUT" $pkgs
fi
