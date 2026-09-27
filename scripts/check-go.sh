#!/usr/bin/env bash
# Standard Go gate that excludes vendored JS dependency trees.
#
# Why this exists: the web frontend pulls JS deps under web/node_modules, and some
# of those packages ship Go source without their own go.mod. With no guarding
# module boundary, `go build ./...` recurses into them and absorbs them as
# module subpackages, which pollutes the module and makes the gate depend on JS
# deps that have nothing to do with the project. It is not acceptable for a
# JS-only dependency to be able to break the Go gate.
#
# Fix: enumerate the Go package list with `go list ./...`, drop every package
# whose import path crosses a /node_modules/ segment, then run build, vet, and
# test on the remainder. The web/node_modules tree is never edited (npm
# reinstalls would overwrite any change), so the exclusion is done here at the
# gate-command layer rather than by marking a nested module inside it.
#
# Exit codes of the go toolchain are preserved: package enumeration finishes
# before filtering, and each go subcommand runs under set -e so the script exits
# with go's real exit code on the first failure -- no truncating pipes or
# short-circuit masking of a non-zero status.

set -euo pipefail

# Use the Go toolchain on PATH by default; callers may pin one with GO.
GO="${GO:-go}"
# Populated migration matrices can exceed Go's default package deadline on
# shared runners. Keep an explicit bounded timeout without delaying fast tests.
GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-30m}"

export CGO_ENABLED=0

# Always run against the repository root regardless of the caller's CWD.
cd "$(dirname "$0")/.."

# Enumerate this module's packages, dropping anything under a /node_modules/
# segment. Keep the calls separate so an empty filter result cannot replace
# the exit code of a failed `go list`. Project packages must remain afterward.
pkgs="$("$GO" list ./...)"
pkgs="$(printf '%s\n' "$pkgs" | grep -v '/node_modules/')"

printf 'Go build started at %s\n' "$(date -u +%FT%TZ)"
check_go_started=$SECONDS
"$GO" build $pkgs
printf 'Go build completed in %s seconds\n' "$((SECONDS - check_go_started))"

printf 'Go vet started at %s\n' "$(date -u +%FT%TZ)"
check_go_started=$SECONDS
"$GO" vet $pkgs
printf 'Go vet completed in %s seconds\n' "$((SECONDS - check_go_started))"

printf 'Go tests started at %s\n' "$(date -u +%FT%TZ)"
check_go_started=$SECONDS
"$GO" test -timeout="$GO_TEST_TIMEOUT" $pkgs
printf 'Go tests completed in %s seconds\n' "$((SECONDS - check_go_started))"
