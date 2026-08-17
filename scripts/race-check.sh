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

set -euo pipefail

GO="${GO:-go}"

cd "$(dirname "$0")/.."

export CGO_ENABLED=1
if [ -z "${CC:-}" ]; then
  export CC=gcc
fi

if [ $# -gt 0 ]; then
  "$GO" test -race "$@"
else
  pkgs="$("$GO" list ./... | grep -v '/node_modules/')"
  "$GO" test -race $pkgs
fi