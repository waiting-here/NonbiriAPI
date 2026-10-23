#!/usr/bin/env bash
# Verify the canonical Go gate preserves its live list and subprocess failures.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT
fake="$temporary/go"
cat > "$fake" <<'FAKE_GO'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_GO_LOG"
if [ "${FAKE_GO_FAIL:-}" = "$1" ]; then exit 73; fi
case "$1" in
  list) printf '%s\n' example/fast example/node_modules/dependency example/slow ;;
  build|vet|test) ;;
  run)
    cat > "$FAKE_GO_INPUT"
    printf '%s\n' example/slow example/fast
    ;;
  *) exit 79 ;;
esac
FAKE_GO
chmod +x "$fake"
export FAKE_GO_LOG="$temporary/calls" FAKE_GO_INPUT="$temporary/input"
GO="$fake" bash "$root/scripts/check-go.sh" > "$temporary/output" 2>&1
cat > "$temporary/expected-calls" <<'EXPECTED'
list ./...
build example/fast example/slow
vet example/fast example/slow
run ./internal/citools/testorder
test -timeout=30m example/slow example/fast
EXPECTED
diff -u "$temporary/expected-calls" "$FAKE_GO_LOG"
printf '%s\n' example/fast example/slow > "$temporary/expected-input"
diff -u "$temporary/expected-input" "$FAKE_GO_INPUT"

for stage in list build vet run test; do
  : > "$FAKE_GO_LOG"
  if FAKE_GO_FAIL="$stage" GO="$fake" bash "$root/scripts/check-go.sh" > "$temporary/output" 2>&1; then
    printf 'Go gate hid a %s failure\n' "$stage" >&2
    exit 1
  else
    actual=$?
    if [ "$actual" -ne 73 ]; then
      printf 'Go gate changed %s exit code: %s\n' "$stage" "$actual" >&2
      cat "$temporary/output" >&2
      exit 1
    fi
  fi
  last=$(tail -n 1 "$FAKE_GO_LOG")
  if [ "${last%% *}" != "$stage" ]; then
    printf 'Go gate continued after %s failed: %s\n' "$stage" "$last" >&2
    exit 1
  fi
done
printf 'Go gate ordering and subprocess failures passed.\n'
