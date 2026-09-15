#!/usr/bin/env bash
set -euo pipefail

# Exercise actual released code, including tests that need a populated old database.
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
released_commit=74be70ae0e19d39219ff610de3e6dc3a9c058d38
test "$(git rev-parse 'v1.0.0-beta.3^{commit}')" = "$released_commit"
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
root, temporary = map(pathlib.Path, sys.argv[1:])
legacy = temporary / "released"
replacements = {
    str(legacy / "released_gameplay_fixture_test.go"): str(root / "internal/db/testdata/released_gameplay_fixture_test.go.txt"),
    str(legacy / "internal/ledger/legacy_wallet_fixture_test.go"): str(root / "internal/db/testdata/legacy_wallet_fixture_test.go.txt"),
}
(temporary / "overlay.json").write_text(json.dumps({"Replace": replacements}), encoding="utf-8", newline="\n")
PY
export CGO_ENABLED=0
export NONBIRI_LEGACY_FIXTURE="$temporary/data/wallet.db"
export NONBIRI_GAMEPLAY_FIXTURE="$temporary/data/gameplay.db"
export NONBIRI_BILLING_FIXTURE="$temporary/data/billing.db"
export NONBIRI_UPGRADED_FIXTURE="$temporary/data/upgraded.db"
(
    cd "$temporary/released"
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-app.test" .
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-wallet.test" ./internal/ledger
    "$temporary/released-app.test" -test.run '^TestWriteReleased(Gameplay|Billing)Fixture$' -test.v
    "$temporary/released-wallet.test" -test.run '^TestWriteLegacyWalletFixture$' -test.v
)
"$go_command" test -count=1 -v -run '^TestDualAssetUpgradeFromReleasedBinary$' ./internal/db
"$go_command" test -count=1 -v -run '^TestReleased(GameplayUpgradeAndRecovery|BillingUpgradePreservesTerminalAndSettlesActual)$' .
"$temporary/released-wallet.test" -test.run '^TestLegacyRejectsUpgradedFixture$' -test.v
printf 'Released source: %s\n' "$released_commit"
"$go_command" version
sha256sum "$temporary/data/"*.db
bash scripts/check-duel-upgrade.sh
