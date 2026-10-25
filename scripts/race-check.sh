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
# With no arguments this is the complete local concurrency-risk gate.
# The ordinary check-go.sh gate retains every excluded serial/rule test. Package arguments
# remain supported for targeted diagnosis. CI uses --shard N/TOTAL; that mode
# derives the live package/test catalog and uses timing data only for balance.

set -euo pipefail

GO="${GO:-go}"
RACE_TIMEOUT="${RACE_TIMEOUT:-30m}"
RACE_WORKERS="${RACE_WORKERS:-1}"

cd "$(dirname "$0")/.."

export CGO_ENABLED=1
if [ -z "${CC:-}" ]; then
  export CC=gcc
fi

if [ "${1:-}" = "--prepare" ]; then
  test "$#" -ge 2
  directory=$2
  shift 2
  exec "$GO" run ./internal/citools/raceplan -go "$GO" -prepare "$directory" -workers "$RACE_WORKERS" -timeout "$RACE_TIMEOUT" "$@"
elif [ "${1:-}" = "--shard" ]; then
  test "$#" -ge 2
  shard=$2
  shift 2
  selected_args=()
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --workers)
        test "$#" -ge 2
        RACE_WORKERS=$2
        shift 2 ;;
      --prepared)
        test "$#" -ge 2
        selected_args+=(-prepared "$2")
        shift 2 ;;
      --packages)
        test "$#" -ge 2
        selected_args+=(-packages "$2")
        shift 2 ;;
      *) echo "usage: scripts/race-check.sh --shard N/TOTAL [--workers N] [--packages 'exact paths'] [--prepared DIR]" >&2; exit 2 ;;
    esac
  done
  "$GO" run ./internal/citools/raceplan \
    -go "$GO" \
    -shard "$shard" \
    -timeout "$RACE_TIMEOUT" \
    -workers "$RACE_WORKERS" "${selected_args[@]}"
elif [ $# -gt 0 ]; then
  # Build the small wrapper so go run cannot replace a child's exit status.
  # Each invocation owns its executable and the runner removes its DB template.
  runner_directory=$(mktemp -d)
  runner="$runner_directory/raceplan"
  case "${OSTYPE:-}" in msys*|cygwin*) runner="$runner.exe" ;; esac
  cleanup_direct_runner() {
    local status=$?
    trap - EXIT
    if ! rm -f -- "$runner" || ! rmdir -- "$runner_directory"; then
      if [ "$status" -eq 0 ]; then status=1; fi
    fi
    exit "$status"
  }
  trap cleanup_direct_runner EXIT
  "$GO" build -o "$runner" ./internal/citools/raceplan
  "$runner" -go "$GO" -timeout "$RACE_TIMEOUT" -direct -- "$@"
else
  # Full means every ordinary test plus the complete risk-based race catalog.
  # A single local shard uses the same exact exclusions as CI.
  "$GO" run ./internal/citools/raceplan -go "$GO" -shard 1/1 \
    -timeout "$RACE_TIMEOUT" -workers "$RACE_WORKERS"
fi
