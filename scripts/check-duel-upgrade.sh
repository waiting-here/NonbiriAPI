#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
released_commit=bd6198ceccb59dc8b8e0143831a94e94340235d1
test "$(git rev-parse 'v1.0.0-rc.1^{commit}')" = "$released_commit"
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
source = (root / "internal/db/testdata/released_gameplay_fixture_test.go.txt").read_text(encoding="utf-8")
source = source.replace("gameWireFixture", "releasedGameWireFixture").replace("newGameWireFixture", "newReleasedGameWireFixture")
source = source.replace("f94972e6544ee5020c6a16451d4213c0cdef61b51814b6216ba9291e3db9734c", "5e443ca3f99ad1903ed03af718c0c6a1b93b06499740dba201006d132396bc37")
assert source.count("charge != 5") == 1
source = source.replace("charge != 5", "charge != 7").replace("old cap was not reproduced", "released actual charge was not preserved")
for indent, user in [("\t", "userID"), ("\t\t", "id")]:
    needle = indent + 'external, err := ledger.CodedAccount(ctx, tx, "external")'
    assert source.count("\n" + needle + "\n") == 1
    source = source.replace("\n" + needle + "\n", "\n" + indent + f'if _, err := ledger.CreateUserAssetAccount(ctx, tx, {user}, ledger.Game, time.Now().Unix()); err != nil {{ t.Fatal(err) }}\n' + needle + "\n")
gameplay = temporary / "gameplay.go"
gameplay.write_text(source, encoding="utf-8", newline="\n")
replacements = {
    str(legacy / "released_gameplay_fixture_test.go"): str(gameplay),
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
"$go_command" test -count=1 -v -run '^TestDuelUpgradeFromReleasedBinary$' ./internal/db
NONBIRI_DUAL_GAMEPLAY_FIXTURE="$NONBIRI_GAMEPLAY_FIXTURE" NONBIRI_DUAL_BILLING_FIXTURE="$NONBIRI_BILLING_FIXTURE" \
    "$go_command" test -count=1 -v -run '^TestReleased(DualAsset(Gameplay|Billing)|Progression)Upgrade$' .
"$temporary/released-wallet.test" -test.run '^TestReleasedRejectsDuelUpgrade$' -test.v -test.timeout 2m
printf 'Released source: %s\n' "$released_commit"
"$go_command" version
sha256sum "$temporary/data/"*.db
