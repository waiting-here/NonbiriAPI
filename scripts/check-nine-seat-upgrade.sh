#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
previous_commit=dc52c535cf4a3865c553bd98a53ec38010a20b77
test "$(git rev-parse "$previous_commit^{commit}")" = "$previous_commit"
temporary_base=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "$temporary_base/nonbiri-nine-seat-upgrade.XXXXXXXX")
cleanup() {
    local resolved
    resolved=$(cd "$temporary" && pwd -P) || return
    case "$resolved" in
        "$temporary_base"/nonbiri-nine-seat-upgrade.*) rm -rf -- "$resolved" ;;
        *) return 1 ;;
    esac
}
trap cleanup EXIT
mkdir "$temporary/previous" "$temporary/data"
git archive "$previous_commit" | tar -x -C "$temporary/previous"
"$python_command" - "$root" "$temporary" <<'PY'
import json, pathlib, sys
root, temporary = map(pathlib.Path, sys.argv[1:])
replacements = {str(temporary / "previous/internal/game/blackjack/eight_seat_fixture_test.go"): str(root / "internal/db/testdata/previous_eight_seat_fixture_test.go.txt")}
(temporary / "overlay.json").write_text(json.dumps({"Replace": replacements}), encoding="utf-8", newline="\n")
PY
export CGO_ENABLED=0
export NONBIRI_NINE_SEAT_FIXTURES="$temporary/data"
(
    cd "$temporary/previous"
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/previous.test" ./internal/game/blackjack
    "$temporary/previous.test" -test.run '^TestWritePreviousEightSeatFixtures$' -test.v
)
"$go_command" test -count=1 -v -run '^TestNineSeatUpgradeFromPreviousBinary$' ./internal/db
"$temporary/previous.test" -test.run '^TestPreviousBinaryRejectsNineSeatUpgrade$' -test.v
printf 'Previous source: %s\n' "$previous_commit"
"$go_command" version
sha256sum "$temporary/data/"*.db
