#!/usr/bin/env bash
# Required summaries accept skipped children only when the live plan excludes
# them. A failed plan, failed child or unknown result never becomes green.
set -euo pipefail
test "${PLAN_RESULT:-}" = success
case "${MODE:-}" in routine|full) ;; *) exit 1 ;; esac
check_child() {
  local label=$1 applicable=$2 result=$3
  if [ "$MODE" = full ] && [ "$applicable" != true ]; then return 1; fi
  case "$applicable:$result" in
    true:success) printf '%s: applicable checks completed\n' "$label" ;;
    false:skipped) printf '%s: not applicable under the live plan\n' "$label" ;;
    *) printf '%s: unexpected plan/result (%s/%s)\n' "$label" "$applicable" "$result" >&2; return 1 ;;
  esac
}
check_child first "${FIRST_APPLICABLE:-}" "${FIRST_RESULT:-}"
check_child second "${SECOND_APPLICABLE:-}" "${SECOND_RESULT:-}"
if [ "${THIRD_APPLICABLE+x}" = x ]; then
  check_child third "$THIRD_APPLICABLE" "${THIRD_RESULT:-}"
fi
