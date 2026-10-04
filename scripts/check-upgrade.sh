#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
released_commit=8949a3d6e5b3d7536549f42a4c597393fccab62a
bash "$root/scripts/ensure-upgrade-source.sh" "$released_commit"
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
mkdir "$temporary/released" "$temporary/data"
git archive "$released_commit" | tar -x -C "$temporary/released"
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
unset NONBIRI_UPGRADE_MASTER_KEY_FILE
(
    # The released binary creates the populated source using its own schema.
    unset NONBIRI_RACE_TEMPLATE_PATH NONBIRI_RACE_TEMPLATE_SHA256 NONBIRI_RACE_TEMPLATE_ID
    cd "$temporary/released"
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-wallet.test" ./internal/ledger
    "$temporary/released-wallet.test" -test.run '^TestWriteReleasedUpgradeFixture$' -test.v -test.timeout 2m
)
"$go_command" test -count=1 -v -timeout=10m -run '^TestUpgradeFromReleasedBinary$' ./internal/db
"$go_command" test -count=1 -v -timeout=2m -run '^TestEconomyAuditCatchUpFromReleasedBinary$' ./internal/economyaudit
printf 'Released source: %s\n' "$released_commit"
"$go_command" version
