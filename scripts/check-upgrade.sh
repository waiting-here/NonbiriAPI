#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
released_commit=03456c51d7537e806fc1b4576fc7e9660dc3f696
attendance_commit=b1700f25d7a73535af76cdd15c356d92eff0d8fd
bash "$root/scripts/ensure-upgrade-source.sh" "$released_commit"
bash "$root/scripts/ensure-upgrade-source.sh" "$attendance_commit"
temporary_base=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "$temporary_base/nonbiri-upgrade.XXXXXXXX")
cleanup() {
    local resolved
    resolved=$(cd "$temporary" && pwd -P) || return
    case "$resolved" in
        "$temporary_base"/nonbiri-upgrade.*) rm -rf -- "$resolved" ;;
        *) return 1 ;;
    esac
}
trap cleanup EXIT
mkdir "$temporary/released" "$temporary/attendance"
mkdir -m 700 "$temporary/data"
git archive "$released_commit" -- go.mod go.sum internal | tar -x -C "$temporary/released"
"$python_command" - "$root" "$temporary" <<'PY'
import json, pathlib, sys
root, temporary = map(pathlib.Path, sys.argv[1:3])
released = temporary / "released"
replacements = {
    str(released / ("internal/ledger/" + name + ".go")):
        str(root / ("internal/db/testdata/" + name + ".go.txt"))
    for name in ["released_governance_facts_test", "released_upgrade_fixture_test", "released_economy_audit_fixture_test"]
}
(temporary / "overlay.json").write_text(json.dumps({"Replace": replacements}), encoding="utf-8", newline="\n")
PY
export CGO_ENABLED=0
export NONBIRI_RELEASED_FIXTURE="$temporary/data/wallet.db"
export NONBIRI_UPGRADE_FIXTURE="$NONBIRI_RELEASED_FIXTURE"
export NONBIRI_ECONOMY_AUDIT_FIXTURE="$NONBIRI_RELEASED_FIXTURE"
(
    # The released binary creates the populated source using its own schema.
    unset NONBIRI_RACE_TEMPLATE_PATH NONBIRI_RACE_TEMPLATE_SHA256 NONBIRI_RACE_TEMPLATE_ID
    cd "$temporary/released"
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-wallet.test" ./internal/ledger
    "$temporary/released-wallet.test" -test.run '^TestWriteReleasedUpgradeFixture$' -test.v -test.timeout 2m
)
export NONBIRI_UPGRADE_CLI="$temporary/upgrade.exe"
"$go_command" build -o "$NONBIRI_UPGRADE_CLI" ./cmd/nonbiri-upgrade-v1
"$go_command" test -count=1 -v -timeout=10m -run '^TestConvertReleasedDatabase$' ./internal/upgrade/v1
NONBIRI_STABLE_UPGRADED_FIXTURE="$NONBIRI_RELEASED_FIXTURE" "$temporary/released-wallet.test" -test.run '^TestReleasedRejectsStableSchema$' -test.v -test.timeout 2m
"$go_command" test -count=1 -v -timeout=2m -run '^TestEconomyAuditCatchUpFromReleasedBinary$' ./internal/economyaudit
git archive "$attendance_commit" -- go.mod go.sum internal | tar -x -C "$temporary/attendance"
cp "$root/internal/attendance/released_test.go" "$temporary/attendance/internal/attendance/"
export NONBIRI_RELEASED_ATTENDANCE_FIXTURE="$temporary/data/attendance.db"
(
    unset NONBIRI_RACE_TEMPLATE_PATH NONBIRI_RACE_TEMPLATE_SHA256 NONBIRI_RACE_TEMPLATE_ID
    cd "$temporary/attendance"
    "$go_command" test -c -o "$temporary/released-attendance.test" ./internal/attendance
    "$temporary/released-attendance.test" -test.run '^TestWriteReleasedAttendanceFixture$' -test.v -test.timeout 2m
)
"$go_command" test -count=1 -v -timeout=5m -run '^TestConvertReleasedAttendanceDatabase$' ./internal/upgrade/v1
"$go_command" test -count=1 -v -timeout=2m -run '^TestReplayReleasedAttendance$' ./internal/attendance
"$temporary/released-attendance.test" -test.run '^TestReleasedAttendanceRejectsUpgradedSchema$' -test.v -test.timeout 2m
printf 'Released sources: %s %s\n' "$released_commit" "$attendance_commit"
"$go_command" version
