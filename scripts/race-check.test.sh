#!/usr/bin/env bash
# Explicit race arguments stay intact and temporary wrappers leave no files.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT
fake="$temporary/go"
cat > "$fake" <<'FAKE_GO'
#!/usr/bin/env bash
set -euo pipefail
test "$1" = build
test "$2" = -o
test "$4" = ./internal/citools/raceplan
printf '%s\n' "$3" > "$FAKE_RACE_RUNNER_PATH"
if [ "${FAKE_RACE_BUILD_STATUS:-0}" -ne 0 ]; then exit "$FAKE_RACE_BUILD_STATUS"; fi
cat > "$3" <<'FAKE_RUNNER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\0' "$@" > "$FAKE_RACE_ARGS"
exit "$FAKE_RACE_STATUS"
FAKE_RUNNER
chmod +x "$3"
FAKE_GO
chmod +x "$fake"
export FAKE_RACE_RUNNER_PATH="$temporary/runner-path" FAKE_RACE_ARGS="$temporary/args"
expected_args=(-go "$fake" -timeout 5m -direct -- -p 1 -run '^(TestOne|TestTwo/sub)$' ./internal/auth -args '-custom=value with spaces')
printf '%s\0' "${expected_args[@]}" > "$temporary/expected-args"
check_cleanup() {
  local runner_path
  runner_path=$(cat "$FAKE_RACE_RUNNER_PATH")
  test ! -e "$runner_path"
  test ! -d "$(dirname "$runner_path")"
}
for status in 0 23; do
  actual=0
  FAKE_RACE_STATUS=$status RACE_TIMEOUT=5m GO="$fake" bash "$root/scripts/race-check.sh" \
    -p 1 -run '^(TestOne|TestTwo/sub)$' ./internal/auth -args '-custom=value with spaces' \
    > "$temporary/output" 2>&1 || actual=$?
  test "$actual" -eq "$status"
  cmp "$temporary/expected-args" "$FAKE_RACE_ARGS"
  check_cleanup
done
actual=0
FAKE_RACE_BUILD_STATUS=73 FAKE_RACE_STATUS=0 GO="$fake" bash "$root/scripts/race-check.sh" ./internal/auth \
  > "$temporary/output" 2>&1 || actual=$?
test "$actual" -eq 73
check_cleanup
printf 'Explicit race arguments, subprocess failures and wrapper cleanup passed.\n'
