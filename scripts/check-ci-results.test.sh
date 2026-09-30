#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
run_case() {
  local expected=$1 mode=$2 plan=$3 first=$4 first_result=$5 second=$6 second_result=$7 actual=0
  MODE=$mode PLAN_RESULT=$plan FIRST_APPLICABLE=$first FIRST_RESULT=$first_result \
    SECOND_APPLICABLE=$second SECOND_RESULT=$second_result \
    bash scripts/check-ci-results.sh > /dev/null 2>&1 || actual=$?
  if [ "$expected" = pass ]; then test "$actual" -eq 0
  else test "$actual" -ne 0
  fi
}
run_case pass full success true success true success
run_case pass routine success true success false skipped
run_case pass routine success false skipped false skipped
run_case fail full success true success false skipped
run_case fail routine failure false skipped false skipped
run_case fail routine cancelled false skipped false skipped
run_case fail routine success true failure false skipped
run_case fail routine success true cancelled false skipped
run_case fail routine success true skipped false skipped
run_case fail routine success false success false skipped
run_case fail routine success unknown skipped false skipped
run_case fail invalid success true success true success
printf 'Required summary failure propagation: passed\n'

check_third_child() {
  local mode=$1 applicable=$2 result=$3
  MODE=$mode PLAN_RESULT=success FIRST_APPLICABLE=true FIRST_RESULT=success \
    SECOND_APPLICABLE=true SECOND_RESULT=success \
    THIRD_APPLICABLE=$applicable THIRD_RESULT=$result \
    bash scripts/check-ci-results.sh > /dev/null 2>&1
}
for result in failure cancelled skipped missing; do
  if check_third_child full true "$result"; then
    printf 'Unexpected third-child success: %s\n' "$result" >&2
    exit 1
  fi
done
if check_third_child full true ""; then exit 1; fi
check_third_child full true success
check_third_child routine false skipped
printf 'Third-child failure propagation: passed\n'
