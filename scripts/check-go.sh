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
# Exit codes of the go toolchain are preserved: with pipefail, the `go list`
# pipeline must itself succeed, and each go subcommand runs under set -e so the
# script exits with go's real exit code on the first failure -- no truncating
# pipes or short-circuit masking of a non-zero status.

set -euo pipefail

# Use the Go toolchain on PATH by default; callers may pin one with GO.
GO="${GO:-go}"

# Always run against the repository root regardless of the caller's CWD.
cd "$(dirname "$0")/.."

# Enumerate this module's packages, dropping anything under a /node_modules/
# segment. grep -v returns 0 whenever it still emits at least one line (the
# project packages always remain), and under pipefail a `go list` failure
# propagates as a non-zero pipeline status before any build runs.
pkgs="$("$GO" list ./... | grep -v '/node_modules/')"

"$GO" build $pkgs
"$GO" vet $pkgs
"$GO" test $pkgs