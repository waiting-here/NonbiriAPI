#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
previous_commit=5052edc86ee59f99e48a022c4b8ab56b3a44032f
test "$(git rev-parse "$previous_commit^{commit}")" = "$previous_commit"
temporary_base=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "$temporary_base/nonbiri-blackjack-upgrade.XXXXXXXX")
cleanup() {
    local resolved
    resolved=$(cd "$temporary" && pwd -P) || return
    case "$resolved" in
        "$temporary_base"/nonbiri-blackjack-upgrade.*) rm -rf -- "$resolved" ;;
        *) return 1 ;;
    esac
}
trap cleanup EXIT
mkdir "$temporary/previous" "$temporary/data"
git archive "$previous_commit" | tar -x -C "$temporary/previous"
"$python_command" - "$root" "$temporary" <<'PY'
import json, pathlib, sys
root, temporary = map(pathlib.Path, sys.argv[1:])
replacements = {str(temporary / "previous/previous_table_fixture_test.go"): str(root / "internal/db/testdata/previous_table_fixture_test.go.txt")}
(temporary / "overlay.json").write_text(json.dumps({"Replace": replacements}), encoding="utf-8", newline="\n")
PY
export CGO_ENABLED=0
export NONBIRI_PREVIOUS_TABLE_FIXTURES="$temporary/data"
(
    cd "$temporary/previous"
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/previous.test" .
    "$temporary/previous.test" -test.run '^TestWritePreviousTableFixtures$' -test.v
)
"$go_command" test -count=1 -v -run '^TestBlackjackUpgradeFromPreviousBinary$' ./internal/db
"$temporary/previous.test" -test.run '^TestPreviousBinaryRejectsBlackjackUpgrade$' -test.v
printf 'Previous source: %s\n' "$previous_commit"
"$go_command" version
sha256sum "$temporary/data/"*.db
