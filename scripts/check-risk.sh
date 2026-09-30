#!/usr/bin/env bash
# Daily verification follows the live reverse dependency closure. Final
# candidates use --mode full; check-go.sh remains the complete Go entry.
set -euo pipefail
cd "$(dirname "$0")/.."
GO="${GO:-go}"
mode=routine
base=
head=
plan_only=false
component=all
while [ "$#" -gt 0 ]; do
  case "$1" in
    --mode|--base|--head|--component)
      test "$#" -ge 2
      case "$1" in
        --mode) mode=$2 ;;
        --base) base=$2 ;;
        --head) head=$2 ;;
        --component) component=$2 ;;
      esac
      shift 2 ;;
    --plan-only) plan_only=true; shift ;;
    *) printf 'Unknown option: %s\n' "$1" >&2; exit 2 ;;
  esac
done
case "$component" in all|go|race|web) ;; *) exit 2 ;; esac
scope=$(mktemp)
trap 'rm -f -- "$scope"' EXIT
CGO_ENABLED=0 "$GO" run ./internal/citools/riskplan -go "$GO" \
  -mode "$mode" -base "$base" -head "$head" -github-output "$scope"
if "$plan_only"; then exit 0; fi
go_packages=()
race_packages=()
web=false
upgrade=false
while IFS='=' read -r key value; do
  case "$key" in
    mode) mode=$value ;;
    go_packages) read -r -a go_packages <<< "$value" ;;
    race_packages) read -r -a race_packages <<< "$value" ;;
    web) web=$value ;;
    upgrade) upgrade=$value ;;
  esac
done
if [ "$component" = all ] || [ "$component" = go ]; then
  if [ "$mode" = full ]; then
    bash scripts/check-go.sh
  elif [ "${#go_packages[@]}" -gt 0 ]; then
    export CGO_ENABLED=0
    "$GO" build "${go_packages[@]}"
    "$GO" vet "${go_packages[@]}"
    "$GO" test -timeout="${GO_TEST_TIMEOUT:-30m}" "${go_packages[@]}"
  else
    printf 'Go: not applicable to this scope\n'
  fi
  if "$upgrade"; then bash scripts/check-upgrade.sh; fi
fi
if [ "$component" = all ] || [ "$component" = race ]; then
  if [ "${#race_packages[@]}" -gt 0 ]; then
    if [ "$mode" = full ]; then bash scripts/race-check.sh
    else bash scripts/race-check.sh "${race_packages[@]}"
    fi
  else
    printf 'Race: not executed; no applicable concurrency risk\n'
  fi
fi
if [ "$component" = all ] || [ "$component" = web ]; then
  if "$web"; then
    cd web
    npm_command="${NPM:-npm}"
    "$npm_command" test
    "$npm_command" run typecheck
    "$npm_command" run lint
    "$npm_command" run build
    git diff --exit-code -- THIRD_PARTY_NOTICES.md
  else
    printf 'Web: not applicable to this scope\n'
  fi
fi
