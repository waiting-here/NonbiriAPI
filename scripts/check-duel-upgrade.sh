#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
released_commit=db959c64674afc531046a63066de0464725d439c
test "$(git rev-parse "$released_commit^{commit}")" = "$released_commit"
temporary_base=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "$temporary_base/nonbiri-upgrade.XXXXXXXX")
cleanup() {
    if [[ "${NONBIRI_UPGRADE_KEEP_TEMP:-0}" == 1 ]]; then
        printf 'Upgrade fixture workspace: %s\n' "$temporary"
        return
    fi
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
root, temporary = map(pathlib.Path, sys.argv[1:])
legacy = temporary / "released"
replacements = {
    str(legacy / "internal/ledger/released_governance_facts_test.go"): str(root / "internal/db/testdata/released_governance_facts_test.go.txt"),
    str(legacy / "released_gameplay_fixture_test.go"): str(root / "internal/db/testdata/released_gameplay_fixture_test.go.txt"),
    str(legacy / "released_progression_fixture_test.go"): str(root / "internal/db/testdata/released_progression_fixture_test.go.txt"),
    str(legacy / "internal/ledger/released_dual_wallet_fixture_test.go"): str(root / "internal/db/testdata/released_dual_wallet_fixture_test.go.txt"),
}
(temporary / "overlay.json").write_text(json.dumps({"Replace": replacements}), encoding="utf-8", newline="\n")
PY
export CGO_ENABLED=0
export NONBIRI_DUAL_WALLET_FIXTURE="$temporary/data/wallet.db"
export NONBIRI_DUAL_UPGRADED_FIXTURE="$temporary/data/upgraded.db"
export NONBIRI_GAMEPLAY_FIXTURE="$temporary/data/gameplay.db"
export NONBIRI_BILLING_FIXTURE="$temporary/data/billing.db"
export NONBIRI_DUAL_DUEL_FIXTURE="$temporary/data/duel.db"
export NONBIRI_DUAL_BLACKJACK_FIXTURE="$temporary/data/blackjack.db"
(
    cd "$temporary/released"
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-app.test" .
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-wallet.test" ./internal/ledger
    "$temporary/released-app.test" -test.run '^TestWriteReleased(Gameplay|Billing|Progression)Fixture$' -test.v -test.timeout 2m
    "$temporary/released-wallet.test" -test.run '^TestWriteReleasedDualWalletFixture$' -test.v -test.timeout 2m
)
"$go_command" test -count=1 -v -run '^TestGovernanceUpgradeFromReleasedBinary$' ./internal/db
NONBIRI_DUAL_GAMEPLAY_FIXTURE="$NONBIRI_GAMEPLAY_FIXTURE" NONBIRI_DUAL_BILLING_FIXTURE="$NONBIRI_BILLING_FIXTURE" \
    "$go_command" test -count=1 -v -run '^TestReleased(DualAsset(Gameplay|Billing)|Progression)Upgrade$' .
"$temporary/released-wallet.test" -test.run '^TestReleasedRejectsDuelUpgrade$' -test.v -test.timeout 2m
printf 'Released source: %s\n' "$released_commit"
"$go_command" version
sha256sum "$temporary/data/"*.db
