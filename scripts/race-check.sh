#!/usr/bin/env bash
# Race detector gate for concurrency-/cancellation-heavy packages (esp. 命脉
# tracks: egress, secret). Go's -race requires cgo, so this enables cgo with
# the mingw64 gcc toolchain.
#
# Production builds stay CGO_ENABLED=0 pure-Go (DEC-002 / modernc.org/sqlite needs
# no cgo). The cgo + gcc here is test-only and does not touch the production binary.
#
# Same node_modules exclusion as check-go.sh (npm deps ship Go source without a
# go.mod; `go ./...` would otherwise absorb them — see check-go.sh comment).
#
# Exit codes preserved: set -e + pipefail propagate go's real status.

set -euo pipefail

GO="${GO:-G:/code/NonbiriAPI/.pi/go/bin/go}"

cd "$(dirname "$0")/.."

export CGO_ENABLED=1
export CC="G:/code/mingw64/bin/gcc.exe"
export PATH="G:/code/mingw64/bin:$PATH"

if [ $# -gt 0 ]; then
  "$GO" test -race "$@"
else
  pkgs="$("$GO" list ./... | grep -v '/node_modules/')"
  "$GO" test -race $pkgs
fi