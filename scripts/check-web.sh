#!/usr/bin/env bash
# Execute the same selected Web checks locally and in CI.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root/web"
npm_command=${NPM:-npm}
go_command=${GO:-go}
component=${1:-all}
group=${2:-all}
shard=${3:-all}
playwright_args=()
browser_status=0
if [ "${CI:-}" = true ]; then
  mkdir -p "$root/.cache/ci"
  playwright_args=(--reporter=line,json --max-failures=0)
fi
run_browser() {
  local status=0
  "$@" || status=$?
  if [ "$status" -ne 0 ]; then
    if [ "${CI:-}" != true ]; then return "$status"; fi
    browser_status=1
  fi
}
selection=$(mktemp)
fixture=
cleanup() {
  rm -f -- "$selection"
  if [ -n "$fixture" ]; then rm -f -- "$fixture"; fi
}
trap cleanup EXIT
select_values() {
  node "$root/scripts/browser-plan.mjs" "$@" > "$selection"
  mapfile -d '' -t selected < "$selection"
}
case "$component" in unit|browser|all) ;; *) exit 2 ;; esac
if [ "$component" = unit ] || [ "$component" = all ]; then
  node --test "$root/scripts/browser-plan.test.mjs" "$root/scripts/check-risk.test.mjs" "$root/scripts/ci-metrics.test.mjs" "$root/scripts/update-verification-timings.test.mjs" "$root/scripts/ci-station-build.test.mjs"
  select_values related
  if [ "${#selected[@]}" -eq 0 ]; then
    "$npm_command" test
  else
    "$npm_command" run check:copy
    "$npm_command" run test:node
    "$npm_command" exec -- vitest related --run --passWithNoTests "${selected[@]}"
    "$npm_command" run test:assets
  fi
fi
if [ "$component" = browser ] || [ "$component" = all ]; then
  export PLAYWRIGHT_HTML_OPEN=never PLAYWRIGHT_NO_COPY_PROMPT=1
  if [ "$group" = all ] || [ "$group" = smoke ]; then
    select_values engine
    if [ "${selected[0]}" = true ] && [ "$shard" != 2/2 ]; then
      run_browser node test/fatfish-engine/check.mjs chromium
    fi
    select_values specs "$shard"
    if [ "${#selected[@]}" -gt 0 ]; then
      export PLAYWRIGHT_JSON_OUTPUT_FILE="$root/.cache/ci/browser-smoke-${shard//\//-}.json"
      run_browser "$npm_command" exec -- playwright test "${selected[@]}" "${playwright_args[@]}"
    fi
  fi
  if [ "$group" != smoke ]; then
    select_values suites "$group"
    if [ "${#selected[@]}" -gt 0 ]; then
      if [ -z "${NONBIRI_BROWSER_FIXTURE_BINARY:-}" ]; then
        fixture=$(mktemp)
        case "${OSTYPE:-}" in msys*|cygwin*) rm -f -- "$fixture"; fixture="$fixture.exe" ;; esac
        CGO_ENABLED=0 "$go_command" test -c -tags dist -o "$fixture" ../internal/app
        export NONBIRI_BROWSER_FIXTURE_BINARY="$fixture"
        if command -v cygpath >/dev/null 2>&1; then
          NONBIRI_BROWSER_FIXTURE_BINARY=$(cygpath -m "$fixture")
        fi
      fi
      for suite in "${selected[@]}"; do
        export PLAYWRIGHT_JSON_OUTPUT_FILE="$root/.cache/ci/browser-$suite.json"
        CGO_ENABLED=0 run_browser "$npm_command" exec -- playwright test -c "playwright.$suite.config.ts" "${playwright_args[@]}"
      done
    fi
  fi
fi
exit "$browser_status"
